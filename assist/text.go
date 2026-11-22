package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// lc — нижний регистр той же длины в байтах: позиции совпадений годятся для исходной строки
func lc(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		l := unicode.ToLower(r)
		if utf8.RuneLen(l) != utf8.RuneLen(r) {
			l = r
		}
		b.WriteRune(l)
	}
	return b.String()
}

// re — регулярка с самым длинным совпадением
func re(s string) *regexp.Regexp {
	r := regexp.MustCompile(s)
	r.Longest()
	return r
}

type rule struct {
	re     *regexp.Regexp
	action string
}

func parseRules(text string) []rule {
	var rs []rule
	for _, l := range strings.Split(text, "\n") {
		k := strings.Index(l, " => ")
		if k < 0 {
			continue
		}
		rs = append(rs, rule{re(l[:k]), strings.TrimSpace(l[k+4:])})
	}
	return rs
}

var (
	rules     = parseRules(rulesText)
	fallback  = re(fallbackRe)
	endKinds  = re(dialogEnd)
	aliceLead = re(`^ *алиса[ ,.!]*`)
	noWords   = onlyWordsRe(dialogNo)
	yesWords  = onlyWordsRe(dialogYes)
)

const punct = " ,.:;!?-"

// ruleFor — действие и вопрос по первому совпавшему правилу
func ruleFor(phrase string) (action, q string, ok bool) {
	t := lc(phrase)
	for _, r := range rules {
		m := r.re.FindStringIndex(t)
		if m == nil {
			continue
		}
		q = strings.TrimLeft(phrase[m[1]:], punct)
		if q == "" && r.action != "@reset" {
			q = phrase[:m[0]]
			if a := aliceLead.FindStringIndex(lc(q)); a != nil {
				q = q[a[1]:]
			}
		}
		return r.action, strings.TrimRight(q, punct), true
	}
	return "", "", false
}

func matches(r *regexp.Regexp, s string) bool { return r.MatchString(lc(s)) }

// onlyWordsRe — фраза только из слов списка; «Алиса» впереди не мешает
func onlyWordsRe(list string) *regexp.Regexp {
	return re(`^(алиса[ ,.!?-]*)?(` + list + `)([ ,.!?-]+(` + list + `))*[ ,.!?-]*$`)
}

// ---- даты ----

var (
	months   = strings.Fields("января февраля марта апреля мая июня июля августа сентября октября ноября декабря")
	weekdays = strings.Fields("воскресенье понедельник вторник среда четверг пятница суббота") // time.Weekday
)

// day — «7 ноября 2026» со сдвигом offset дней
func day(now time.Time, offset int) string {
	t := now.Add(time.Duration(offset) * 24 * time.Hour)
	return fmt.Sprintf("%d %s %d", t.Day(), months[t.Month()-1], t.Year())
}

// today — дата и время для промпта; время — только с известным поясом (часы колонки в UTC)
func today(now time.Time, known bool) string {
	w := weekdays[now.Weekday()]
	if !known {
		return fmt.Sprintf("Сегодня %s, %s года.", w, day(now, 0))
	}
	_, off := now.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	o := fmt.Sprintf("UTC%s%d", sign, off/3600)
	if m := off % 3600 / 60; m != 0 {
		o += fmt.Sprintf(":%02d", m)
	}
	return fmt.Sprintf("Сегодня %s, %s года. Сейчас %s по местному времени (%s).", w, day(now, 0), now.Format("15:04"), o)
}

var dateWords = []struct {
	re     *regexp.Regexp
	offset int
}{
	// только целые слова; длинные раньше
	{dateWordRe("позавчера"), -2}, {dateWordRe("вчера"), -1},
	{dateWordRe("послезавтра"), 2}, {dateWordRe("завтра"), 1}, {dateWordRe("сегодня"), 0},
}

const wordEdge = `[ ,.:;!?"()-]`

func dateWordRe(w string) *regexp.Regexp { return regexp.MustCompile(wordEdge + w + wordEdge) }

// searchQuery — запрос для поиска: нижний регистр, «вчера», «завтра» и т. п. → дата
func searchQuery(s string, now time.Time) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		l = " " + lc(l) + " "
		for _, d := range dateWords {
			l = replaceWord(l, d.re, day(now, d.offset))
		}
		lines[i] = l[1 : len(l)-1]
	}
	return strings.Join(lines, "\n")
}

// replaceWord — слово между границами → d; правая граница может быть левой у следующего
func replaceWord(s string, r *regexp.Regexp, d string) string {
	var out strings.Builder
	for {
		m := r.FindStringIndex(s)
		if m == nil {
			break
		}
		_, w := utf8.DecodeLastRuneInString(s[:m[1]])
		out.WriteString(s[:m[0]+1]) // граница — один байт
		out.WriteString(d)
		s = s[m[1]-w:]
	}
	out.WriteString(s)
	return out.String()
}

// ---- строки для тела запроса ----

var jsonEscaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`)

// jsonEsc — строка для JSON; управляющие символы — пробелом
func jsonEsc(s string) string {
	return jsonEscaper.Replace(strings.Map(func(r rune) rune {
		if r < 0x20 {
			return ' '
		}
		return r
	}, s))
}

// jsonEscNL — то же, перевод строки — \n
func jsonEscNL(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = jsonEsc(l)
	}
	return strings.Join(lines, `\n`)
}
