package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAnswer(t *testing.T) {
	long := strings.Repeat("Слово слово слово. ", 120) // ~3 КБ
	for _, c := range []struct {
		code   int
		body   string
		dialog bool
		want   string
	}{
		{200, `{"answer":{"response":"Фридрих Мерц [1]. Канцлер с 2025 года [1, 3][2-4]."},"citations":[]}`, false, "Фридрих Мерц. Канцлер с 2025 года."},
		{200, `{"answer":{"response":"Ответ\nв две строки 😀 и\ttab."}}`, false, "Ответ в две строки и tab."},
		{200, `{"answer":{"response":"Начало ответа. Оборванный хвост без точ"}}`, false, "Начало ответа."},
		{200, `{"answer":{"response":"Цитата «как есть»"}}`, false, "Цитата «как есть»"},
		{200, `{"answer":{"kind":" благодарность ","response":"Рада помочь!"}}`, true, "благодарность\tРада помочь!"},
		{200, `{"answer":{"response":""}}`, false, "ERR 200 пустой ответ"},
		{401, `{"error":"Invalid API key"}`, false, "ERR 401 Invalid API key"},
		{429, `{"error":{"message":"Too many"}}`, false, "ERR 429 Too many"},
		{502, `<html>bad gateway</html>`, false, "ERR 502 "},
	} {
		got, err := parseAnswer(c.code, []byte(c.body), c.dialog)
		if err != nil {
			got = err.Error()
		}
		if got != c.want {
			t.Errorf("%d %s\n got %q\nwant %q", c.code, c.body, got, c.want)
		}
	}
	got, _ := parseAnswer(200, []byte(`{"answer":{"response":"`+long+`"}}`), false)
	if len(got) > answerMax || !strings.HasSuffix(got, ".") {
		t.Errorf("длинный: %d байт, хвост %q", len(got), got[len(got)-10:])
	}
}

func TestBodies(t *testing.T) {
	q := "кто \"канцлер\"\\ германии\nи\tвчера"
	for name, body := range map[string]string{
		"answer": answerBody(q, answerPrompt+" Сегодня.", geoJSON("-8.650 115.220", "ID")),
		"dialog": dialogBody(q, "Сегодня.", ""),
	} {
		var v struct {
			Query, Model, SystemPrompt string
			OutputSchema               map[string]any
			UserLocation               *struct{ Country string }
		}
		if err := json.Unmarshal([]byte(body), &v); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, body)
		}
		if v.Model != answerModel || v.OutputSchema["type"] != "object" || !strings.Contains(v.SystemPrompt, "Сегодня.") {
			t.Errorf("%s: %+v", name, v)
		}
		if name == "answer" && (v.UserLocation == nil || v.UserLocation.Country != "ID" || v.Query != `кто "канцлер"\ германии и вчера`) {
			t.Errorf("answer: %q %+v", v.Query, v.UserLocation)
		}
		if name == "dialog" && v.Query != "кто \"канцлер\"\\ германии\nи вчера" {
			t.Errorf("dialog: %q", v.Query)
		}
	}
}
