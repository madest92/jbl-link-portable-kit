package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

var fastTiming = timing{
	Guard: 50 * time.Millisecond, NoticeWait: 10 * time.Millisecond,
	SayStart: 300 * time.Millisecond, ShortStart: 300 * time.Millisecond,
	SpeechMax: 2 * time.Second, Await: time.Second,
}

// fakeAlice — Алиса для тестов: на say говорит (2 → 0), на listen слушает (1)
type fakeAlice struct {
	mu  sync.Mutex
	log []string
	ev  chan event
}

func (f *fakeAlice) add(s string) {
	f.mu.Lock()
	f.log = append(f.log, s)
	f.mu.Unlock()
}
func (f *fakeAlice) cancel() { f.add("cancel") }
func (f *fakeAlice) listen() { f.add("listen"); f.ev <- event{Kind: evState, State: 1} }
func (f *fakeAlice) say(t string) {
	f.add("say " + t)
	go func() {
		time.Sleep(10 * time.Millisecond)
		f.ev <- event{Kind: evState, State: 2}
		time.Sleep(20 * time.Millisecond)
		f.ev <- event{Kind: evState, State: 0}
	}()
}

func (f *fakeAlice) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

type rig struct {
	t     *testing.T
	h     *hook
	alice *fakeAlice
	out   *syncBuf
	mu    sync.Mutex
	asked []string
	ctxs  []context.Context
}

// newRig — хук с поддельной Алисой; ask отвечает answer(q) через delay
func newRig(t *testing.T, delay time.Duration, answer func(q string) (string, int)) *rig {
	r := &rig{t: t, alice: &fakeAlice{ev: make(chan event, 64)}, out: &syncBuf{}}
	j := &journal{out: r.out, debugFile: "/nonexistent", place: newPlace(t.TempDir(), "/nonexistent")}
	r.h = &hook{
		t: fastTiming, j: j, io: r.alice, dlg: &dialog{}, state: newStateBox(),
		hasExa: func() bool { return true }, warmUp: func() {}, drop: func() {},
		ask: func(ctx context.Context, q string) (string, []string, int) {
			r.mu.Lock()
			r.asked = append(r.asked, q)
			r.ctxs = append(r.ctxs, ctx)
			r.mu.Unlock()
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return "", nil, 1
			}
			a, code := answer(q)
			return a, []string{"exa: 0.0 с"}, code
		},
	}
	r.h.start()
	go func() {
		for ev := range r.alice.ev {
			r.h.handle(ev)
		}
	}()
	return r
}

func (r *rig) send(lines ...string) {
	for _, l := range lines {
		ev, _ := parseLine(l)
		r.alice.ev <- ev
	}
}

// wait — пока в журнале не появится строка s
func (r *rig) wait(s string) {
	r.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(r.out.String(), s) {
		if time.Now().After(deadline) {
			r.t.Fatalf("нет %q в журнале:\n%s\nАлисе: %q", s, r.out.String(), r.alice.got())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (r *rig) said() []string {
	var s []string
	for _, l := range r.alice.got() {
		if strings.HasPrefix(l, "say ") {
			s = append(s, strings.TrimPrefix(l, "say "))
		}
	}
	return s
}

func echoAnswer(q string) (string, int) { return "Ответ про " + q + ".", 0 }

func TestTriggerDialog(t *testing.T) {
	r := newRig(t, 20*time.Millisecond, echoAnswer)
	r.send("@state 1", "Алиса, спроси у нейросети, кто такой Мерц", "@state 3")
	r.wait("llm: жду продолжения")
	got := r.said()
	if len(got) != 1 || !strings.HasPrefix(got[0], "Ответ про кто такой Мерц. sil <[300]> ") {
		t.Fatalf("сказано %q", got)
	}
	// в окне — уточнение: второй вопрос — с историей
	r.send("а сколько ему лет", "@state 3")
	r.wait("llm: диалог: уточнение")
	r.wait("ответ за")
	// и «нет, спасибо» — конец
	r.wait("llm: жду продолжения")
	time.Sleep(50 * time.Millisecond)
	r.send("нет, спасибо", "@state 0")
	r.wait("llm: диалог: отказ — разговор закончен")
	if !r.h.dlg.has() {
		t.Error("история стёрта после «нет»")
	}
}

func TestFallbackAfterNotice(t *testing.T) {
	r := newRig(t, 100*time.Millisecond, echoAnswer)
	r.send("@state 1", "кто выиграл чемпионат мира по хоккею", "@state 3",
		"@speech К сожалению, ничего найти не получилось.")
	r.wait("llm: Алиса не знает ответа")
	r.wait("llm: озвучено")
	got := r.said()
	if len(got) != 2 || got[0] != llmNotice || !strings.HasPrefix(got[1], "Ответ про кто выиграл чемпионат мира по хоккею.") {
		t.Fatalf("сказано %q", got)
	}
}

// TestInterrupted — перебили во время ожидания Exa: следующий вопрос отвечен
func TestInterrupted(t *testing.T) {
	r := newRig(t, 200*time.Millisecond, echoAnswer)
	r.send("@state 1", "загугли кто сейчас канцлер германии", "@state 3")
	r.wait("llm: фраза-триггер")
	time.Sleep(30 * time.Millisecond)
	r.send("@state 1", "загугли кто сейчас папа римский", "@state 3")
	r.wait("llm: озвучено")
	r.mu.Lock()
	first := r.ctxs[0].Err()
	r.mu.Unlock()
	if first == nil {
		t.Error("первый вопрос не отменён")
	}
	got := r.said()
	if len(got) != 1 || !strings.Contains(got[0], "папа римский") {
		t.Errorf("сказано %q", got)
	}
}

func TestEmptyTriggerThenQuestion(t *testing.T) {
	r := newRig(t, 10*time.Millisecond, echoAnswer)
	r.send("@state 1", "Алиса, спроси у нейросети", "@state 3")
	r.wait("llm: жду вопрос")
	time.Sleep(20 * time.Millisecond) // listen → @state 1
	r.send("сколько лет вселенной", "@state 3")
	r.wait("llm: вопрос после «Что спросить?»")
	r.wait("llm: озвучено")
	if got := r.said(); len(got) != 2 || got[0] != llmEmpty || !strings.HasPrefix(got[1], "Ответ про сколько лет вселенной.") {
		t.Errorf("сказано %q", got)
	}
}

func TestResetWithQuestion(t *testing.T) {
	r := newRig(t, 10*time.Millisecond, echoAnswer)
	r.h.dlg.add("старый вопрос", "старый ответ")
	r.send("@state 1", "Новый разговор: кто сейчас папа римский", "@state 3")
	r.wait("llm: новый разговор, сразу вопрос")
	r.wait("ответ за")
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.asked) != 1 || r.asked[0] != "кто сейчас папа римский" {
		t.Errorf("спрошено %q", r.asked)
	}
}

func TestDialogEnd(t *testing.T) {
	r := newRig(t, 10*time.Millisecond, func(q string) (string, int) { return "Всего доброго.", 2 })
	r.send("@state 1", "загугли пока", "@state 3")
	r.wait("llm: озвучено")
	time.Sleep(50 * time.Millisecond)
	for _, l := range r.alice.got() {
		if l == "listen" {
			t.Error("после конца разговора открыт микрофон")
		}
	}
}

func TestErrorPhrases(t *testing.T) {
	r := newRig(t, 10*time.Millisecond, func(q string) (string, int) { return "ERR 429 Too many", 1 })
	r.send("@state 1", "загугли курс доллара", "@state 3")
	r.wait("llm: озвучено")
	if got := r.said(); len(got) != 1 || got[0] != llmLimit {
		t.Errorf("сказано %q", got)
	}
}

func TestGuardRecancel(t *testing.T) {
	r := newRig(t, 200*time.Millisecond, echoAnswer)
	r.send("@state 1", "загугли погоду", "@state 3", "@state 2") // Алиса заговорила после отмены
	r.wait("повторная отмена")
}

func TestDialogTTL(t *testing.T) {
	d := &dialog{}
	d.add("q", "a")
	d.at = time.Now().Add(-dialogTTL - time.Second)
	d.expire()
	if d.has() {
		t.Error("история не истекла")
	}
	for i := 0; i < dialogKeep+3; i++ {
		d.add("q", "a")
	}
	if len(d.pairs) != dialogKeep {
		t.Errorf("пар %d", len(d.pairs))
	}
}
