package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ---- TLS ----

// caPool — корни для Exa: файл Алисы (72 мс; системные — 450 мс), без него — системные
func caPool() *x509.CertPool {
	if b, err := os.ReadFile(caFile); err == nil {
		if p := x509.NewCertPool(); p.AppendCertsFromPEM(b) {
			return p
		}
	}
	return systemPool()
}

// systemPool — системные корни и файл Алисы: Nominatim одним файлом Алисы не проверяется
var systemPool = sync.OnceValue(func() *x509.CertPool {
	p, err := x509.SystemCertPool()
	if err != nil || p == nil {
		p = x509.NewCertPool()
	}
	if b, err := os.ReadFile(caFile); err == nil {
		p.AppendCertsFromPEM(b)
	}
	return p
})

func tlsConfig(pool *x509.CertPool) *tls.Config {
	return &tls.Config{RootCAs: pool, NextProtos: []string{"http/1.1"}}
}

func dialTLS(ctx context.Context, pool *x509.CertPool, host string) (net.Conn, error) {
	c := tlsConfig(pool)
	c.ServerName = host
	d := tls.Dialer{Config: c}
	// tcp4: IPv6 на колонке нет, а потерянный ответ на AAAA стоит 5 с таймаута DNS
	return d.DialContext(ctx, "tcp4", host+":443")
}

// warm — соединение, открытое заранее, пока человек договаривает или Алиса думает; его берёт
// следующий запрос.
type warm struct {
	host   string
	pool   *x509.CertPool
	maxAge time.Duration

	mu   sync.Mutex
	slot *pending
}

type pending struct {
	at     time.Time
	cancel context.CancelFunc
	done   chan struct{}
	conn   net.Conn
	err    error
}

func (p *pending) discard() {
	p.cancel()
	go func() {
		<-p.done
		if p.conn != nil {
			p.conn.Close()
		}
	}()
}

// open — новое соединение вместо прежнего
func (w *warm) open() {
	ctx, cancel := context.WithCancel(context.Background())
	p := &pending{at: time.Now(), cancel: cancel, done: make(chan struct{})}
	w.mu.Lock()
	if w.slot != nil {
		w.slot.discard()
	}
	w.slot = p
	w.mu.Unlock()
	go func() {
		p.conn, p.err = dialTLS(ctx, w.pool, w.host)
		close(p.done)
	}()
}

// warmUp — пробное рукопожатие: первое после запуска долгое (до 1,5 с)
func (w *warm) warmUp() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if c, err := dialTLS(ctx, w.pool, w.host); err == nil {
		c.Close()
	}
}

// drop — закрыть неиспользованное
func (w *warm) drop() {
	w.mu.Lock()
	if w.slot != nil {
		w.slot.discard()
		w.slot = nil
	}
	w.mu.Unlock()
}

// dial — для http.Transport: готовое соединение, если свежее и живое, иначе новое
func (w *warm) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	w.mu.Lock()
	p := w.slot
	w.slot = nil
	w.mu.Unlock()
	if p != nil && time.Since(p.at) <= w.maxAge {
		select {
		case <-p.done:
			if p.err == nil && alive(p.conn) {
				return p.conn, nil
			}
			p.discard()
		case <-ctx.Done():
			p.discard()
			return nil, ctx.Err()
		}
	} else if p != nil {
		p.discard()
	}
	return dialTLS(ctx, w.pool, w.host)
}

// alive — сервер не закрыл соединение, пока оно ждало
func alive(c net.Conn) bool {
	c.SetReadDeadline(time.Now().Add(time.Millisecond))
	var b [1]byte
	_, err := c.Read(b[:])
	c.SetReadDeadline(time.Time{})
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// ---- Exa /answer ----

type exa struct {
	warm *warm
	http *http.Client
}

func newExa(pool *x509.CertPool, maxAge time.Duration) *exa {
	w := &warm{host: answerHost, pool: pool, maxAge: maxAge}
	return &exa{warm: w, http: &http.Client{Transport: &http.Transport{
		DialTLSContext:    w.dial,
		DisableKeepAlives: true, // одно соединение — один запрос
	}}}
}

// post — запрос в /answer: код и тело ответа
func (e *exa) post(ctx context.Context, key, body string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", "https://"+answerHost+"/answer", strings.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("x-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	req.Header["User-Agent"] = []string{""} // без User-Agent
	resp, err := e.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, b, err
}

// answerBody — первый вопрос
func answerBody(q, sys, geo string) string {
	return fmt.Sprintf(`{"query":"%s","model":"%s","systemPrompt":"%s","outputSchema":%s%s}`,
		jsonEsc(q), answerModel, jsonEsc(sys), answerSchema, geo)
}

// dialogBody — уточнение; q — по dialogQuery
func dialogBody(q, date, geo string) string {
	sys := jsonEsc(answerPrompt+" "+date) + `\n` + jsonEsc(answerDialog) + `\n` + jsonEscNL(dialogPrompt)
	return fmt.Sprintf(`{"query":"%s","model":"%s","systemPrompt":"%s","outputSchema":%s%s}`,
		jsonEscNL(q), answerModel, sys, dialogSchema, geo)
}

var (
	footnoteRe = regexp.MustCompile(` *\[[0-9]+([,-] ?[0-9]+)*\]`)
	spacesRe   = regexp.MustCompile(`[ \t]+`)
	answerMax  = 1500 // байт; длиннее — до последнего предложения
)

// speech — текст для речи: управляющие символы — пробелом, эмодзи — прочь
func speech(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r < 0x20:
			return ' '
		case r > 0xffff || r == utf8.RuneError:
			return -1
		}
		return r
	}, s)
}

func squash(s string) string { return strings.TrimSpace(spacesRe.ReplaceAllString(s, " ")) }

// parseAnswer — ответ Exa без сносок, до answerMax; dialog — впереди вид реплики и табуляция.
// Ошибка — «ERR код сообщение».
func parseAnswer(code int, body []byte, dialog bool) (string, error) {
	var v struct {
		Answer  json.RawMessage `json:"answer"`
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	json.Unmarshal(body, &v)
	if code != 200 {
		msg := v.Message
		var s string
		var o struct{ Message string }
		if json.Unmarshal(v.Error, &s) == nil && s != "" {
			msg = s
		} else if json.Unmarshal(v.Error, &o) == nil && o.Message != "" {
			msg = o.Message
		}
		return "", fmt.Errorf("ERR %d %s", code, speech(msg))
	}
	var a struct{ Kind, Response string }
	json.Unmarshal(v.Answer, &a)
	// знаки Алиса читает сама — убираем только сноски [1], [1,6], [1-3]
	ans := squash(footnoteRe.ReplaceAllString(speech(a.Response), ""))
	if ans == "" {
		return "", errors.New("ERR 200 пустой ответ")
	}
	if len(ans) > answerMax {
		j := strings.LastIndexAny(ans[:answerMax], ".!?")
		if j >= 500 {
			ans = ans[:j+1]
		} else {
			cut := ans[:answerMax]
			for !utf8.ValidString(cut) {
				cut = cut[:len(cut)-1]
			}
			if k := strings.LastIndexByte(cut, ' '); k > 0 {
				cut = cut[:k]
			}
			ans = cut
		}
	}
	// оборван на полуслове — до последнего предложения
	if !strings.HasSuffix(ans, "…") && !strings.HasSuffix(ans, "»") && strings.IndexAny(ans[len(ans)-1:], `.!?)"`) < 0 {
		if j := strings.LastIndexAny(ans, ".!?"); j >= 0 {
			ans = ans[:j+1]
		}
	}
	if dialog {
		ans = squash(speech(a.Kind)) + "\t" + ans
	}
	return ans, nil
}
