package jobs

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/quiet"
	"github.com/Asion001/mangarr/internal/settings"
)

var ErrScheduleLocked = errors.New("the reader sync interval is set by the environment")

// Task is a scheduled command. Task name == command name.
type Task struct {
	Name        string
	Interval    time.Duration
	MinInterval time.Duration
	Body        map[string]any
	// RunOnStart runs a newly created task immediately instead of after one interval.
	RunOnStart bool
}

type Scheduler struct {
	db    *db.DB
	queue *Queue
	log   *slog.Logger
	tick  time.Duration
	store *settings.Store
	mu    sync.Mutex
	tasks map[string]Task
}

func NewScheduler(d *db.DB, q *Queue, log *slog.Logger, stores ...*settings.Store) *Scheduler {
	store := settings.NewStore(d)
	if len(stores) > 0 {
		store = stores[0]
	}
	s := &Scheduler{db: d, queue: q, log: log, tick: 30 * time.Second, store: store, tasks: map[string]Task{}}
	q.OnDone(s.commandDone)
	return s
}

// Add updates code defaults without overwriting user schedules or pause state.
func (s *Scheduler) Add(ctx context.Context, t Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.MinInterval <= 0 {
		switch t.Name {
		case "HealthCheck":
			t.MinInterval = time.Minute
		case "RefreshSources", "SyncReadProgress":
			t.MinInterval = 5 * time.Minute
		default:
			t.MinInterval = 15 * time.Minute
		}
	}
	now := time.Now().UTC()
	row := &model.ScheduledTask{Name: t.Name, IntervalMinutes: int(t.Interval / time.Minute), UpdatedAt: now}
	if !t.RunOnStart {
		row.LastExecution = &now
	}
	_, err := s.db.NewInsert().Model(row).On("CONFLICT (name) DO UPDATE").Set("interval_minutes = EXCLUDED.interval_minutes").Exec(ctx)
	if err == nil {
		s.tasks[t.Name] = t
	}
	return err
}

func (s *Scheduler) MinIntervalMinutes(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return int(s.tasks[name].MinInterval / time.Minute)
}

func (s *Scheduler) Get(ctx context.Context, name string) (model.ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(ctx, name)
}

func (s *Scheduler) get(ctx context.Context, name string) (model.ScheduledTask, error) {
	row := model.ScheduledTask{Name: name}
	if _, ok := s.tasks[name]; !ok {
		return row, sql.ErrNoRows
	}
	err := s.db.NewSelect().Model(&row).WherePK().Scan(ctx)
	if err == nil {
		err = s.applyEnvironment(ctx, &row)
	}
	return row, err
}

func (s *Scheduler) SetSchedule(ctx context.Context, name string, v Schedule) error {
	s.mu.Lock()
	err := func() error {
		if s.scheduleLocked(name) {
			return ErrScheduleLocked
		}
		if _, err := s.get(ctx, name); err != nil {
			return err
		}
		if err := v.Validate(int(s.tasks[name].MinInterval / time.Minute)); err != nil {
			return err
		}
		row := &model.ScheduledTask{Name: name, ScheduleKind: &v.Kind, TimesOfDay: v.TimesOfDay, Weekdays: v.Weekdays, UpdatedAt: time.Now().UTC()}
		if v.Kind == "interval" {
			row.CustomIntervalMinutes = &v.IntervalMinutes
		}
		_, err := s.db.NewUpdate().Model(row).Column("schedule_kind", "custom_interval_minutes", "times_of_day", "weekdays", "updated_at").WherePK().Exec(ctx)
		return err
	}()
	s.mu.Unlock()
	if err == nil {
		s.changed()
	}
	return err
}

func (s *Scheduler) ResetSchedule(ctx context.Context, name string) error {
	if s.scheduleLocked(name) {
		return ErrScheduleLocked
	}
	s.mu.Lock()
	_, err := s.get(ctx, name)
	if err == nil {
		_, err = s.db.NewUpdate().Model((*model.ScheduledTask)(nil)).Set("schedule_kind = NULL").Set("custom_interval_minutes = NULL").Set("times_of_day = NULL").Set("weekdays = NULL").Set("updated_at = ?", time.Now().UTC()).Where("name = ?", name).Exec(ctx)
	}
	s.mu.Unlock()
	if err == nil {
		s.changed()
	}
	return err
}

func (s *Scheduler) SetPaused(ctx context.Context, name string, paused bool) error {
	return s.setPaused(ctx, name, paused)
}

func (s *Scheduler) SetPausedAll(ctx context.Context, paused bool) error {
	return s.setPaused(ctx, "", paused)
}

func (s *Scheduler) setPaused(ctx context.Context, name string, paused bool) error {
	s.mu.Lock()
	err := func() error {
		names := []string{}
		if name != "" {
			if _, err := s.get(ctx, name); err != nil {
				return err
			}
			names = append(names, name)
		} else {
			for name := range s.tasks {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		// Pausing must not move the daily catch-up anchor.
		_, err := s.db.NewUpdate().Model((*model.ScheduledTask)(nil)).Set("paused = ?", paused).Where("name IN (?)", bun.In(names)).Exec(ctx)
		return err
	}()
	s.mu.Unlock()
	if err == nil {
		s.changed()
	}
	return err
}

func (s *Scheduler) changed() { s.queue.bus.Changed("tasks", "updated", 0) }

func (s *Scheduler) Tasks(ctx context.Context) ([]model.ScheduledTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []model.ScheduledTask
	err := s.db.NewSelect().Model(&rows).Order("name").Scan(ctx)
	out := []model.ScheduledTask{}
	for _, row := range rows {
		if _, ok := s.tasks[row.Name]; ok {
			if err := s.applyEnvironment(ctx, &row); err != nil {
				return nil, err
			}
			out = append(out, row)
		}
	}
	return out, err
}

func (s *Scheduler) Location(ctx context.Context) (*time.Location, error) {
	cfg, err := s.store.Schedule(ctx)
	return quiet.Location(cfg.Timezone), err
}

func (s *Scheduler) Preview(ctx context.Context, v Schedule, n int, names ...string) ([]time.Time, error) {
	if err := v.Validate(1); err != nil {
		return nil, err
	}
	loc, err := s.Location(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	n = min(max(n, 0), 100)
	if len(names) == 0 || names[0] == "" || n == 0 {
		return slots(v, now, n, loc), nil
	}
	row, err := s.Get(ctx, names[0])
	if err != nil {
		return nil, err
	}
	if err := v.Validate(s.MinIntervalMinutes(names[0])); err != nil {
		return nil, err
	}
	row.ScheduleKind, row.CustomIntervalMinutes = &v.Kind, &v.IntervalMinutes
	row.TimesOfDay, row.Weekdays, row.UpdatedAt = v.TimesOfDay, v.Weekdays, now
	return forecast(row, now, n, loc), nil
}

func nextDue(row model.ScheduledTask, loc *time.Location, now time.Time) time.Time {
	v := EffectiveSchedule(row)
	anchor := row.UpdatedAt
	if row.LastExecution != nil {
		anchor = *row.LastExecution
	}
	if v.Kind == "interval" && row.LastExecution == nil {
		return now
	}
	runs := slots(v, anchor, 1, loc)
	if len(runs) == 0 {
		return now.Add(24 * time.Hour)
	}
	return runs[0]
}

func (s *Scheduler) NextRuns(ctx context.Context, name string, n int) ([]time.Time, error) {
	row, err := s.Get(ctx, name)
	if err != nil {
		return nil, err
	}
	out := []time.Time{}
	if row.Paused || n <= 0 {
		return out, nil
	}
	loc, err := s.Location(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range s.queue.Active() {
		if c.Name == name {
			return out, nil
		} // completion establishes the next anchor
	}
	return forecast(row, time.Now(), min(n, 100), loc), nil
}

func (s *Scheduler) Run(ctx context.Context) {
	s.check(ctx)
	t := time.NewTicker(s.tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.check(ctx)
		}
	}
}

func (s *Scheduler) check(ctx context.Context) {
	s.checkAt(ctx, time.Now().UTC())
}

func (s *Scheduler) checkAt(ctx context.Context, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var rows []model.ScheduledTask
	if err := s.db.NewSelect().Model(&rows).Scan(ctx); err != nil {
		s.log.Error("scheduler: load tasks", "err", err)
		return
	}
	loc, err := s.Location(ctx)
	if err != nil {
		s.log.Error("scheduler: load timezone", "err", err)
		return
	}
	active := map[string]bool{}
	for _, cmd := range s.queue.Active() {
		active[cmd.Name] = true
	}
	for _, row := range rows {
		if err := s.applyEnvironment(ctx, &row); err != nil {
			s.log.Error("scheduler: load environment", "err", err)
			continue
		}
		t, ok := s.tasks[row.Name]
		if !ok || row.Paused || active[row.Name] || t.Interval <= 0 || nextDue(row, loc, now).After(now) {
			continue
		}
		if _, err := s.queue.Push(ctx, t.Name, t.Body, "scheduled"); err != nil {
			if !errors.Is(err, ErrHeld) { // due again once the database is back
				s.log.Error("scheduler: push", "task", t.Name, "err", err)
			}
			continue
		}
		_, _ = s.db.NewUpdate().Model((*model.ScheduledTask)(nil)).Set("last_start = ?", now).Where("name = ?", row.Name).Exec(ctx)
	}
}

// commandDone records completion before the queue releases the running command.
// Manual runs count too, even while a schedule is paused.
func (s *Scheduler) commandDone(cmd *model.Command) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[cmd.Name]; !ok || cmd.EndedAt == nil {
		return
	}
	if _, err := s.db.NewUpdate().Model((*model.ScheduledTask)(nil)).Set("last_execution = ?", *cmd.EndedAt).Where("name = ?", cmd.Name).Exec(context.Background()); err != nil {
		s.log.Error("scheduler: record completion", "task", cmd.Name, "err", err)
	}
}

func (s *Scheduler) scheduleLocked(name string) bool {
	if name != "SyncReadProgress" {
		return false
	}
	for _, lock := range s.store.Locks(settings.KeyReadSync) {
		if lock.Path == "intervalMinutes" {
			return true
		}
	}
	return false
}

func (s *Scheduler) applyEnvironment(ctx context.Context, row *model.ScheduledTask) error {
	if !s.scheduleLocked(row.Name) {
		return nil
	}
	v, err := s.store.ReadSync(ctx)
	if err != nil {
		return err
	}
	kind := "interval"
	row.ScheduleKind, row.CustomIntervalMinutes = &kind, &v.IntervalMinutes
	row.TimesOfDay, row.Weekdays = nil, nil
	return nil
}

func forecast(row model.ScheduledTask, now time.Time, n int, loc *time.Location) []time.Time {
	next := nextDue(row, loc, now)
	if next.Before(now) {
		next = now
	}
	out := []time.Time{next.In(loc)}
	return append(out, slots(EffectiveSchedule(row), next, n-1, loc)...)
}
