package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/pprof"
	"runtime"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/imagedeliver"
	"github.com/Asion001/mangarr/internal/metrics"
)

func init() { register((*Server).registerMetrics) }

// CacheStat is how often one cache had what was asked for, since start.
type CacheStat struct {
	Name   string `json:"name"`
	Hits   int64  `json:"hits"`
	Misses int64  `json:"misses"`
}

// PerformanceReport is the performance page: requests and queries over a
// range, plus caches and the server process now.
type PerformanceReport struct {
	metrics.Report
	Caches        []CacheStat `json:"caches"`
	UptimeSeconds int64       `json:"uptimeSeconds"`
	GoVersion     string      `json:"goVersion"`
	// Profiling: Go's profiler is on (MANGARR_PPROF) at api/v1/system/pprof/.
	Profiling bool `json:"profiling"`
}

const pprofPath = "/api/v1/system/pprof/"

func (s *Server) cacheStats() []CacheStat {
	disk := s.app.ImageCache.HitCounts()
	images := CacheStat{Name: "Thumbnails and covers"}
	for _, b := range []string{"thumbs", "covers", "assets"} {
		images.Hits += disk[b].Hits
		images.Misses += disk[b].Misses
	}
	variants := disk[imagedeliver.Bucket]
	sh, sm := s.app.SourceCache.HitCounts()
	lh, lm := cbz.ListCacheCounts()
	return []CacheStat{images, {Name: "Resized page copies", Hits: variants.Hits, Misses: variants.Misses},
		{Name: "Source responses", Hits: sh, Misses: sm}, {Name: "Archive page lists", Hits: lh, Misses: lm}}
}

func (s *Server) registerMetrics() {
	tags := []string{"System"}
	huma.Register(s.api, huma.Operation{OperationID: "system-metrics", Method: http.MethodGet, Path: "/api/v1/system/metrics", Tags: tags,
		Summary: "How fast the server answers and where the time goes, over the last hour, day or week"},
		func(ctx context.Context, in *struct {
			Range string `query:"range" enum:"1h,24h,7d" default:"24h"`
		}) (*struct{ Body PerformanceReport }, error) {
			return &struct{ Body PerformanceReport }{PerformanceReport{Report: s.app.Metrics.Report(in.Range), Caches: s.cacheStats(),
				UptimeSeconds: int64(time.Since(s.app.StartedAt).Seconds()), GoVersion: runtime.Version(), Profiling: s.app.Cfg.Pprof}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "system-metrics-prometheus", Method: http.MethodGet, Path: "/api/v1/system/metrics/prometheus", Tags: tags,
		Summary: "The same numbers for Prometheus (scrape with the API key as a bearer token)"},
		func(ctx context.Context, _ *struct{}) (*struct {
			ContentType string `header:"Content-Type"`
			Body        []byte
		}, error) {
			var extra []metrics.Sample
			for _, c := range s.cacheStats() {
				for result, n := range map[string]int64{"hit": c.Hits, "miss": c.Misses} {
					extra = append(extra, metrics.Sample{Name: "mangarr_cache_lookups_total", Help: "Cache lookups by cache and result.", Type: "counter",
						Labels: map[string]string{"cache": c.Name, "result": result}, Value: float64(n)})
				}
			}
			var buf bytes.Buffer
			s.app.Metrics.WritePrometheus(&buf, extra)
			return &struct {
				ContentType string `header:"Content-Type"`
				Body        []byte
			}{"text/plain; version=0.0.4; charset=utf-8", buf.Bytes()}, nil
		})
}

// pprofHandler serves Go's profiler to admins only, under pprofPath.
func (s *Server) pprofHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !access.From(r.Context()).IsAdmin() {
			http.Error(w, "profiling is for admins", http.StatusForbidden)
			return
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/debug/pprof/" + strings.TrimPrefix(r.URL.Path, pprofPath)
		mux.ServeHTTP(w, r2)
	})
}
