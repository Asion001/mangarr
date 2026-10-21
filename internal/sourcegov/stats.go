package sourcegov

import (
	"sort"
	"sync"
	"time"
)

// LatencyBounds are the upper bounds (ms) of the response-time buckets a
// request is counted in; the last bucket holds everything slower.
var LatencyBounds = [...]int64{250, 500, 1000, 2000, 5000, 10000, 30000}

// Latencies counts requests per response-time bucket (one more than
// LatencyBounds).
type Latencies [len(LatencyBounds) + 1]int

// Tally is what a catalog did in one hour.
type Tally struct {
	Requests  int
	Failures  int
	Throttled int
	// Fallbacks is how often a download gave up on this catalog and used
	// another one instead.
	Fallbacks int
	TotalMs   int64
	Latency   Latencies
}

// Add folds o into t.
func (t *Tally) Add(o Tally) {
	t.Requests += o.Requests
	t.Failures += o.Failures
	t.Throttled += o.Throttled
	t.Fallbacks += o.Fallbacks
	t.TotalMs += o.TotalMs
	for i, n := range o.Latency {
		t.Latency[i] += n
	}
}

// Percentile estimates the p-th percentile (0..1) response time in ms from
// the buckets, interpolating inside the bucket it falls in; 0 when nothing
// was timed.
func (l Latencies) Percentile(p float64) int64 {
	total := 0
	for _, n := range l {
		total += n
	}
	if total == 0 {
		return 0
	}
	want := p * float64(total)
	seen := 0
	for i, n := range l {
		if n == 0 {
			continue
		}
		if float64(seen+n) >= want {
			lo := int64(0)
			if i > 0 {
				lo = LatencyBounds[i-1]
			}
			if i == len(LatencyBounds) {
				return lo // slower than the last bound: say at least that
			}
			frac := (want - float64(seen)) / float64(n)
			return lo + int64(frac*float64(LatencyBounds[i]-lo))
		}
		seen += n
	}
	return LatencyBounds[len(LatencyBounds)-1]
}

func latencyBucket(d time.Duration) int {
	ms := d.Milliseconds()
	for i, b := range LatencyBounds {
		if ms <= b {
			return i
		}
	}
	return len(LatencyBounds)
}

// HourKey is a catalog's tally for the hour starting at Hour (UTC).
type HourKey struct {
	Key  Key
	Hour time.Time
}

// Failure is one failed request, kept for the reliability view.
type Failure struct {
	At    time.Time `json:"at"`
	What  string    `json:"what"`
	Error string    `json:"error"`
}

// recentFailures is how many failures each catalog keeps.
const recentFailures = 20

// Stats counts requests per catalog and hour until they are drained to the
// database, and keeps each catalog's latest failures.
type Stats struct {
	mu      sync.Mutex
	pending map[HourKey]*Tally
	fails   map[Key][]Failure
}

func newStats() *Stats {
	return &Stats{pending: map[HourKey]*Tally{}, fails: map[Key][]Failure{}}
}

func (s *Stats) tally(k Key, at time.Time) *Tally {
	hk := HourKey{Key: k, Hour: at.UTC().Truncate(time.Hour)}
	t, ok := s.pending[hk]
	if !ok {
		t = &Tally{}
		s.pending[hk] = t
	}
	return t
}

func (s *Stats) record(k Key, at time.Time, what string, d time.Duration, err error) {
	_, throttled := Classify(err)
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.tally(k, at)
	t.Requests++
	t.TotalMs += d.Milliseconds()
	t.Latency[latencyBucket(d)]++
	if err == nil {
		return
	}
	t.Failures++
	if throttled {
		t.Throttled++
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	f := append(s.fails[k], Failure{At: at.UTC(), What: what, Error: msg})
	if len(f) > recentFailures {
		f = f[len(f)-recentFailures:]
	}
	s.fails[k] = f
}

func (s *Stats) fallback(k Key, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tally(k, at).Fallbacks++
}

// Drain returns the tallies counted since the last drain and forgets them.
func (s *Stats) Drain() map[HourKey]Tally {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[HourKey]Tally, len(s.pending))
	for k, t := range s.pending {
		out[k] = *t
	}
	s.pending = map[HourKey]*Tally{}
	return out
}

// Restore puts tallies back after a drain could not be stored.
func (s *Stats) Restore(m map[HourKey]Tally) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hk, t := range m {
		s.tally(hk.Key, hk.Hour).Add(t)
	}
}

// Failures returns k's latest failures, newest first.
func (s *Stats) Failures(k Key) []Failure {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := append([]Failure(nil), s.fails[k]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	return out
}
