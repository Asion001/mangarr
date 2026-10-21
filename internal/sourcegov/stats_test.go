package sourcegov

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

func TestPercentile(t *testing.T) {
	var l Latencies
	if got := l.Percentile(0.5); got != 0 {
		t.Fatalf("empty: %d", got)
	}
	l[2] = 10 // 500..1000 ms
	if got := l.Percentile(0.5); got != 750 {
		t.Fatalf("median inside one bucket: %d, want 750", got)
	}
	l = Latencies{}
	l[len(l)-1] = 3 // slower than the last bound
	if got := l.Percentile(0.5); got != LatencyBounds[len(LatencyBounds)-1] {
		t.Fatalf("slowest bucket: %d", got)
	}
	l = Latencies{4, 0, 0, 0, 0, 0, 0, 1}
	if got := l.Percentile(0.5); got > 250 {
		t.Fatalf("median of mostly fast requests: %d", got)
	}
}

// TestStatsCountRequests: every governed request is counted in its hour,
// failures keep what failed, throttling counts as both, and a fallback is
// counted on the catalog that was given up.
func TestStatsCountRequests(t *testing.T) {
	g, clk := newTestGov(model.ThrottleConfig{Preset: "fast"}, nil)
	ctx := context.Background()
	_ = g.DoOp(ctx, k, "Search “a”", func(context.Context) error {
		clk.mu.Lock()
		clk.t = clk.t.Add(1200 * time.Millisecond)
		clk.mu.Unlock()
		return nil
	})
	_ = g.DoOp(ctx, k, "Chapter list of X", func(context.Context) error { return errors.New("http error 500") })
	_ = g.DoOp(ctx, k, "Page list", func(context.Context) error { return errors.New("HTTP error 429: Too Many Requests") })
	g.Fallback(k)
	got := g.Stats().Drain()
	if len(got) != 1 {
		t.Fatalf("tallies %+v", got)
	}
	for hk, tl := range got {
		if !hk.Hour.Equal(time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)) {
			t.Fatalf("hour %v", hk.Hour)
		}
		if tl.Requests != 3 || tl.Failures != 2 || tl.Throttled != 1 || tl.Fallbacks != 1 || tl.TotalMs != 1200 || tl.Latency[3] != 1 {
			t.Fatalf("tally %+v", tl)
		}
	}
	if again := g.Stats().Drain(); len(again) != 0 {
		t.Fatalf("drained twice: %+v", again)
	}
	f := g.Stats().Failures(k)
	if len(f) != 2 || f[0].What == "" || f[1].Error != "http error 500" && f[0].Error != "http error 500" {
		t.Fatalf("failures %+v", f)
	}
	// a request cancelled by the caller says nothing about the site
	cctx, cancel := context.WithCancel(ctx)
	_ = g.DoOp(cctx, k, "x", func(context.Context) error { cancel(); return context.Canceled })
	if left := g.Stats().Drain(); len(left) != 0 {
		t.Fatalf("cancelled request counted: %+v", left)
	}
}

func TestStatsKeepLatestFailures(t *testing.T) {
	g, _ := newTestGov(model.ThrottleConfig{Preset: "fast"}, nil)
	for range recentFailures + 5 {
		_ = g.DoOp(context.Background(), k, "x", func(context.Context) error { return errors.New("boom") })
	}
	if n := len(g.Stats().Failures(k)); n != recentFailures {
		t.Fatalf("kept %d failures", n)
	}
}
