package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---- состояние Алисы ----

// stateBox — последнее состояние Алисы; ch закрывается при каждой смене
type stateBox struct {
	mu    sync.Mutex
	st    int
	known bool
	ch    chan struct{}
}

func newStateBox() *stateBox { return &stateBox{ch: make(chan struct{})} }

func (b *stateBox) set(st int) {
	b.mu.Lock()
	b.st, b.known = st, true
	close(b.ch)
	b.ch = make(chan struct{})
	b.mu.Unlock()
}

func (b *stateBox) get() (int, bool, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.st, b.known, b.ch
}

// ---- история разговора ----

type dialog struct {
	mu    sync.Mutex
	pairs [][2]string
	at    time.Time // время последнего ответа
}

func (d *dialog) add(q, a string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pairs = append(d.pairs, [2]string{q, a})
	if len(d.pairs) > dialogKeep {
		d.pairs = d.pairs[len(d.pairs)-dialogKeep:]
	}
	d.at = time.Now()
}

func (d *dialog) clear() {
	d.mu.Lock()
	d.pairs, d.at = nil, time.Time{}
	d.mu.Unlock()
}

// expire — стереть историю старше dialogTTL
func (d *dialog) expire() {
	d.mu.Lock()
	if d.at.IsZero() || time.Since(d.at) >= dialogTTL {
		d.pairs, d.at = nil, time.Time{}
	}
	d.mu.Unlock()
}

func (d *dialog) has() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pairs) > 0
}

// text — «Вопрос: …», «Ответ: …» по строке
func (d *dialog) text() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var l []string
	for _, p := range d.pairs {
		l = append(l, "Вопрос: "+p[0], "Ответ: "+p[1])
	}
	return strings.Join(l, "\n")
}

// ---- вопрос в Exa ----

// answerer — вопрос → ответ Exa и шаги для журнала. r: 0 — ответ, 1 — ошибка («ERR …»),
// 2 — вид реплики из dialogEnd (конец разговора).
type answerer struct {
	exa     *exa
	place   *place
	dlg     *dialog
	keyFile string
	j       *journal
}

func readKey(f string) string {
	b, _ := os.ReadFile(f)
	k, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(k)
}

func hasKey(f string) bool {
	st, err := os.Stat(f)
	return err == nil && st.Size() > 0
}

func secs(d time.Duration) string {
	ms := d.Milliseconds()
	return fmt.Sprintf("%d.%d", ms/1000, ms%1000/100)
}

func (a *answerer) ask(ctx context.Context, q string) (string, []string, int) {
	key := readKey(a.keyFile)
	if key == "" {
		return "ERR нет ключа", nil, 1
	}
	a.place.refresh()
	geo, cc, region := a.place.lookup()
	now, known := a.place.now()
	date := today(now, known)
	// запрос в журнал — только если отличается от фразы
	if sq := searchQuery(q, now); sq != lc(q) {
		a.j.dlog("запрос: %s", sq)
	}
	dialog := a.dlg.has()
	var body string
	if dialog {
		body = dialogBody(searchQuery(fmt.Sprintf(dialogQuery, q, a.dlg.text()), now), date, geoJSON(geo, cc))
	} else {
		sq := searchQuery(q, now)
		if region != "" {
			sq += fmt.Sprintf(geoSuffix, region)
		}
		body = answerBody(sq, answerPrompt+" "+date, geoJSON(geo, cc))
	}
	t := time.Now()
	rctx, cancel := context.WithTimeout(ctx, answerTimeout)
	defer cancel()
	code, resp, err := a.exa.post(rctx, key, body)
	if ctx.Err() != nil {
		return "", nil, 1
	}
	if err != nil && errors.Is(rctx.Err(), context.DeadlineExceeded) {
		return "ERR таймаут", nil, 1
	}
	steps := []string{"exa: " + secs(time.Since(t)) + " с"}
	if geo != "" {
		// в журнал — регион, без координат
		r := region
		if r == "" {
			r = "координаты"
		}
		steps = append(steps, "место: "+r)
	}
	var ans string
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		err = fmt.Errorf("ERR нет ответа (%v)", err)
	} else {
		ans, err = parseAnswer(code, resp, dialog)
	}
	if err != nil {
		a.j.dlog("exa: %s", err)
		return err.Error(), steps, 1
	}
	kind := ""
	if dialog {
		kind, ans, _ = strings.Cut(ans, "\t")
	}
	a.j.dlog("exa: %s", ans)
	if dialog {
		k := kind
		if k == "" {
			k = "?"
		}
		if matches(endKinds, kind) {
			return ans, append(steps, "вид: "+k+" — разговор закончен"), 2
		}
		steps = append(steps, "вид: "+k)
	}
	return ans, steps, 0
}

// ---- хук ----

// speaker — команды Алисе (в 9910)
type speaker interface {
	cancel()         // stop_conversation
	listen()         // start_conversation
	say(text string) // текст её голосом
}

type hubSpeaker struct {
	hub *iohub
	j   *journal
}

func (s hubSpeaker) send(b []byte) {
	if err := s.hub.send(b); err != nil {
		s.j.logf("ошибка 9910: %v", err)
	}
}
func (s hubSpeaker) cancel()      { s.send(frameStop) }
func (s hubSpeaker) listen()      { s.send(frameListen) }
func (s hubSpeaker) say(t string) { s.send(sayFrame(t)) }

type job struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (j *job) running() bool {
	if j == nil {
		return false
	}
	select {
	case <-j.done:
		return false
	default:
		return true
	}
}

// hook — ответ Exa по фразе-триггеру и вместо «не знаю» Алисы, с диалогом. handle — из одной
// горутины, по порядку событий; обработчик вопроса (work) — своя горутина с отменой.
type hook struct {
	t      timing
	j      *journal
	io     speaker
	ask    func(ctx context.Context, q string) (string, []string, int)
	hasExa func() bool
	warmUp func() // открыть соединение с Exa заранее
	drop   func() // закрыть неиспользованное
	dlg    *dialog
	state  *stateBox

	said  atomic.Bool  // после отмены мы уже говорили — повторно не отменять
	turns atomic.Int32 // вопросов в разговоре

	awaitMu   sync.Mutex
	awaitOn   bool // микрофон открыли мы: следующая фраза — наша
	awaitAt   time.Time
	awaitMode string // "" — после «Что спросить?», dialog, dialogq — после ответа-вопроса

	// только у handle
	worker *job
	phrase string // последняя фраза Алисе — для «не знаю»
	pre    bool   // соединение открыто по @partial или нашему микрофону
	fpre   bool   // соединение открыто, пока Алиса думает
	guard  time.Time
	st     int
}

func (h *hook) start() {
	h.st = -1
	h.drop()
	h.awaitClear()
	h.turns.Store(0)
	h.dlg.clear()
}

func (h *hook) preconnect() {
	if h.hasExa() {
		h.warmUp()
	}
}

// awaiting — ждём ли вопрос (наш микрофон открыт не дольше t.Await)
func (h *hook) awaiting() (string, bool) {
	h.awaitMu.Lock()
	defer h.awaitMu.Unlock()
	return h.awaitMode, h.awaitOn && time.Since(h.awaitAt) < h.t.Await
}

func (h *hook) awaitClear() bool {
	h.awaitMu.Lock()
	defer h.awaitMu.Unlock()
	was := h.awaitOn
	h.awaitOn = false
	return was
}

// intercept — отменить ответ Алисы; t.Guard её речь отменяется повторно
func (h *hook) intercept() {
	h.said.Store(false)
	h.io.cancel()
	h.guard = time.Now().Add(h.t.Guard)
}

// startWorker — обработчик вопроса вместо прежнего
func (h *hook) startWorker(mode, q string) {
	if h.worker != nil {
		h.worker.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(j.done)
		defer cancel()
		h.work(ctx, mode, q)
	}()
	h.worker = j
}

// resetDialog — сброс истории; q — первый вопрос нового разговора
func (h *hook) resetDialog(q string) {
	h.intercept()
	if h.worker != nil {
		h.worker.cancel()
	}
	h.worker = nil
	h.dlg.clear()
	if q != "" {
		h.j.logf("llm: новый разговор, сразу вопрос")
		h.startWorker("trigger", q)
	} else {
		h.j.logf("llm: новый разговор")
		h.startWorker("reset", "")
	}
}

func (h *hook) handle(ev event) {
	switch ev.Kind {
	case evState:
		prev, st := h.st, ev.State
		h.st = st
		h.state.set(st)
		// заранее открытые соединения не пригодились — закрываем
		if h.pre && st != 1 {
			h.drop()
			h.pre = false
		}
		if h.fpre && st != 3 {
			h.drop()
			h.fpre = false
		}
		// наш микрофон, а человек промолчал — разговор закончен
		if prev == 1 && st != 1 && h.awaitClear() {
			h.j.logf("llm: тишина — разговор закончен")
		}
		switch st {
		case 1:
			// новый разговор: недосказанный ответ больше не нужен
			if h.worker.running() {
				h.worker.cancel()
				h.drop()
			}
			h.worker = nil
			// наш микрофон — соединение сразу
			if _, ok := h.awaiting(); ok {
				h.preconnect()
				h.pre = true
			}
		case 3:
			// Алиса думает — соединение заранее, на случай «не знаю»
			if h.phrase != "" && !h.worker.running() && h.hasExa() {
				h.preconnect()
				h.fpre = true
			}
		case 2:
			// заговорила после отмены — ещё раз, пока мы молчим
			if time.Now().Before(h.guard) && !h.said.Load() {
				h.io.cancel()
				h.j.logf("повторная отмена")
			}
		}
	case evPartial:
		if h.pre {
			return
		}
		if a, _, ok := ruleFor(ev.Text); ok && (a == "@llm" || a == "@reset") {
			h.preconnect()
			h.pre = true
		}
	case evSpeech:
		h.j.dlog("алиса: %s", ev.Text)
		if h.phrase == "" || !h.hasExa() || !matches(fallback, ev.Text) {
			return
		}
		h.intercept()
		h.fpre = false // соединение заберёт обработчик
		h.j.logf("llm: Алиса не знает ответа")
		h.startWorker("fallback", h.phrase)
		h.phrase = ""
	case evFinal:
		h.final(ev.Text)
	}
}

func (h *hook) final(line string) {
	h.j.dlog("пользователь: %s", line)
	if mode, ok := h.awaiting(); ok {
		h.awaitClear()
		h.pre, h.phrase = false, ""
		if a, q, ok := ruleFor(line); ok && a == "@reset" {
			h.resetDialog(q)
			return
		}
		switch {
		case mode == "":
			h.intercept()
			h.j.logf("llm: вопрос после «Что спросить?»")
			h.startWorker("trigger", line)
		case mode == "dialog" && matches(noWords, line):
			h.intercept()
			h.drop()
			h.j.logf("llm: диалог: отказ — разговор закончен")
		case mode == "dialog" && matches(yesWords, line):
			h.intercept()
			h.j.logf("llm: диалог: согласие без вопроса")
			h.startWorker("dialog", "")
		default:
			h.intercept()
			h.j.logf("llm: диалог: уточнение")
			h.startWorker("dialog", line)
		}
		return
	}
	h.phrase = line
	a, q, ok := ruleFor(line)
	if !ok {
		h.pre = false
		return
	}
	if !h.hasExa() {
		return
	}
	h.pre, h.phrase = false, ""
	switch a {
	case "@llm":
		h.intercept()
		h.j.logf("llm: фраза-триггер")
		h.startWorker("trigger", q)
	case "@reset":
		h.resetDialog(q)
	}
}

// ---- обработчик вопроса ----

// say — текст голосом, если обработчик ещё нужен
func (h *hook) say(ctx context.Context, text string) bool {
	if ctx.Err() != nil {
		return false
	}
	h.said.Store(true)
	h.io.say(text)
	return true
}

// waitSpoken — ждёт конца речи (SPEAKING → другое); не началась за start — false
func (h *hook) waitSpoken(ctx context.Context, start time.Duration) (string, bool) {
	t0 := time.Now()
	var t1 time.Time
	limit := time.NewTimer(start)
	defer limit.Stop()
	max := time.NewTimer(h.t.SpeechMax)
	defer max.Stop()
	for {
		st, known, ch := h.state.get()
		switch {
		case known && st == 2:
			if t1.IsZero() {
				t1 = time.Now()
			}
		case !t1.IsZero() && known:
			return fmt.Sprintf("начало через %s с, речь %s с", secs(t1.Sub(t0)), secs(time.Since(t1))), true
		}
		select {
		case <-ch:
		case <-limit.C:
			if t1.IsZero() {
				return "", false
			}
		case <-max.C:
			return "речь дольше 2 мин", true
		case <-ctx.Done():
			return "", false
		}
	}
}

// speak — сказать и дождаться конца; речь не началась (плохая сеть) — повтор
func (h *hook) speak(ctx context.Context, text string) bool {
	if !h.say(ctx, text) {
		return false
	}
	spoke, ok := h.waitSpoken(ctx, h.t.SayStart)
	if !ok {
		if ctx.Err() != nil {
			return false
		}
		h.j.logf("llm: повтор озвучки")
		if !h.say(ctx, text) {
			return false
		}
		if spoke, ok = h.waitSpoken(ctx, h.t.SayStart); !ok {
			return false
		}
	}
	h.j.logf("llm: озвучено: %s", spoke)
	return true
}

// awaitQuestion — микрофон для следующей фразы
func (h *hook) awaitQuestion(ctx context.Context, mode string) {
	if ctx.Err() != nil {
		return
	}
	h.awaitMu.Lock()
	h.awaitOn, h.awaitAt, h.awaitMode = true, time.Now(), mode
	h.awaitMu.Unlock()
	go h.io.listen()
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-ctx.Done():
		return false
	}
}

func endsWithQuestion(s string) bool {
	return strings.HasSuffix(s, "?") || strings.HasSuffix(s, "?»") || strings.HasSuffix(s, "?)")
}

// work — вопрос в Exa и ответ голосом, потом приглашение и микрофон. mode: trigger, fallback
// (сначала llmNotice), dialog, reset; пустой q — спросить, что спросить.
func (h *hook) work(ctx context.Context, mode, q string) {
	t0 := time.Now()
	// «не знаю» — новый вопрос Алисе: прежний разговор не нужен
	switch mode {
	case "fallback":
		h.dlg.clear()
		h.turns.Store(0)
	case "dialog":
		h.dlg.expire()
	default:
		h.dlg.expire()
		h.turns.Store(0)
	}
	if q == "" {
		h.drop()
		phrase := llmEmpty
		switch mode {
		case "dialog":
			phrase = dialogHear
		case "reset":
			phrase = dialogReset
		}
		if !h.say(ctx, phrase) {
			return
		}
		h.waitSpoken(ctx, h.t.ShortStart)
		if ctx.Err() != nil {
			return
		}
		h.j.logf("llm: жду вопрос")
		m := ""
		if mode == "dialog" {
			m = "dialog"
		}
		h.awaitQuestion(ctx, m)
		return
	}
	var notice chan struct{}
	if mode == "fallback" {
		// notice закрывается, когда уведомление договорено
		notice = make(chan struct{})
		go func() {
			defer close(notice)
			if sleepCtx(ctx, h.t.NoticeWait) && h.say(ctx, llmNotice) {
				h.waitSpoken(ctx, h.t.ShortStart)
			}
		}()
	}
	ans, steps, r := h.ask(ctx, q)
	if ctx.Err() != nil {
		return
	}
	s := strings.Join(steps, ", ")
	if s != "" {
		s += ", "
	}
	invite, more := "", false
	switch r {
	case 2:
		// конец разговора: без приглашения
		h.j.logf("llm: %sза %s с", s, secs(time.Since(t0)))
		if ans != "" {
			h.speak(ctx, ans)
		}
		return
	case 0:
		h.j.logf("llm: %sответ за %s с", s, secs(time.Since(t0)))
		h.dlg.add(q, ans)
		n := h.turns.Add(1)
		// ответ-вопрос (загадка) — без приглашения, но с микрофоном, даже после dialogTurns
		if endsWithQuestion(ans) {
			more = dialogTurns > 0
		} else if n < dialogTurns {
			more, invite = true, dialogAsk[rand.IntN(len(dialogAsk))]
		}
	default:
		if ans == "" {
			ans = "ERR"
		}
		h.j.logf("llm: %s%s", s, ans)
		if strings.HasPrefix(ans, "ERR 429 ") {
			ans = llmLimit
		} else {
			ans = llmError
		}
	}
	if notice != nil {
		// новый фрейм оборвал бы уведомление
		select {
		case <-notice:
		case <-ctx.Done():
			return
		}
	}
	// приглашение — в том же фрейме: отдельный — ещё 1–2,5 с синтеза
	if invite != "" {
		ans += " " + dialogPause + " " + invite
	}
	if !h.speak(ctx, ans) || !more {
		return
	}
	h.j.logf("llm: жду продолжения")
	// dialogq: после ответа-вопроса «да» и «нет» — тоже уточнение
	if invite != "" {
		h.awaitQuestion(ctx, "dialog")
	} else {
		h.awaitQuestion(ctx, "dialogq")
	}
}
