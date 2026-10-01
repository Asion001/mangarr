package worker

import (
	"slices"
	"sync"
	"time"
)

// Worker states, as the status page shows them.
const (
	StateStarting = "starting" // saying hello
	StateWaiting  = "waiting"  // the server doesn't answer yet
	StateReady    = "ready"    // asking for work
	StateOff      = "off"      // switched off in System → Workers: waits to be switched on
	StateStopped  = "stopped"
	StateFailed   = "failed" // gave up (a refused key, say)
)

// recentKept is how many finished tasks the status page lists.
const recentKept = 50

// Status is what a worker is doing, kept for the status page of a desktop
// worker. Every method is safe on a nil *Status, which keeps nothing.
type Status struct {
	mu     sync.Mutex
	state  string
	err    string
	since  time.Time
	hello  Welcome
	models []string
	devs   []string
	active map[int64]*Activity
	recent []Finished
	totals Totals
}

// Activity is one task in progress.
type Activity struct {
	ID         int64     `json:"id"`
	Kind       string    `json:"kind"`
	Label      string    `json:"label,omitempty"`
	Stage      string    `json:"stage"`
	PagesDone  int       `json:"pagesDone"`
	PagesTotal int       `json:"pagesTotal"`
	Started    time.Time `json:"started"`
}

// Finished is one task this worker is done with, well or not.
type Finished struct {
	ID       int64     `json:"id"`
	Kind     string    `json:"kind"`
	Label    string    `json:"label,omitempty"`
	Pages    int       `json:"pages"`
	BytesIn  int64     `json:"bytesIn"`
	BytesOut int64     `json:"bytesOut"`
	Seconds  float64   `json:"seconds"`
	GPU      string    `json:"gpu,omitempty"`
	Error    string    `json:"error,omitempty"`
	At       time.Time `json:"at"`
}

// Totals add up every task since the worker started.
type Totals struct {
	Done     int            `json:"done"`
	Failed   int            `json:"failed"`
	ByKind   map[string]int `json:"byKind"`
	Pages    int            `json:"pages"`
	BytesIn  int64          `json:"bytesIn"`
	BytesOut int64          `json:"bytesOut"`
}

// Snapshot is a copy of the status at one moment.
type Snapshot struct {
	State   string     `json:"state"`
	Error   string     `json:"error,omitempty"`
	Since   time.Time  `json:"since"`
	Name    string     `json:"name,omitempty"`
	Roles   []string   `json:"roles"`
	Limit   int        `json:"limit"`
	Models  []string   `json:"models"`
	Devices []string   `json:"devices"`
	Active  []Activity `json:"active"`
	Recent  []Finished `json:"recent"`
	Totals  Totals     `json:"totals"`
}

// NewStatus starts an empty status.
func NewStatus() *Status {
	return &Status{state: StateStopped, since: time.Now(), active: map[int64]*Activity{}, totals: Totals{ByKind: map[string]int{}}}
}

// Set records the worker's state (and why, for waiting and failed).
func (s *Status) Set(state string, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != state {
		s.since = time.Now()
	}
	s.state, s.err = state, ""
	if err != nil {
		s.err = err.Error()
	}
	if state == StateStopped || state == StateFailed {
		clear(s.active)
	}
}

// State is the worker's state now.
func (s *Status) State() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *Status) welcome(w Welcome, models, devices []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.hello, s.models, s.devs = w, models, devices
	s.mu.Unlock()
	s.Set(StateReady, nil)
}

func (s *Status) begin(t Task) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[t.ID] = &Activity{ID: t.ID, Kind: t.Kind, Label: t.Label, Stage: "starting", PagesTotal: t.PagesTotal, Started: time.Now()}
}

// stage says what a task is doing now; a negative count is left as it is.
func (s *Status) stage(id int64, stage string, done, total int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.active[id]
	if a == nil {
		return
	}
	if stage != "" {
		a.Stage = stage
	}
	if done >= 0 {
		a.PagesDone = done
	}
	if total >= 0 {
		a.PagesTotal = total
	}
}

func (s *Status) end(t Task, res result, started time.Time, err error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, t.ID)
	f := Finished{ID: t.ID, Kind: t.Kind, Label: t.Label, Pages: res.Pages, BytesIn: res.BytesIn, BytesOut: res.BytesOut,
		GPU: res.GPU, Seconds: time.Since(started).Seconds(), At: time.Now()}
	if err != nil {
		f.Error = err.Error()
		s.totals.Failed++
	} else {
		s.totals.Done++
		s.totals.ByKind[t.Kind]++
	}
	s.totals.Pages += res.Pages
	s.totals.BytesIn += res.BytesIn
	s.totals.BytesOut += res.BytesOut
	s.recent = append([]Finished{f}, s.recent...)
	if len(s.recent) > recentKept {
		s.recent = s.recent[:recentKept]
	}
}

// Snapshot copies the status; limit is the tasks the worker takes at once.
func (s *Status) Snapshot(limit int) Snapshot {
	if s == nil {
		return Snapshot{State: StateStopped}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Snapshot{State: s.state, Error: s.err, Since: s.since, Name: s.hello.Name, Roles: slices.Clone(s.hello.Roles), Limit: limit,
		Models: slices.Clone(s.models), Devices: slices.Clone(s.devs), Active: []Activity{}, Recent: slices.Clone(s.recent), Totals: s.totals}
	out.Totals.ByKind = map[string]int{}
	for k, v := range s.totals.ByKind {
		out.Totals.ByKind[k] = v
	}
	for _, a := range s.active {
		out.Active = append(out.Active, *a)
	}
	slices.SortFunc(out.Active, func(a, b Activity) int { return a.Started.Compare(b.Started) })
	if out.Recent == nil {
		out.Recent = []Finished{}
	}
	return out
}
