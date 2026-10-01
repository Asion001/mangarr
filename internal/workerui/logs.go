package workerui

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// logKept is how many lines the page can show.
const logKept = 500

// Line is one log record as the page shows it.
type Line struct {
	Seq   int64     `json:"seq"`
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
	Attrs string    `json:"attrs,omitempty"`
}

// Logs keeps the last lines logged, numbered so the page asks only for what
// it hasn't seen.
type Logs struct {
	mu    sync.Mutex
	lines []Line
	seq   int64
}

func (l *Logs) add(r slog.Record, attrs string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	l.lines = append(l.lines, Line{Seq: l.seq, Time: r.Time, Level: r.Level.String(), Msg: r.Message, Attrs: attrs})
	if len(l.lines) > logKept {
		l.lines = append(l.lines[:0:0], l.lines[len(l.lines)-logKept:]...)
	}
}

// Since returns the lines after seq.
func (l *Logs) Since(seq int64) []Line {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []Line{}
	for _, ln := range l.lines {
		if ln.Seq > seq {
			out = append(out, ln)
		}
	}
	return out
}

// Handler logs to next (the console) and keeps a copy in l.
func (l *Logs) Handler(next slog.Handler) slog.Handler { return &teeHandler{logs: l, next: next} }

type teeHandler struct {
	logs  *Logs
	next  slog.Handler
	attrs []slog.Attr
	group string
}

func (h *teeHandler) Enabled(ctx context.Context, lv slog.Level) bool { return h.next.Enabled(ctx, lv) }

func (h *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	write := func(a slog.Attr) bool {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		fmt.Fprintf(&b, "%s=%v", key, a.Value.Resolve())
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)
	h.logs.add(r, b.String())
	return h.next.Handle(ctx, r)
}

func (h *teeHandler) WithAttrs(as []slog.Attr) slog.Handler {
	return &teeHandler{logs: h.logs, next: h.next.WithAttrs(as), attrs: append(h.attrs[:len(h.attrs):len(h.attrs)], as...), group: h.group}
}

func (h *teeHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" {
		g = h.group + "." + name
	}
	return &teeHandler{logs: h.logs, next: h.next.WithGroup(name), attrs: h.attrs, group: g}
}
