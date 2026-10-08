package metrics

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestQuantile(t *testing.T) {
	var s Stat
	for i := 0; i < 90; i++ {
		s.add(3*time.Millisecond, 200, 0) // the 2–5 ms bin
	}
	for i := 0; i < 10; i++ {
		s.add(300*time.Millisecond, 200, 0) // the 200–500 ms bin
	}
	if p50 := s.Quantile(0.5); p50 < 2*time.Millisecond || p50 > 5*time.Millisecond {
		t.Errorf("p50 = %v, want within 2–5 ms", p50)
	}
	if p95 := s.Quantile(0.95); p95 < 200*time.Millisecond || p95 > 500*time.Millisecond {
		t.Errorf("p95 = %v, want within 200–500 ms", p95)
	}
	var empty Stat
	if empty.Quantile(0.95) != 0 {
		t.Error("no requests, no latency")
	}
}

func TestReport(t *testing.T) {
	c := New()
	now := time.Date(2026, 10, 8, 12, 0, 30, 0, time.UTC)
	c.now = func() time.Time { return now }
	c.started = now.Add(-48 * time.Hour)
	for i := 0; i < 10; i++ {
		c.Request("GET /api/v1/series", 200, 40*time.Millisecond, 1000, []Phase{{"db", 10 * time.Millisecond}})
	}
	c.Request("GET /api/v1/read/chapters/{id}/pages/{n}", 304, time.Millisecond, 0, nil)
	c.Request("GET /api/v1/discover", 502, 2*time.Second, 50, []Phase{{"source", 1900 * time.Millisecond}})
	c.Query(time.Millisecond)
	c.Query(time.Second)

	// an hour later the minute buckets of the last hour are empty, the day still has them
	r := c.Report("1h")
	if r.Summary.Requests != 12 || len(r.Points) != 60 || r.StepSeconds != 60 {
		t.Fatalf("1h: %+v", r.Summary)
	}
	if r.Summary.PeakPerMinute != 12 {
		t.Fatalf("peak: %v", r.Summary.PeakPerMinute)
	}
	if r.Summary.Errors != 1 || r.Summary.CachedShare != 1.0/12 || r.Queries.Count != 2 || r.Queries.Slow != 1 {
		t.Fatalf("1h summary: %+v queries %+v", r.Summary, r.Queries)
	}
	if r.Endpoints[0].Route != "GET /api/v1/discover" || r.Endpoints[1].Calls != 10 {
		t.Fatalf("endpoints by total time: %+v", r.Endpoints)
	}
	if r.Phases[0].Name != "source" || r.Phases[0].Share <= 0.5 {
		t.Fatalf("phases: %+v", r.Phases)
	}
	now = now.Add(2 * time.Hour)
	if r := c.Report("1h"); r.Summary.Requests != 0 {
		t.Fatalf("1h two hours later: %+v", r.Summary)
	}
	if r := c.Report("24h"); r.Summary.Requests != 12 || len(r.Points) != 48 || r.StepSeconds != 1800 {
		t.Fatalf("24h: %+v (%d points)", r.Summary, len(r.Points))
	}
	if r := c.Report("7d"); r.Summary.Requests != 12 || len(r.Points) != 56 {
		t.Fatalf("7d: %+v", r.Summary)
	}
	now = now.Add(8 * 24 * time.Hour)
	if r := c.Report("7d"); r.Summary.Requests != 0 {
		t.Fatalf("a week on, the old hours are gone: %+v", r.Summary)
	}
	if r := c.Report("bogus"); r.Range != "24h" {
		t.Fatalf("unknown range: %q", r.Range)
	}
}

func TestWritePrometheus(t *testing.T) {
	c := New()
	c.Request(`GET /api/v1/series/{id}`, 200, 30*time.Millisecond, 10, nil)
	c.Request(`GET /api/v1/series/{id}`, 500, 3*time.Second, 10, nil)
	c.Query(2 * time.Millisecond)
	var buf bytes.Buffer
	c.WritePrometheus(&buf, []Sample{{Name: "mangarr_cache_lookups_total", Help: "h", Type: "counter", Labels: map[string]string{"cache": `a"b`, "result": "hit"}, Value: 3}})
	out := buf.String()
	for _, want := range []string{
		`mangarr_http_request_duration_seconds_bucket{route="GET /api/v1/series/{id}",le="0.05"} 1`,
		`mangarr_http_request_duration_seconds_bucket{route="GET /api/v1/series/{id}",le="+Inf"} 2`,
		`mangarr_http_request_duration_seconds_count{route="GET /api/v1/series/{id}"} 2`,
		`mangarr_http_server_errors_total{route="GET /api/v1/series/{id}"} 1`,
		`mangarr_db_query_duration_seconds_count 1`,
		`mangarr_cache_lookups_total{cache="a\"b",result="hit"} 3`,
		"# TYPE mangarr_go_goroutines gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "# TYPE mangarr_download_jobs gauge") != 1 {
		t.Error("a metric with several label sets gets one TYPE line")
	}
}
