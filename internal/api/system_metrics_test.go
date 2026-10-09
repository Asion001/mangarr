package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/api"
)

// The performance page counts requests by route, and the same numbers go
// out in Prometheus's format; both are for admins only.
func TestSystemMetrics(t *testing.T) {
	srv, a := newServer(t, false)
	g, _ := a.Settings.General(context.Background())
	get := func(path string, key bool) (*http.Response, string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if key {
			req.Header.Set("Authorization", "Bearer "+g.APIKey)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	for i := 0; i < 3; i++ {
		get("/api/v1/series", true)
	}
	get("/api/v1/series/12345", true)
	get("/", false)
	get("/api/v1/worker/tasks", false) // workers' requests stay off the page

	if resp, _ := get("/api/v1/system/metrics", false); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("metrics without a login: %d", resp.StatusCode)
	}
	resp, body := get("/api/v1/system/metrics?range=1h", true)
	if resp.StatusCode != 200 {
		t.Fatalf("metrics: %d %s", resp.StatusCode, body)
	}
	var rep api.PerformanceReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		t.Fatal(err)
	}
	routes := map[string]int64{}
	for _, e := range rep.Endpoints {
		routes[e.Route] = e.Calls
	}
	for route := range routes {
		if strings.Contains(route, "/worker/") {
			t.Errorf("worker request on the page: %s", route)
		}
	}
	if routes["GET /api/v1/series"] != 3 || routes["GET /api/v1/series/{id}"] != 1 || routes["GET (web interface files)"] != 1 || routes["GET (API, refused or unknown)"] != 1 {
		t.Fatalf("routes: %v", routes)
	}
	if rep.Range != "1h" || len(rep.Points) != 60 || len(rep.Caches) != 4 || rep.GoVersion == "" || rep.Profiling {
		t.Fatalf("report: %+v", rep)
	}

	resp, body = get("/api/v1/system/metrics/prometheus", true)
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain") ||
		!strings.Contains(body, `mangarr_http_request_duration_seconds_count{route="GET /api/v1/series"} 3`) {
		t.Fatalf("prometheus: %d %s", resp.StatusCode, body)
	}
	if resp, _ := get("/api/v1/system/pprof/", true); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("the profiler is off unless MANGARR_PPROF is set: %d", resp.StatusCode)
	}
}
