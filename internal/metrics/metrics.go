// Package metrics keeps the server's performance numbers in memory: how
// many requests it answered and how fast, where the time went, how the
// database and caches are doing, and a few runtime gauges. Requests are kept
// per minute for the last day and per hour for the last week, which is a
// few megabytes at most; nothing survives a restart.
package metrics

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/uptrace/bun"
)

// bounds are the latency histogram's upper bounds; the last bin is
// everything slower.
var bounds = []time.Duration{
	time.Millisecond, 2 * time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond,
	50 * time.Millisecond, 100 * time.Millisecond, 200 * time.Millisecond, 500 * time.Millisecond,
	time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second,
}

const nBins = 15 // len(bounds) + 1

// Stat is what a set of requests (or queries) added up to.
type Stat struct {
	Count       int64
	Errors      int64 // 5xx
	NotModified int64 // 304
	Bytes       int64
	Sum         time.Duration
	Hist        [nBins]int64
}

func (s *Stat) add(d time.Duration, status int, bytes int64) {
	s.Count++
	s.Sum += d
	s.Bytes += bytes
	switch {
	case status >= 500:
		s.Errors++
	case status == 304:
		s.NotModified++
	}
	i := sort.Search(len(bounds), func(i int) bool { return d <= bounds[i] })
	s.Hist[i]++
}

func (s *Stat) merge(o *Stat) {
	s.Count += o.Count
	s.Errors += o.Errors
	s.NotModified += o.NotModified
	s.Bytes += o.Bytes
	s.Sum += o.Sum
	for i := range s.Hist {
		s.Hist[i] += o.Hist[i]
	}
}

// Quantile estimates the q-th latency (0 < q < 1) from the histogram,
// interpolating inside the bin it falls in.
func (s *Stat) Quantile(q float64) time.Duration {
	if s.Count == 0 {
		return 0
	}
	rank := q * float64(s.Count)
	var seen float64
	for i, n := range s.Hist {
		if n == 0 {
			continue
		}
		if seen+float64(n) >= rank {
			lo := time.Duration(0)
			if i > 0 {
				lo = bounds[i-1]
			}
			hi := lo * 2
			if i < len(bounds) {
				hi = bounds[i]
			}
			frac := (rank - seen) / float64(n)
			return lo + time.Duration(frac*float64(hi-lo))
		}
		seen += float64(n)
	}
	return bounds[len(bounds)-1]
}

// bucket is one minute (or hour) of requests.
type bucket struct {
	at      int64 // start, in unix seconds
	total   Stat
	routes  map[string]*Stat
	phases  map[string]time.Duration
	queries Stat
	slowQ   int64
}

func (b *bucket) reset(at int64) {
	*b = bucket{at: at, routes: map[string]*Stat{}, phases: map[string]time.Duration{}}
}

type ring struct {
	step    int64 // seconds
	buckets []bucket
}

func newRing(step time.Duration, n int) *ring {
	return &ring{step: int64(step / time.Second), buckets: make([]bucket, n)}
}

// at is the bucket for time t, cleared when it last held an older period.
func (r *ring) at(t time.Time) *bucket {
	start := t.Unix() / r.step * r.step
	b := &r.buckets[(start/r.step)%int64(len(r.buckets))]
	if b.at != start {
		b.reset(start)
	}
	return b
}

// Phase is time a request spent in one named part (from Server-Timing).
type Phase struct {
	Name string
	Dur  time.Duration
}

// Collector gathers the numbers. Its zero value is not usable: use New.
type Collector struct {
	// SlowQuery is when a database query counts as slow.
	SlowQuery time.Duration

	mu      sync.Mutex
	started time.Time
	minutes *ring
	hours   *ring
	// totals since start, for the Prometheus endpoint
	routes  map[string]*Stat
	queries Stat
	slowQ   int64
	gauges  Gauges
	now     func() time.Time
}

// New returns an empty collector.
func New() *Collector {
	return &Collector{SlowQuery: 250 * time.Millisecond, started: time.Now(), minutes: newRing(time.Minute, 24*60),
		hours: newRing(time.Hour, 7*24), routes: map[string]*Stat{}, now: time.Now}
}

// Started is when the collector began counting.
func (c *Collector) Started() time.Time { return c.started }

// Request records one answered request under its route ("GET /api/v1/series").
func (c *Collector) Request(route string, status int, d time.Duration, bytes int64, phases []Phase) {
	if c == nil {
		return
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range []*bucket{c.minutes.at(now), c.hours.at(now)} {
		b.total.add(d, status, bytes)
		st := b.routes[route]
		if st == nil {
			st = &Stat{}
			b.routes[route] = st
		}
		st.add(d, status, bytes)
		for _, p := range phases {
			b.phases[p.Name] += p.Dur
		}
	}
	st := c.routes[route]
	if st == nil {
		st = &Stat{}
		c.routes[route] = st
	}
	st.add(d, status, bytes)
}

// Query records one database query.
func (c *Collector) Query(d time.Duration) {
	if c == nil {
		return
	}
	now := c.now()
	slow := d >= c.SlowQuery
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, b := range []*bucket{c.minutes.at(now), c.hours.at(now)} {
		b.queries.add(d, 200, 0)
		if slow {
			b.slowQ++
		}
	}
	c.queries.add(d, 200, 0)
	if slow {
		c.slowQ++
	}
}

// QueryHook times every query run through a bun database.
func (c *Collector) QueryHook() bun.QueryHook { return queryHook{c} }

type queryHook struct{ c *Collector }

func (queryHook) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context { return ctx }

func (h queryHook) AfterQuery(_ context.Context, e *bun.QueryEvent) {
	h.c.Query(time.Since(e.StartTime))
}
