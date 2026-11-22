// jbl-assist — помощник Алисы на JBL Link Portable (прошивка 1.80.0.228): ответы Exa её голосом
// по фразе-триггеру и вместо её «не знаю», с уточнениями в одном разговоре.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev" // build.sh: -X main.version=…

const usage = `jbl-assist run              служба: слушать Алису и отвечать
jbl-assist text             события канала Алисы (@state N, @partial, фраза, @speech)
jbl-assist consume          то же, что run, но события — строками text со stdin
jbl-assist ask 'вопрос' […] ответы Exa текстом, один разговор
jbl-assist ask -warm …      то же, соединение заранее, как у службы (для замеров)
jbl-assist say 'текст'      сказать голосом Алисы
jbl-assist tell 'текст'     текст в Алису, как сказанный голосом
jbl-assist version

Окружение: JBL_ASSIST_DIR (` + defaultDir + `: exa.key, debug), JBL_ASSIST_RUN (` + defaultRun + `),
JBL_ASSIST_LOG (` + defaultLog + `), EXA_KEY_FILE (по умолчанию $JBL_ASSIST_DIR/exa.key).
`

type env struct {
	dir, run, log, keyFile, aliceLog string
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadEnv() env {
	e := env{
		dir:      getenv("JBL_ASSIST_DIR", defaultDir),
		run:      getenv("JBL_ASSIST_RUN", defaultRun),
		log:      getenv("JBL_ASSIST_LOG", defaultLog),
		aliceLog: getenv("JBL_ASSIST_ALICE_LOG", aliceLog),
	}
	e.keyFile = getenv("EXA_KEY_FILE", filepath.Join(e.dir, "exa.key"))
	return e
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	e := loadEnv()
	os.MkdirAll(e.run, 0700)
	hub := &iohub{hold: defaultTiming.IOHubHold}
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "run", "consume":
		serve(e, hub, cmd == "consume")
	case "text":
		w := bufio.NewWriter(os.Stdout)
		listenAlice(context.Background(), defaultTiming.Reconnect, func(ev event) {
			fmt.Fprintln(w, ev)
			w.Flush()
		})
	case "ask":
		if len(args) == 0 {
			fail("нужен вопрос")
		}
		os.Exit(askCLI(e, args))
	case "say", "tell":
		if len(args) != 1 || args[0] == "" {
			fail("нужен текст")
		}
		f := sayFrame
		if cmd == "tell" {
			f = vinsFrame
		}
		if err := hub.send(f(args[0])); err != nil {
			fail(err.Error())
		}
		hub.wg.Wait()
	case "version":
		fmt.Println("jbl-assist", version)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func fail(s string) {
	fmt.Fprintln(os.Stderr, "ERROR:", s)
	os.Exit(1)
}

// serve — служба: события Алисы (или строки со stdin) → hook
func serve(e env, hub *iohub, stdin bool) {
	pool := caPool()
	pl := newPlace(e.run, e.aliceLog)
	j := &journal{out: os.Stdout, path: e.log, debugFile: filepath.Join(e.dir, "debug"), place: pl}
	ex := newExa(pool, defaultTiming.WarmMax)
	dlg := &dialog{}
	a := &answerer{exa: ex, place: pl, dlg: dlg, keyFile: e.keyFile, j: j}
	h := &hook{
		t: defaultTiming, j: j, io: hubSpeaker{hub, j}, ask: a.ask, dlg: dlg, state: newStateBox(),
		hasExa: func() bool { return hasKey(e.keyFile) },
		warmUp: ex.warm.open, drop: ex.warm.drop,
	}
	h.start()
	j.logf("jbl-assist %s запущен", version)
	if !h.hasExa() {
		j.logf("llm: нет ключа %s — правила @llm и «не знаю» выключены", e.keyFile)
	} else {
		// регион — заранее; не вышло — спросит следующий вопрос
		go func() { pl.refresh(); pl.lookup() }()
		go ex.warm.warmUp()
	}
	events := make(chan event, 64)
	if stdin {
		go func() {
			sc := bufio.NewScanner(os.Stdin)
			for sc.Scan() {
				if ev, ok := parseLine(strings.TrimRight(sc.Text(), "\r")); ok {
					events <- ev
				}
			}
			close(events)
		}()
	} else {
		go listenAlice(context.Background(), defaultTiming.Reconnect, func(ev event) { events <- ev })
	}
	for ev := range events {
		h.handle(ev)
	}
	// stdin кончился — даём обработчику договорить
	if h.worker != nil {
		select {
		case <-h.worker.done:
		case <-time.After(3 * time.Minute):
		}
	}
	hub.wg.Wait()
}

// askCLI — вопросы в Exa без голоса, одним разговором
func askCLI(e env, qs []string) int {
	pool := caPool()
	pl := newPlace(e.run, e.aliceLog)
	j := &journal{out: os.Stderr, debugFile: filepath.Join(e.dir, "debug"), place: pl}
	dlg := &dialog{}
	a := &answerer{exa: newExa(pool, defaultTiming.WarmMax), place: pl, dlg: dlg, keyFile: e.keyFile, j: j}
	// регион — сразу: в фоне первый вопрос ушёл бы без него
	pl.refresh()
	if geo, _, region := pl.cached(); geo != "" && region == "" {
		pl.fetch(geoKey(geo))
	}
	warmFirst := qs[0] == "-warm"
	if warmFirst {
		qs = qs[1:]
	}
	for _, q := range qs {
		if len(qs) > 1 {
			fmt.Println(">", q)
		}
		if warmFirst {
			a.exa.warm.open()
			time.Sleep(3 * time.Second)
		}
		t := time.Now()
		ans, steps, r := a.ask(context.Background(), q)
		if warmFirst {
			steps = append(steps, "всего: "+secs(time.Since(t))+" с")
		}
		for _, s := range steps {
			fmt.Fprintln(os.Stderr, s)
		}
		switch r {
		case 2:
			fmt.Println("КОНЕЦ:", ans)
			return 0
		case 1:
			fmt.Println(ans)
			return 1
		}
		fmt.Println(ans)
		dlg.add(q, ans)
	}
	return 0
}
