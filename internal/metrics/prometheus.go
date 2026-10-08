package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Sample is one extra value for the Prometheus endpoint, such as a cache's
// hits.
type Sample struct {
	Name   string
	Help   string
	Type   string // counter or gauge
	Labels map[string]string
	Value  float64
}

// WritePrometheus writes everything counted since start in Prometheus's
// text format, followed by extra.
func (c *Collector) WritePrometheus(w io.Writer, extra []Sample) {
	c.mu.Lock()
	routes := make([]string, 0, len(c.routes))
	for r := range c.routes {
		routes = append(routes, r)
	}
	sort.Strings(routes)
	stats := make([]Stat, len(routes))
	for i, r := range routes {
		stats[i] = *c.routes[r]
	}
	queries, slow, g := c.queries, c.slowQ, c.gauges
	c.mu.Unlock()

	header := func(name, typ, help string) {
		fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", name, help, name, typ)
	}
	header("mangarr_http_request_duration_seconds", "histogram", "Time to answer a request, by route.")
	for i, r := range routes {
		writeHist(w, "mangarr_http_request_duration_seconds", `route="`+escape(r)+`"`, &stats[i])
	}
	for _, m := range []struct {
		name, help string
		v          func(*Stat) int64
	}{
		{"mangarr_http_server_errors_total", "Requests answered with a 5xx status, by route.", func(s *Stat) int64 { return s.Errors }},
		{"mangarr_http_not_modified_total", "Requests answered 304 (the client already had the response), by route.", func(s *Stat) int64 { return s.NotModified }},
		{"mangarr_http_response_bytes_total", "Bytes sent in response bodies, by route.", func(s *Stat) int64 { return s.Bytes }},
	} {
		header(m.name, "counter", m.help)
		for i, r := range routes {
			fmt.Fprintf(w, "%s{route=\"%s\"} %d\n", m.name, escape(r), m.v(&stats[i]))
		}
	}
	header("mangarr_db_query_duration_seconds", "histogram", "Time a database query took.")
	writeHist(w, "mangarr_db_query_duration_seconds", "", &queries)
	header("mangarr_db_slow_queries_total", "counter", "Database queries slower than the slow-query threshold.")
	fmt.Fprintf(w, "mangarr_db_slow_queries_total %d\n", slow)
	for _, s := range []Sample{
		{Name: "mangarr_go_heap_bytes", Help: "Heap memory in use.", Type: "gauge", Value: float64(g.HeapBytes)},
		{Name: "mangarr_go_goroutines", Help: "Goroutines running.", Type: "gauge", Value: float64(g.Goroutines)},
		{Name: "mangarr_db_connections_open", Help: "Open database connections.", Type: "gauge", Value: float64(g.DBOpen)},
		{Name: "mangarr_db_connections_in_use", Help: "Database connections in use.", Type: "gauge", Value: float64(g.DBInUse)},
		{Name: "mangarr_db_connection_waits_total", Help: "Times a query waited for a free connection.", Type: "counter", Value: float64(g.DBWaitCount)},
		{Name: "mangarr_download_jobs", Help: "Download jobs by state.", Type: "gauge", Labels: map[string]string{"state": "waiting"}, Value: float64(g.QueueWaiting)},
		{Name: "mangarr_download_jobs", Help: "Download jobs by state.", Type: "gauge", Labels: map[string]string{"state": "running"}, Value: float64(g.QueueRunning)},
		{Name: "mangarr_uptime_seconds", Help: "Seconds since the server started.", Type: "gauge", Value: time.Since(c.started).Seconds()},
	} {
		extra = append([]Sample{s}, extra...)
	}
	seen := map[string]bool{}
	sort.SliceStable(extra, func(i, j int) bool { return extra[i].Name < extra[j].Name })
	for _, s := range extra {
		if !seen[s.Name] {
			seen[s.Name] = true
			header(s.Name, s.Type, s.Help)
		}
		fmt.Fprintf(w, "%s%s %s\n", s.Name, labels(s.Labels), strconv.FormatFloat(s.Value, 'g', -1, 64))
	}
}

func writeHist(w io.Writer, name, label string, s *Stat) {
	sep := ""
	if label != "" {
		sep = ","
	}
	var cum int64
	for i, b := range bounds {
		cum += s.Hist[i]
		fmt.Fprintf(w, "%s_bucket{%s%sle=\"%s\"} %d\n", name, label, sep, strconv.FormatFloat(b.Seconds(), 'g', -1, 64), cum)
	}
	fmt.Fprintf(w, "%s_bucket{%s%sle=\"+Inf\"} %d\n", name, label, sep, s.Count)
	if label != "" {
		label = "{" + label + "}"
	}
	fmt.Fprintf(w, "%s_sum%s %s\n%s_count%s %d\n", name, label, strconv.FormatFloat(s.Sum.Seconds(), 'g', -1, 64), name, label, s.Count)
}

func labels(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + `="` + escape(m[k]) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

var escaper = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)

func escape(s string) string { return escaper.Replace(s) }
