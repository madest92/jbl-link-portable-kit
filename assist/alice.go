package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Канал Алисы: кадр — 4 байта длины (LE) + protobuf QuasarMessage. 9875 — aliced (события),
// 9910 — iohub_clients (io_control, как кнопка).
const (
	alicedAddr = "127.0.0.1:9875"
	iohubAddr  = "127.0.0.1:9910"
	frameMax   = 600000
)

// ---- protobuf ----

func putVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// pb — поле field типа length-delimited
func pb(field int, payload []byte) []byte {
	b := putVarint(nil, uint64(field)<<3|2)
	b = putVarint(b, uint64(len(payload)))
	return append(b, payload...)
}

func frame(msg []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(len(msg)))
	return append(b, msg...)
}

func varint(b []byte, i int) (uint64, int, bool) {
	var x uint64
	for s := uint(0); i < len(b) && s < 64; s += 7 {
		c := b[i]
		i++
		x |= uint64(c&0x7f) << s
		if c < 0x80 {
			return x, i, true
		}
	}
	return 0, i, false
}

// find — первое поле field верхнего уровня: число или байты
func find(b []byte, field int, wantBytes bool) (uint64, []byte, bool) {
	for i := 0; i < len(b); {
		tag, j, ok := varint(b, i)
		if !ok {
			return 0, nil, false
		}
		i = j
		f, w := int(tag>>3), tag&7
		switch w {
		case 0:
			x, j, ok := varint(b, i)
			if !ok {
				return 0, nil, false
			}
			i = j
			if f == field && !wantBytes {
				return x, nil, true
			}
		case 1:
			i += 8
		case 5:
			i += 4
		case 2:
			n, j, ok := varint(b, i)
			if !ok || n > uint64(len(b)-j) {
				return 0, nil, false
			}
			i = j + int(n)
			if f == field && wantBytes {
				return 0, b[j:i], true
			}
		default:
			return 0, nil, false
		}
	}
	return 0, nil, false
}

// text — строка поля: до max байт по границе символа, переводы строк — пробелом
func text(b []byte, max int) string {
	if len(b) > max {
		b = b[:max]
		for len(b) > 0 && !utf8.Valid(b) {
			b = b[:len(b)-1]
		}
	}
	return strings.NewReplacer("\n", " ", "\r", " ").Replace(string(b))
}

// ---- события ----

type evKind int

const (
	evState   evKind = iota // State: 0 простой, 1 слушает, 2 говорит, 3 думает
	evPartial               // Text: распознано пока столько
	evFinal                 // Text: финальная фраза
	evSpeech                // Text: ответ Алисы, приходит до речи
)

type event struct {
	Kind  evKind
	State int
	Text  string
}

// String — строка формата команды text (её читает consume)
func (e event) String() string {
	switch e.Kind {
	case evState:
		return "@state " + string(rune('0'+e.State))
	case evPartial:
		return "@partial " + e.Text
	case evSpeech:
		return "@speech " + e.Text
	}
	return e.Text
}

// parseLine — событие из строки формата text
func parseLine(l string) (event, bool) {
	switch {
	case strings.HasPrefix(l, "@state "):
		s := strings.TrimPrefix(l, "@state ")
		if len(s) != 1 || s[0] < '0' || s[0] > '9' {
			return event{}, false
		}
		return event{Kind: evState, State: int(s[0] - '0')}, true
	case strings.HasPrefix(l, "@partial "):
		return event{Kind: evPartial, Text: strings.TrimPrefix(l, "@partial ")}, true
	case strings.HasPrefix(l, "@speech "):
		return event{Kind: evSpeech, Text: strings.TrimPrefix(l, "@speech ")}, true
	case l == "":
		return event{}, false
	}
	return event{Kind: evFinal, Text: l}, true
}

// parser — кадры → события. AliceState (поле 3): 1 — состояние, 2 — распознанный текст,
// 3 → 1 — ответ, 7 — id запроса. Финальная фраза — последний текст до выхода из состояния 1.
type parser struct {
	status    int
	text, id  string
	printedID string
	speech    string
}

func newParser() *parser { return &parser{status: -1} }

func (p *parser) handle(msg []byte, emit func(event)) {
	_, st3, ok := find(msg, 3, true)
	if !ok {
		return
	}
	state := 0 // нулевое значение protobuf не передаёт
	if v, _, ok := find(st3, 1, false); ok {
		state = int(v)
	}
	if state == 1 {
		if _, b, ok := find(st3, 2, true); ok && len(b) > 0 {
			t := text(b, 1023)
			if t != p.text {
				p.text = t
				emit(event{Kind: evPartial, Text: t})
			}
			p.id = ""
			if _, b, ok := find(st3, 7, true); ok {
				p.id = text(b, 63)
			}
		}
	} else if p.status == 1 && p.text != "" && (p.id == "" || p.id != p.printedID) {
		emit(event{Kind: evFinal, Text: p.text})
		p.printedID = p.id
	}
	if state != 1 {
		p.text = ""
	}
	if state != p.status {
		p.status = state
		p.speech = ""
		emit(event{Kind: evState, State: state})
	}
	if state == 3 {
		if _, vr, ok := find(st3, 3, true); ok {
			if _, b, ok := find(vr, 1, true); ok && len(b) > 0 {
				t := text(b, 2047)
				if t != p.speech {
					p.speech = t
					emit(event{Kind: evSpeech, Text: t})
				}
			}
		}
	}
}

func readFrame(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(h[:])
	if n == 0 || n > frameMax {
		return nil, errors.New("плохая длина кадра")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// listenAlice — события Алисы до отмены ctx; при обрыве переподключается
func listenAlice(ctx context.Context, pause time.Duration, emit func(event)) {
	for ctx.Err() == nil {
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", alicedAddr)
		if err == nil {
			stop := context.AfterFunc(ctx, func() { c.Close() })
			p := newParser()
			for {
				msg, err := readFrame(c)
				if err != nil {
					break
				}
				p.handle(msg, emit)
			}
			stop()
			c.Close()
		}
		select {
		case <-ctx.Done():
		case <-time.After(pause):
		}
	}
}

// ---- io_control в 9910 ----

var (
	frameStop   = frame(pb(88, pb(29, nil))) // stop_conversation: отменяет ответ, обрывает речь
	frameListen = frame(pb(88, pb(30, nil))) // start_conversation: открывает микрофон
)

// sayFrame — текст голосом Алисы, дословно, без микрофона (vins_server_action,
// repeat_after_me_semantic_frame). Речь — через 1–2,5 с; новый фрейм обрывает текущую.
func sayFrame(s string) []byte {
	s = strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s)
	var v bytes.Buffer
	enc := json.NewEncoder(&v)
	enc.SetEscapeHTML(false) // разметка синтеза «sil <[300]>» — как есть
	enc.Encode(s)
	js := `{"type":"server_action","name":"@@mm_semantic_frame","payload":{"typed_semantic_frame":{"repeat_after_me_semantic_frame":{"text":{"string_value":` +
		strings.TrimSuffix(v.String(), "\n") + `}}},"analytics":{"origin":"Scenario","purpose":"repeat_after_me"}}}`
	return frame(pb(88, pb(50, pb(1, []byte(js)))))
}

// vinsFrame — текст в Алису, как сказанный голосом (vins_call)
func vinsFrame(s string) []byte { return frame(pb(88, pb(47, pb(1, []byte(s))))) }

// iohub — кадры в 9910. Сервер сразу шлёт ~9 КБ состояния; закрыться, не дочитав, — RST, и команда
// теряется. Поэтому соединение дочитывается и держится hold.
type iohub struct {
	hold time.Duration
	wg   sync.WaitGroup // незакрытые соединения: CLI ждёт их перед выходом
}

func (h *iohub) send(b []byte) error {
	c, err := net.DialTimeout("tcp", iohubAddr, 2*time.Second)
	if err != nil {
		return err
	}
	c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write(b); err != nil {
		c.Close()
		return err
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		c.SetReadDeadline(time.Now().Add(h.hold))
		io.Copy(io.Discard, c)
		c.Close()
	}()
	return nil
}
