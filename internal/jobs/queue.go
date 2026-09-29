// Package jobs implements a persisted command queue (Sonarr-style) and a
// scheduler that pushes commands on intervals.
//
// Rules:
//   - pushing a command identical to one queued/running returns the existing one
//   - an exclusive command only starts when nothing else runs, and blocks others
//   - at most Workers commands run concurrently
//   - commands left "started" by a crash are marked orphaned on startup
package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
)

type Handler func(ctx context.Context, cmd *Run) error

type Definition struct {
	Name        string
	Description string
	Exclusive   bool
	// Priority: lower runs first among queued commands.
	Priority int
	Handler  Handler
}

// Run is the handle given to a running command.
type Run struct {
	Command *model.Command
	q       *Queue
	lastMsg time.Time
}

// Body decodes the command body into v.
func (r *Run) Body(v any) error {
	b, err := json.Marshal(r.Command.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// Progress updates the visible status message (throttled persistence).
func (r *Run) Progress(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.q.mu.Lock()
	r.Command.Message = msg
	snap := *r.Command
	r.q.mu.Unlock()
	if time.Since(r.lastMsg) > time.Second {
		r.lastMsg = time.Now()
		_, _ = r.q.db.NewUpdate().Model(&snap).Column("message").WherePK().Exec(context.Background())
		r.q.bus.Changed("command", "updated", snap.ID)
	}
}

type Queue struct {
	db      *db.DB
	bus     *events.Bus
	log     *slog.Logger
	workers int

	mu      sync.Mutex
	defs    map[string]Definition
	queued  []*model.Command
	running map[int64]*model.Command
	wake    chan struct{}
	held    bool

	onDone []func(cmd *model.Command)
}

// ErrHeld: the queue is held while the database moves or is restored, so
// new commands would land in a table that is being replaced.
var ErrHeld = errors.New("the database is being moved or restored")

// Hold stops starting and accepting commands (true) or starts again
// (false); running ones finish. Used while the database moves.
func (q *Queue) Hold(on bool) {
	q.mu.Lock()
	q.held = on
	q.mu.Unlock()
	q.signal()
}

// Running is the number of commands running now.
func (q *Queue) Running() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.running)
}

func NewQueue(d *db.DB, bus *events.Bus, log *slog.Logger, workers int) *Queue {
	if workers <= 0 {
		workers = 3
	}
	return &Queue{db: d, bus: bus, log: log, workers: workers, defs: map[string]Definition{},
		running: map[int64]*model.Command{}, wake: make(chan struct{}, 1)}
}

func (q *Queue) Register(def Definition) {
	q.mu.Lock()
	q.defs[def.Name] = def
	q.mu.Unlock()
}

// OnDone registers a callback invoked after each command finishes.
func (q *Queue) OnDone(fn func(cmd *model.Command)) { q.onDone = append(q.onDone, fn) }

func (q *Queue) Definitions() []Definition {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Definition, 0, len(q.defs))
	for _, d := range q.defs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func bodyKey(name string, body map[string]any) string {
	b, _ := json.Marshal(body) // map keys are sorted by encoding/json
	return name + string(b)
}

// Push queues a command, or returns the identical queued/running one.
func (q *Queue) Push(ctx context.Context, name string, body map[string]any, trigger string) (*model.Command, error) {
	if body == nil {
		body = map[string]any{}
	}
	// normalize numbers etc. through JSON so dedup keys are stable
	if b, err := json.Marshal(body); err == nil {
		body = map[string]any{}
		_ = json.Unmarshal(b, &body)
	}
	q.mu.Lock()
	if q.held {
		q.mu.Unlock()
		return nil, ErrHeld
	}
	if _, ok := q.defs[name]; !ok {
		q.mu.Unlock()
		return nil, fmt.Errorf("unknown command %q", name)
	}
	key := bodyKey(name, body)
	for _, c := range q.queued {
		if bodyKey(c.Name, c.Body) == key {
			cp := *c
			q.mu.Unlock()
			return &cp, nil
		}
	}
	for _, c := range q.running {
		if bodyKey(c.Name, c.Body) == key {
			cp := *c
			q.mu.Unlock()
			return &cp, nil
		}
	}

	cmd := &model.Command{Name: name, Body: body, Status: model.CommandQueued, Trigger: trigger, QueuedAt: time.Now().UTC()}
	if _, err := q.db.NewInsert().Model(cmd).Exec(ctx); err != nil {
		q.mu.Unlock()
		return nil, err
	}
	cp := *cmd
	q.queued = append(q.queued, cmd)
	q.mu.Unlock()
	q.bus.Changed("command", "created", cp.ID)
	q.bus.Changed("tasks", "updated", 0)
	q.signal()
	return &cp, nil
}

func (q *Queue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// Start recovers state and runs workers until ctx is cancelled.
func (q *Queue) Start(ctx context.Context) error {
	now := time.Now().UTC()
	if _, err := q.db.NewUpdate().Model((*model.Command)(nil)).
		Set("status = ?", model.CommandOrphaned).Set("ended_at = ?", now).
		Where("status = ?", model.CommandStarted).Exec(ctx); err != nil {
		return err
	}
	var pending []*model.Command
	if err := q.db.NewSelect().Model(&pending).Where("status = ?", model.CommandQueued).Order("id").Scan(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	q.queued = append(q.queued, pending...)
	q.mu.Unlock()

	for i := 0; i < q.workers; i++ {
		go q.worker(ctx)
	}
	q.signal()
	return nil
}

func (q *Queue) worker(ctx context.Context) {
	for {
		cmd, def := q.next()
		if cmd == nil {
			select {
			case <-ctx.Done():
				return
			case <-q.wake:
				continue
			case <-time.After(5 * time.Second):
				continue
			}
		}
		q.execute(ctx, cmd, def)
		q.signal()
	}
}

// next picks the next runnable command respecting exclusivity.
func (q *Queue) next() (*model.Command, Definition) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.held {
		return nil, Definition{}
	}
	for _, r := range q.running {
		if q.defs[r.Name].Exclusive {
			return nil, Definition{}
		}
	}
	if len(q.running) >= q.workers {
		return nil, Definition{}
	}
	sort.SliceStable(q.queued, func(i, j int) bool {
		pi, pj := q.defs[q.queued[i].Name].Priority, q.defs[q.queued[j].Name].Priority
		if pi != pj {
			return pi < pj
		}
		return q.queued[i].ID < q.queued[j].ID
	})
	for i, c := range q.queued {
		def := q.defs[c.Name]
		if def.Exclusive && len(q.running) > 0 {
			continue
		}
		// never run two commands with the same name concurrently
		busy := false
		for _, r := range q.running {
			if r.Name == c.Name {
				busy = true
				break
			}
		}
		if busy {
			continue
		}
		q.queued = append(q.queued[:i], q.queued[i+1:]...)
		q.running[c.ID] = c
		return c, def
	}
	return nil, Definition{}
}

func (q *Queue) execute(ctx context.Context, cmd *model.Command, def Definition) {
	start := time.Now().UTC()
	q.mu.Lock()
	cmd.Status = model.CommandStarted
	cmd.StartedAt = &start
	snap := *cmd
	q.mu.Unlock()
	_, _ = q.db.NewUpdate().Model(&snap).Column("status", "started_at").WherePK().Exec(ctx)
	q.bus.Changed("command", "updated", cmd.ID)
	q.bus.Changed("tasks", "updated", 0)
	q.log.Debug("command started", "name", cmd.Name, "id", cmd.ID)

	run := &Run{Command: cmd, q: q}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
			}
		}()
		if def.Handler == nil {
			return fmt.Errorf("no handler for %s", cmd.Name)
		}
		return def.Handler(ctx, run)
	}()

	end := time.Now().UTC()
	q.mu.Lock()
	cmd.EndedAt = &end
	cmd.DurationMs = end.Sub(start).Milliseconds()
	if err != nil {
		cmd.Status = model.CommandFailed
		cmd.Error = err.Error()
	} else {
		cmd.Status = model.CommandCompleted
	}
	final := *cmd
	q.mu.Unlock()
	if err != nil {
		q.log.Warn("command failed", "name", final.Name, "id", final.ID, "err", err)
	} else {
		q.log.Debug("command completed", "name", final.Name, "id", final.ID, "duration", end.Sub(start))
	}
	// persist before removing from the running set so Get never sees a gap
	_, _ = q.db.NewUpdate().Model(&final).Column("status", "ended_at", "duration_ms", "error", "message").WherePK().Exec(context.Background())

	for _, fn := range q.onDone {
		fn(&final)
	}
	q.mu.Lock()
	delete(q.running, final.ID)
	q.mu.Unlock()
	q.bus.Changed("command", "updated", final.ID)
	q.bus.Changed("tasks", "updated", 0)
}

// Active returns queued and running commands.
func (q *Queue) Active() []*model.Command {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]*model.Command, 0, len(q.queued)+len(q.running))
	for _, c := range q.running {
		cp := *c
		out = append(out, &cp)
	}
	for _, c := range q.queued {
		cp := *c
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Recent returns the latest commands from the database.
func (q *Queue) Recent(ctx context.Context, limit int, name ...string) ([]model.Command, error) {
	var out []model.Command
	query := q.db.NewSelect().Model(&out).Order("id DESC").Limit(limit)
	if len(name) > 0 && name[0] != "" {
		query.Where("name = ?", name[0])
	}
	err := query.Scan(ctx)
	return out, err
}

// Get returns a command by id.
func (q *Queue) Get(ctx context.Context, id int64) (*model.Command, error) {
	q.mu.Lock()
	if c, ok := q.running[id]; ok {
		cp := *c
		q.mu.Unlock()
		return &cp, nil
	}
	q.mu.Unlock()
	var c model.Command
	if err := q.db.NewSelect().Model(&c).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, err
	}
	return &c, nil
}

// Wait blocks until the command finishes or ctx is done (used in tests/CLI).
func (q *Queue) Wait(ctx context.Context, id int64) (*model.Command, error) {
	for {
		c, err := q.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		if c.Status != model.CommandQueued && c.Status != model.CommandStarted {
			return c, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}
