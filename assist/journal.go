package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// journal — строки службы в out и в файл со временем по поясу Алисы (journald на колонке ничего
// не хранит); подробности (dlog) — только в файл и с флагом debug.
type journal struct {
	out       io.Writer
	path      string // пусто — без файла
	debugFile string
	place     *place

	mu        sync.Mutex
	tzChecked time.Time
}

func (j *journal) logf(format string, a ...any) { j.write(true, fmt.Sprintf(format, a...)) }

// dlog — подробная строка, до 3000 байт
func (j *journal) dlog(format string, a ...any) {
	if !j.debug() {
		return
	}
	s := fmt.Sprintf(format, a...)
	if len(s) > 3000 {
		s = s[:3000]
	}
	j.write(false, "dbg: "+s)
}

func (j *journal) debug() bool {
	_, err := os.Stat(j.debugFile)
	return err == nil
}

func (j *journal) write(toOut bool, line string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	// без файла — всё в out
	if (toOut || j.path == "") && j.out != nil {
		fmt.Fprintln(j.out, line)
	}
	if j.path == "" {
		return
	}
	// пояса в журнале Алисы может ещё не быть
	if !j.place.tzKnown() && time.Since(j.tzChecked) > 10*time.Second {
		j.tzChecked = time.Now()
		j.place.refresh()
	}
	if st, err := os.Stat(j.path); err == nil && st.Size() >= logMax {
		os.Rename(j.path, j.path+".1")
	}
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	now, _ := j.place.now()
	fmt.Fprintf(f, "%s %s\n", now.Format("02.01 15:04:05"), line)
	f.Close()
}
