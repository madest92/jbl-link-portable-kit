package main

import (
	"testing"
	"time"
)

func TestRuleFor(t *testing.T) {
	for _, c := range []struct{ in, action, q string }{
		{"Алиса, спроси у нейросети, кто такой Фридрих Мерц?", "@llm", "кто такой Фридрих Мерц"},
		{"Алиса, спроси у нейросети", "@llm", ""},
		{"Узнай у ИИ сколько лет вселенной", "@llm", "сколько лет вселенной"},
		{"загугли кто сейчас папа римский", "@llm", "кто сейчас папа римский"},
		{"Алиса, кто сейчас папа римский, погугли", "@llm", "кто сейчас папа римский"},
		{"Найди в интернете рецепт плова", "@llm", "рецепт плова"},
		{"Новый разговор: кто сейчас папа римский", "@reset", "кто сейчас папа римский"},
		{"давайте начнём новую беседу", "@reset", ""},
		{"Забудь наш разговор", "@reset", ""},
		{"что такое нейросеть", "", ""},
		{"спроси у нейросетки как дела", "@llm", "как дела"},
	} {
		a, q, ok := ruleFor(c.in)
		if a != c.action || q != c.q || ok != (c.action != "") {
			t.Errorf("%q → %q %q, want %q %q", c.in, a, q, c.action, c.q)
		}
	}
}

func TestOnlyWords(t *testing.T) {
	for _, c := range []struct {
		in      string
		no, yes bool
	}{
		{"Нет, спасибо", true, false},
		{"Алиса, нет", true, false},
		{"спасибо, всё понятно", true, false},
		{"Да", false, true},
		{"да, давай", false, true},
		{"нет, а кто его жена", false, false},
		{"включи музыку", false, false},
	} {
		if matches(noWords, c.in) != c.no || matches(yesWords, c.in) != c.yes {
			t.Errorf("%q: нет=%v да=%v", c.in, matches(noWords, c.in), matches(yesWords, c.in))
		}
	}
}

func TestFallback(t *testing.T) {
	for _, s := range []string{"К сожалению, ничего найти не получилось.", "По вашему запросу я ничего не нашла.",
		"Тут в двух словах не рассказать.", "Есть темы, в которых я могу ошибиться. Лучше промолчу."} {
		if !matches(fallback, s) {
			t.Errorf("не поймано: %q", s)
		}
	}
	for _, s := range []string{"Нашла в интернете: Лев XIV.", "Ой, что-то пошло не так. Пожалуйста, попробуйте ещё раз."} {
		if matches(fallback, s) {
			t.Errorf("поймано лишнее: %q", s)
		}
	}
}

func TestSearchQuery(t *testing.T) {
	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	for in, want := range map[string]string{
		"что приготовить на завтрак":     "что приготовить на завтрак",
		"какой сегодняшний курс доллара": "какой сегодняшний курс доллара",
		"Кто играл вчера":                "кто играл 21 ноября 2026",
		"погода завтра?":                 "погода 23 ноября 2026?",
		"вчера, сегодня и послезавтра":   "21 ноября 2026, 22 ноября 2026 и 24 ноября 2026",
		"позавчера\nсегодня":             "20 ноября 2026\n22 ноября 2026",
	} {
		if got := searchQuery(in, now); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestToday(t *testing.T) {
	load := func(n string) *time.Location { l, _ := time.LoadLocation(n); return l }
	at := time.Date(2026, 11, 8, 12, 27, 0, 0, time.UTC)
	for _, c := range []struct {
		loc   *time.Location
		known bool
		want  string
	}{
		{load("Asia/Makassar"), true, "Сегодня воскресенье, 8 ноября 2026 года. Сейчас 20:27 по местному времени (UTC+8)."},
		{load("Asia/Kolkata"), true, "Сегодня воскресенье, 8 ноября 2026 года. Сейчас 17:57 по местному времени (UTC+5:30)."},
		{load("America/St_Johns"), true, "Сегодня воскресенье, 8 ноября 2026 года. Сейчас 08:57 по местному времени (UTC-3:30)."},
		{time.UTC, true, "Сегодня воскресенье, 8 ноября 2026 года. Сейчас 12:27 по местному времени (UTC+0)."},
		{time.UTC, false, "Сегодня воскресенье, 8 ноября 2026 года."},
	} {
		if got := today(at.In(c.loc), c.known); got != c.want {
			t.Errorf("%v: %q", c.loc, got)
		}
	}
}
