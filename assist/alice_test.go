package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrames(t *testing.T) {
	// проверены на колонке
	if want := []byte("\x06\x00\x00\x00\xc2\x05\x03\xea\x01\x00"); !bytes.Equal(frameStop, want) {
		t.Errorf("stop % x", frameStop)
	}
	if want := []byte("\x06\x00\x00\x00\xc2\x05\x03\xf2\x01\x00"); !bytes.Equal(frameListen, want) {
		t.Errorf("listen % x", frameListen)
	}
}

func TestSayFrame(t *testing.T) {
	f := sayFrame("Ответ \"в кавычках\"\nи строкой sil <[300]> Продолжим?")
	_, a, _ := find(f[4:], 88, true)
	_, b, _ := find(a, 50, true)
	_, js, ok := find(b, 1, true)
	if !ok {
		t.Fatal("нет server_action")
	}
	if !bytes.Contains(js, []byte("sil <[300]>")) {
		t.Errorf("разметка экранирована: %s", js)
	}
	var v struct {
		Payload struct {
			TSF struct {
				R struct {
					Text struct {
						S string `json:"string_value"`
					} `json:"text"`
				} `json:"repeat_after_me_semantic_frame"`
			} `json:"typed_semantic_frame"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(js, &v); err != nil {
		t.Fatal(err)
	}
	if got := v.Payload.TSF.R.Text.S; got != `Ответ "в кавычках" и строкой sil <[300]> Продолжим?` {
		t.Errorf("текст %q", got)
	}
}

// aliceState — кадр с AliceState: состояние, текст, id, ответ
func aliceState(st int, phrase, id, speech string) []byte {
	var s []byte
	if st != 0 {
		s = append(s, putVarint(nil, 1<<3)...)
		s = putVarint(s, uint64(st))
	}
	if phrase != "" {
		s = append(s, pb(2, []byte(phrase))...)
	}
	if speech != "" {
		s = append(s, pb(3, pb(1, []byte(speech)))...)
	}
	if id != "" {
		s = append(s, pb(7, []byte(id))...)
	}
	return append(pb(169, []byte{1}), pb(3, s)...) // чужое поле впереди — пропускается
}

func TestParser(t *testing.T) {
	var got []string
	p := newParser()
	emit := func(e event) { got = append(got, e.String()) }
	for _, m := range [][]byte{
		aliceState(0, "", "", ""),
		aliceState(1, "", "", ""),
		aliceState(1, "алиса", "r1", ""),
		aliceState(1, "Алиса, спроси", "r1", ""),
		aliceState(1, "Алиса, спроси", "r1", ""), // тот же текст — без события
		aliceState(3, "", "", "К сожалению,\nничего не нашлось."),
		aliceState(2, "", "", ""),
		aliceState(1, "Алиса, спроси", "r1", ""), // тот же запрос ещё раз — финальной не будет
		aliceState(0, "", "", ""),
	} {
		p.handle(m, emit)
	}
	want := []string{
		"@state 0", "@state 1", "@partial алиса", "@partial Алиса, спроси",
		"Алиса, спроси", "@state 3", "@speech К сожалению, ничего не нашлось.", "@state 2",
		"@partial Алиса, спроси", "@state 1", "@state 0", // текст раньше смены состояния
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("события\n got %q\nwant %q", got, want)
	}
}

func TestParseLine(t *testing.T) {
	for _, l := range []string{"@state 3", "@partial кто", "@speech Ответ.", "Алиса, загугли"} {
		ev, ok := parseLine(l)
		if !ok || ev.String() != l {
			t.Errorf("%q → %v %q", l, ok, ev)
		}
	}
}

func TestText(t *testing.T) {
	if got := text([]byte("привет"), 5); got != "пр" {
		t.Errorf("обрезка по символу: %q", got)
	}
}
