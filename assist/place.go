package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // своей базы поясов на колонке нет
)

// place — пояс и координаты из журнала Алисы (ротируется за ~25 мин, поэтому кеши в $RUN),
// регион по координатам — Nominatim.
type place struct {
	run, log string
	client   func() *http.Client // Nominatim, при первом запросе

	mu      sync.Mutex
	loc     *time.Location
	known   bool   // пояс найден
	geo     string // «широта долгота», 3 знака
	cc      string // страна региона, «ID»
	region  string // «bali»
	fetchAt time.Time
}

func newPlace(run, log string) *place {
	return &place{run: run, log: log, loc: time.UTC, client: sync.OnceValue(func() *http.Client {
		var d net.Dialer
		return &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig(systemPool()), DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return d.DialContext(ctx, "tcp4", addr) }}}
	})}
}

var (
	tzRe  = regexp.MustCompile(`timezone_: ([A-Za-z0-9_/+-]+)`)
	locRe = regexp.MustCompile(`"location":\{"accuracy":[0-9.]*,"lat":([-0-9.]+),"lon":([-0-9.]+)`)
)

// refresh — пояс и координаты из журнала, иначе из кешей
func (p *place) refresh() {
	var buf []byte
	for _, f := range []string{p.log + ".1", p.log} {
		if b, err := os.ReadFile(f); err == nil {
			buf = append(buf, b...)
		}
	}
	zone := ""
	if m := tzRe.FindAllSubmatch(buf, -1); len(m) > 0 {
		zone = string(m[len(m)-1][1])
	}
	geo := ""
	if m := locRe.FindAllSubmatch(buf, -1); len(m) > 0 {
		la, e1 := strconv.ParseFloat(string(m[len(m)-1][1]), 64)
		lo, e2 := strconv.ParseFloat(string(m[len(m)-1][2]), 64)
		if e1 == nil && e2 == nil {
			geo = fmt.Sprintf("%.3f %.3f", la, lo)
		}
	}
	geo = p.cache("loc", geo)
	zone = p.cache("tz", zone)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.geo = geo
	if zone == "" {
		return
	}
	if l, err := time.LoadLocation(zone); err == nil {
		p.loc, p.known = l, true
	}
}

// cache — новое значение в $RUN/name; пустое — значение из кеша
func (p *place) cache(name, v string) string {
	f := filepath.Join(p.run, name)
	old, _ := os.ReadFile(f)
	o := strings.TrimSpace(string(old))
	if v == "" {
		return o
	}
	if v != o {
		writeAtomic(f, v+"\n")
	}
	return v
}

// writeAtomic — запись через временный файл и rename: refresh идёт из нескольких горутин,
// и читатель не должен увидеть файл, обрезанный os.WriteFile до записи
func writeAtomic(f, s string) {
	t, err := os.CreateTemp(filepath.Dir(f), "."+filepath.Base(f)+".*")
	if err != nil {
		return
	}
	_, err = t.WriteString(s)
	if e := t.Close(); err == nil {
		err = e
	}
	if err == nil {
		err = os.Rename(t.Name(), f)
	}
	if err != nil {
		os.Remove(t.Name())
	}
}

func (p *place) now() (time.Time, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Now().In(p.loc), p.known
}

func (p *place) tzKnown() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.known
}

// geoKey — координаты с точностью ~1 км
func geoKey(geo string) string {
	f := strings.Fields(geo)
	if len(f) != 2 {
		return ""
	}
	la, _ := strconv.ParseFloat(f[0], 64)
	lo, _ := strconv.ParseFloat(f[1], 64)
	return fmt.Sprintf("%.2f %.2f", la, lo)
}

// lookup — координаты, страна и регион; региона ещё нет — запрос в фоне, не чаще geoRetry
func (p *place) lookup() (geo, cc, region string) {
	geo, cc, region = p.cached()
	k := geoKey(geo)
	if k == "" || region != "" {
		return geo, cc, region
	}
	p.mu.Lock()
	if time.Since(p.fetchAt) < geoRetry {
		p.mu.Unlock()
		return geo, "", ""
	}
	p.fetchAt = time.Now()
	p.mu.Unlock()
	go p.fetch(k)
	return geo, "", ""
}

// cached — то же без запроса
func (p *place) cached() (geo, cc, region string) {
	p.mu.Lock()
	geo = p.geo
	p.mu.Unlock()
	b, _ := os.ReadFile(filepath.Join(p.run, "place"))
	if f := strings.Fields(string(b)); len(f) >= 4 && geoKey(geo) != "" && f[0]+" "+f[1] == geoKey(geo) {
		return geo, f[2], strings.Join(f[3:], " ")
	}
	return geo, "", ""
}

// fetch — регион (zoom=5 — провинция) → $RUN/place «широта долгота CC регион»
func (p *place) fetch(k string) {
	f := strings.Fields(k)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	url := fmt.Sprintf("https://%s/reverse?format=jsonv2&lat=%s&lon=%s&zoom=5&accept-language=en", geoHost, f[0], f[1])
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "jbl-assist/1.0")
	resp, err := p.client().Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return
	}
	var v struct {
		Name    string `json:"name"`
		Address struct {
			CountryCode string `json:"country_code"`
		} `json:"address"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&v) != nil {
		return
	}
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r == ' ' || r == '-' {
			return r
		}
		return -1
	}, strings.ToLower(v.Name))
	cc := strings.ToUpper(v.Address.CountryCode)
	if name == "" || len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return
	}
	writeAtomic(filepath.Join(p.run, "place"), k+" "+cc+" "+name+"\n")
}

// geoJSON — поле userLocation; без координат — пусто
func geoJSON(geo, cc string) string {
	f := strings.Fields(geo)
	if len(f) != 2 {
		return ""
	}
	c := ""
	if cc != "" {
		c = `"country":"` + cc + `",`
	}
	return fmt.Sprintf(`,"userLocation":{%s"latitude":%s,"longitude":%s}`, c, f[0], f[1])
}
