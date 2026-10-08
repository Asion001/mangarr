package metrics

import (
	"runtime"
	rtmetrics "runtime/metrics"
	"sort"
	"time"
)

// Gauges are values sampled now and then rather than counted.
type Gauges struct {
	At          time.Time `json:"at"`
	HeapBytes   uint64    `json:"heapBytes"`
	Goroutines  int       `json:"goroutines"`
	GCPauseP99  float64   `json:"gcPauseP99Ms"`
	DBOpen      int       `json:"dbOpen"`
	DBInUse     int       `json:"dbInUse"`
	DBMaxOpen   int       `json:"dbMaxOpen"`
	DBWaitCount int64     `json:"dbWaitCount"`
	DBWaitMs    float64   `json:"dbWaitMs"`
	// QueueWaiting and QueueRunning are download jobs.
	QueueWaiting int `json:"queueWaiting"`
	QueueRunning int `json:"queueRunning"`
}

// SetGauges stores the latest sample.
func (c *Collector) SetGauges(g Gauges) {
	c.mu.Lock()
	c.gauges = g
	c.mu.Unlock()
}

// Gauges returns the latest sample.
func (c *Collector) Gauges() Gauges {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gauges
}

// RuntimeGauges fills in the Go runtime's part of g.
func RuntimeGauges(g *Gauges) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	g.HeapBytes = m.HeapAlloc
	g.Goroutines = runtime.NumGoroutine()
	s := []rtmetrics.Sample{{Name: "/sched/pauses/total/gc:seconds"}}
	rtmetrics.Read(s)
	if s[0].Value.Kind() == rtmetrics.KindFloat64Histogram {
		g.GCPauseP99 = histQuantile(s[0].Value.Float64Histogram(), 0.99) * 1000
	}
}

func histQuantile(h *rtmetrics.Float64Histogram, q float64) float64 {
	var total uint64
	for _, n := range h.Counts {
		total += n
	}
	if total == 0 {
		return 0
	}
	rank := uint64(q * float64(total))
	var seen uint64
	for i, n := range h.Counts {
		seen += n
		if seen >= rank && n > 0 {
			return h.Buckets[i+1]
		}
	}
	return h.Buckets[len(h.Buckets)-1]
}

// Point is one step of the response-time chart.
type Point struct {
	At          time.Time `json:"at"`
	Requests    int64     `json:"requests"`
	P50Ms       float64   `json:"p50Ms"`
	P95Ms       float64   `json:"p95Ms"`
	Errors      int64     `json:"errors"`
	NotModified int64     `json:"notModified"`
}

// Summary adds up a whole range.
type Summary struct {
	Requests      int64   `json:"requests"`
	PerMinute     float64 `json:"perMinute"`
	PeakPerMinute float64 `json:"peakPerMinute"`
	P95Ms         float64 `json:"p95Ms"`
	// PrevP95Ms is the same figure for the range before (0: no data).
	PrevP95Ms   float64 `json:"prevP95Ms"`
	CachedShare float64 `json:"cachedShare"`
	ErrorShare  float64 `json:"errorShare"`
	Errors      int64   `json:"errors"`
	Bytes       int64   `json:"bytes"`
}

// PhaseShare is how much of the handlers' time one phase took.
type PhaseShare struct {
	Name  string  `json:"name"`
	Ms    float64 `json:"ms"`
	Share float64 `json:"share"`
}

// Endpoint is one route over the range.
type Endpoint struct {
	Route       string  `json:"route"`
	Calls       int64   `json:"calls"`
	P50Ms       float64 `json:"p50Ms"`
	P95Ms       float64 `json:"p95Ms"`
	NotModified int64   `json:"notModified"`
	Errors      int64   `json:"errors"`
	Bytes       int64   `json:"bytes"`
	TotalMs     float64 `json:"totalMs"`
}

// QueryStats sums up the database queries of the range.
type QueryStats struct {
	Count int64   `json:"count"`
	P95Ms float64 `json:"p95Ms"`
	Slow  int64   `json:"slow"`
}

// Report is what the performance page shows for a range.
type Report struct {
	Range       string       `json:"range"`
	StepSeconds int          `json:"stepSeconds"`
	Started     time.Time    `json:"started"`
	Points      []Point      `json:"points"`
	Summary     Summary      `json:"summary"`
	Phases      []PhaseShare `json:"phases"`
	Endpoints   []Endpoint   `json:"endpoints"`
	Queries     QueryStats   `json:"queries"`
	Gauges      Gauges       `json:"gauges"`
}

// Ranges the report knows: the buckets it reads and how many make a point.
var Ranges = map[string]struct {
	hourly bool
	n      int // buckets in the range
	group  int // buckets per point
}{
	"1h":  {false, 60, 1},
	"24h": {false, 24 * 60, 30},
	"7d":  {true, 7 * 24, 3},
}

const topEndpoints = 12

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// window is the n buckets of r ending with the one holding now, oldest
// first; buckets nobody wrote come back empty.
func window(r *ring, now time.Time, n int, offset int) []bucket {
	end := now.Unix()/r.step*r.step - int64(offset)*r.step
	out := make([]bucket, n)
	for i := range out {
		at := end - int64(n-1-i)*r.step
		b := r.buckets[(at/r.step)%int64(len(r.buckets))]
		if b.at == at {
			out[i] = b
		} else {
			out[i] = bucket{at: at}
		}
	}
	return out
}

// Report sums up rng ("1h", "24h" or "7d"; anything else is "24h").
func (c *Collector) Report(rng string) Report {
	spec, ok := Ranges[rng]
	if !ok {
		rng, spec = "24h", Ranges["24h"]
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.minutes
	if spec.hourly {
		r = c.hours
	}
	bs := window(r, now, spec.n, 0)
	out := Report{Range: rng, StepSeconds: int(r.step) * spec.group, Started: c.started, Gauges: c.gauges,
		Points: []Point{}, Phases: []PhaseShare{}, Endpoints: []Endpoint{}}

	var total, queries Stat
	routes := map[string]*Stat{}
	phases := map[string]time.Duration{}
	var slow int64
	stepMinutes := float64(out.StepSeconds) / 60
	for i := 0; i < len(bs); i += spec.group {
		var p Stat
		for _, b := range bs[i:min(i+spec.group, len(bs))] {
			p.merge(&b.total)
			queries.merge(&b.queries)
			slow += b.slowQ
			for name, st := range b.routes {
				if routes[name] == nil {
					routes[name] = &Stat{}
				}
				routes[name].merge(st)
			}
			for name, d := range b.phases {
				phases[name] += d
			}
		}
		total.merge(&p)
		out.Points = append(out.Points, Point{At: time.Unix(bs[i].at, 0).UTC(), Requests: p.Count, P50Ms: ms(p.Quantile(0.5)),
			P95Ms: ms(p.Quantile(0.95)), Errors: p.Errors, NotModified: p.NotModified})
		if rate := float64(p.Count) / stepMinutes; rate > out.Summary.PeakPerMinute {
			out.Summary.PeakPerMinute = rate
		}
	}
	minutes := float64(spec.n) * float64(r.step) / 60
	if since := now.Sub(c.started).Minutes(); since < minutes {
		minutes = max(since, 1)
	}
	out.Summary.Requests, out.Summary.Errors, out.Summary.Bytes = total.Count, total.Errors, total.Bytes
	out.Summary.PerMinute = float64(total.Count) / minutes
	out.Summary.P95Ms = ms(total.Quantile(0.95))
	if total.Count > 0 {
		out.Summary.CachedShare = float64(total.NotModified) / float64(total.Count)
		out.Summary.ErrorShare = float64(total.Errors) / float64(total.Count)
	}
	// the range before, where the rings still hold it
	var prev Stat
	switch rng {
	case "1h":
		for _, b := range window(c.minutes, now, 60, 60) {
			prev.merge(&b.total)
		}
	case "24h":
		for _, b := range window(c.hours, now, 24, 24) {
			prev.merge(&b.total)
		}
	}
	out.Summary.PrevP95Ms = ms(prev.Quantile(0.95))

	for name, d := range phases {
		ps := PhaseShare{Name: name, Ms: ms(d)}
		if total.Sum > 0 {
			ps.Share = float64(d) / float64(total.Sum)
		}
		out.Phases = append(out.Phases, ps)
	}
	sort.Slice(out.Phases, func(i, j int) bool { return out.Phases[i].Ms > out.Phases[j].Ms })
	for name, st := range routes {
		out.Endpoints = append(out.Endpoints, Endpoint{Route: name, Calls: st.Count, P50Ms: ms(st.Quantile(0.5)), P95Ms: ms(st.Quantile(0.95)),
			NotModified: st.NotModified, Errors: st.Errors, Bytes: st.Bytes, TotalMs: ms(st.Sum)})
	}
	sort.Slice(out.Endpoints, func(i, j int) bool { return out.Endpoints[i].TotalMs > out.Endpoints[j].TotalMs })
	if len(out.Endpoints) > topEndpoints {
		out.Endpoints = out.Endpoints[:topEndpoints]
	}
	out.Queries = QueryStats{Count: queries.Count, P95Ms: ms(queries.Quantile(0.95)), Slow: slow}
	return out
}
