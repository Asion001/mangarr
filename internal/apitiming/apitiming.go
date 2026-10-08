// Package apitiming measures where a request's time went and reports it in
// the Server-Timing header, so the browser's network panel shows the phases
// of a slow response (and the log line can be found by its request id).
package apitiming

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

type ctxKey struct{}

// Timings collects the phases of one request.
type Timings struct {
	mu    sync.Mutex
	spans []span
}

type span struct {
	name string
	dur  time.Duration
}

// With attaches a fresh Timings to ctx.
func With(ctx context.Context) (context.Context, *Timings) {
	t := &Timings{}
	return context.WithValue(ctx, ctxKey{}, t), t
}

// From returns the request's Timings (nil when nothing is collecting).
func From(ctx context.Context) *Timings {
	t, _ := ctx.Value(ctxKey{}).(*Timings)
	return t
}

// Span times a phase until the returned function runs:
//
//	defer apitiming.Span(ctx, "source")()
//
// Spans of the same name add up, so a loop reports one total.
func Span(ctx context.Context, name string) func() {
	t := From(ctx)
	if t == nil {
		return func() {}
	}
	start := time.Now()
	return func() { t.Add(name, time.Since(start)) }
}

// Add records time spent in a phase.
func (t *Timings) Add(name string, d time.Duration) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.spans {
		if t.spans[i].name == name {
			t.spans[i].dur += d
			return
		}
	}
	if len(t.spans) < 16 { // a handler gone wild can't grow the header forever
		t.spans = append(t.spans, span{name, d})
	}
}

// Each calls fn with every phase recorded so far.
func (t *Timings) Each(fn func(name string, d time.Duration)) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, s := range t.spans {
		fn(s.name, s.dur)
	}
}

// Header renders the Server-Timing value, total first.
func (t *Timings) Header(total time.Duration) string {
	parts := []string{fmt.Sprintf("total;dur=%.1f", ms(total))}
	if t != nil {
		t.mu.Lock()
		for _, s := range t.spans {
			parts = append(parts, fmt.Sprintf("%s;dur=%.1f", s.name, ms(s.dur)))
		}
		t.mu.Unlock()
	}
	return strings.Join(parts, ", ")
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// writer sets Server-Timing just before the status line goes out, which is
// the last moment headers can still be written.
type writer struct {
	http.ResponseWriter
	start   time.Time
	t       *Timings
	status  int
	written bool
}

func (w *writer) WriteHeader(status int) {
	if !w.written {
		w.written, w.status = true, status
		w.Header().Set("Server-Timing", w.t.Header(time.Since(w.start)))
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *writer) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// Flush and Unwrap keep streaming responses (SSE, images) working.
func (w *writer) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Status is the status that was written (0 when the handler wrote nothing).
func (w *writer) Status() int { return w.status }

// Middleware measures every request and reports it in Server-Timing.
// Handlers add their own phases with Span.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, t := With(r.Context())
		ww := &writer{ResponseWriter: w, start: time.Now(), t: t}
		next.ServeHTTP(ww, r.WithContext(ctx))
		if !ww.written { // nothing was written: still report what it cost
			ww.Header().Set("Server-Timing", t.Header(time.Since(ww.start)))
		}
	})
}
