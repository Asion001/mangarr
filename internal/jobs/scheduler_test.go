package jobs

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func taskScheduler(t *testing.T, d *db.DB) (*Scheduler, *Queue) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := NewQueue(d, events.NewBus(), log, 1)
	q.Register(Definition{Name: "Housekeeping", Handler: func(context.Context, *Run) error { return nil }})
	s := NewScheduler(d, q, log)
	if err := s.Add(t.Context(), Task{Name: "Housekeeping", Interval: 24 * time.Hour, RunOnStart: true}); err != nil {
		t.Fatal(err)
	}
	return s, q
}

func TestTaskControls(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		s, q := taskScheduler(t, d)
		ctx := t.Context()
		if err := s.SetSchedule(ctx, "Housekeeping", Schedule{Kind: "interval", IntervalMinutes: 14}); err == nil {
			t.Fatal("accepted interval below minimum")
		}
		if err := s.SetSchedule(ctx, "Housekeeping", Schedule{Kind: "interval", IntervalMinutes: 20}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetPaused(ctx, "Housekeeping", true); err != nil {
			t.Fatal(err)
		}
		s.check(ctx)
		if len(q.Active()) != 0 {
			t.Fatal("paused task was pushed")
		}
		next, err := s.NextRuns(ctx, "Housekeeping", 3)
		if err != nil || len(next) != 0 {
			t.Fatalf("paused preview: %v %v", next, err)
		}
		// Re-registration on restart updates only the default.
		if err := s.Add(ctx, Task{Name: "Housekeeping", Interval: 12 * time.Hour}); err != nil {
			t.Fatal(err)
		}
		row, err := s.Get(ctx, "Housekeeping")
		if err != nil || !row.Paused || row.IntervalMinutes != 720 || EffectiveSchedule(row).IntervalMinutes != 20 {
			t.Fatalf("override lost on restart: %+v %v", row, err)
		}
		manual, err := q.Push(ctx, "Housekeeping", nil, "manual")
		if err != nil {
			t.Fatal(err)
		}
		cmd, def := q.next()
		if cmd == nil {
			t.Fatal("pause blocked manual run")
		}
		q.execute(ctx, cmd, def)
		finished, err := q.Get(ctx, manual.ID)
		row, _ = s.Get(ctx, "Housekeeping")
		if err != nil || finished.Status != model.CommandCompleted || row.LastExecution == nil || !row.LastExecution.Equal(*finished.EndedAt) {
			t.Fatalf("manual completion not recorded: %+v %v", finished, err)
		}
		if err := s.SetPausedAll(ctx, false); err != nil {
			t.Fatal(err)
		}
		s.checkAt(ctx, finished.EndedAt.Add(19*time.Minute))
		if len(q.Active()) != 0 {
			t.Fatal("interval fired before completion plus interval")
		}
		s.checkAt(ctx, finished.EndedAt.Add(20*time.Minute))
		s.checkAt(ctx, finished.EndedAt.Add(60*time.Minute))
		if len(q.Active()) != 1 {
			t.Fatal("queued task duplicated")
		}
		if err := s.ResetSchedule(ctx, "Housekeeping"); err != nil {
			t.Fatal(err)
		}
		row, _ = s.Get(ctx, "Housekeeping")
		if row.Paused || CustomSchedule(row) || EffectiveSchedule(row).IntervalMinutes != 720 {
			t.Fatalf("bad reset: %+v", row)
		}
	})
}

func TestDailyCatchUp(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		s, q := taskScheduler(t, d)
		ctx := t.Context()
		if err := s.store.Set(ctx, settings.KeySchedule, settings.Schedule{Timezone: "Europe/Warsaw"}); err != nil {
			t.Fatal(err)
		}
		v := Schedule{Kind: "daily", TimesOfDay: []string{"05:00"}, Weekdays: []string{"mon", "thu"}}
		if err := s.SetSchedule(ctx, "Housekeeping", v); err != nil {
			t.Fatal(err)
		}
		anchor := time.Date(2026, 3, 22, 12, 0, 0, 0, time.UTC)
		if _, err := d.NewUpdate().Model((*model.ScheduledTask)(nil)).Set("last_execution = NULL").Set("updated_at = ?", anchor).Where("name = ?", "Housekeeping").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		s.checkAt(ctx, time.Date(2026, 3, 23, 3, 59, 0, 0, time.UTC))
		if len(q.Active()) != 0 {
			t.Fatal("daily task pushed before its local slot")
		}
		// A week offline spans several slots and the spring DST transition.
		now := time.Date(2026, 3, 30, 4, 0, 0, 0, time.UTC)
		s.checkAt(ctx, now)
		s.checkAt(ctx, now.Add(time.Hour))
		if len(q.Active()) != 1 {
			t.Fatalf("missed slots did not coalesce: %+v", q.Active())
		}
		row, err := s.Get(ctx, "Housekeeping")
		if err != nil || EffectiveSchedule(row).TimesOfDay[0] != "05:00" {
			t.Fatalf("daily persistence: %+v %v", row, err)
		}
		s.commandDone(&model.Command{Name: "Housekeeping", EndedAt: &now})
		q.mu.Lock()
		q.queued = nil
		q.mu.Unlock()
		s.checkAt(ctx, now.Add(time.Hour))
		if len(q.Active()) != 0 {
			t.Fatal("caught up twice")
		}
		s.checkAt(ctx, time.Date(2026, 4, 2, 3, 0, 0, 0, time.UTC))
		if len(q.Active()) != 1 {
			t.Fatal("next allowed weekday slot did not run")
		}
	})
}

func TestScheduleSlots(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Warsaw")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, after       string
		times, days, want []string
	}{
		{"spring", "2026-03-28T05:00:00+01:00", []string{"05:00"}, nil, []string{"2026-03-29T05:00:00+02:00", "2026-03-30T05:00:00+02:00", "2026-03-31T05:00:00+02:00"}},
		{"autumn", "2026-10-24T05:00:00+02:00", []string{"05:00"}, nil, []string{"2026-10-25T05:00:00+01:00", "2026-10-26T05:00:00+01:00", "2026-10-27T05:00:00+01:00"}},
		{"weekdays", "2026-09-24T05:00:00+02:00", []string{"05:00"}, []string{"mon", "thu"}, []string{"2026-09-28T05:00:00+02:00", "2026-10-01T05:00:00+02:00", "2026-10-05T05:00:00+02:00"}},
		{"sorted times", "2026-09-24T05:00:00+02:00", []string{"23:00", "05:00"}, nil, []string{"2026-09-24T23:00:00+02:00", "2026-09-25T05:00:00+02:00", "2026-09-25T23:00:00+02:00"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			after, _ := time.Parse(time.RFC3339, tt.after)
			runs := slots(Schedule{Kind: "daily", TimesOfDay: tt.times, Weekdays: tt.days}, after, 3, loc)
			if len(runs) != 3 {
				t.Fatalf("runs: %v", runs)
			}
			for i, want := range tt.want {
				if got := runs[i].Format(time.RFC3339); got != want {
					t.Fatalf("slot %d: %s want %s", i, got, want)
				}
			}
		})
	}
	for _, date := range []time.Time{time.Date(2026, 3, 29, 0, 0, 0, 0, loc), time.Date(2026, 10, 25, 0, 0, 0, 0, loc)} {
		runs := slots(Schedule{Kind: "daily", TimesOfDay: []string{"02:30"}}, date, 2, loc)
		expected := time.Date(date.Year(), date.Month(), date.Day(), 2, 30, 0, 0, loc)
		if !runs[0].Equal(expected) || runs[1].Day() == runs[0].Day() {
			t.Fatalf("DST slot repeated or skipped unexpectedly: %v", runs)
		}
	}
}

func TestScheduleValidation(t *testing.T) {
	for _, v := range []Schedule{
		{Kind: "weekly"}, {Kind: "interval", IntervalMinutes: -1}, {Kind: "interval", IntervalMinutes: 525601},
		{Kind: "interval", IntervalMinutes: 30, TimesOfDay: []string{"03:00"}},
		{Kind: "daily"}, {Kind: "daily", TimesOfDay: []string{"25:00"}}, {Kind: "daily", TimesOfDay: []string{"3:00"}},
		{Kind: "daily", TimesOfDay: []string{"03:00", "03:00"}}, {Kind: "daily", TimesOfDay: []string{"03:00"}, Weekdays: []string{"bad"}},
	} {
		if err := v.Validate(15); err == nil {
			t.Errorf("accepted %+v", v)
		}
	}
}

func TestConcurrentCommandDedupAndHistory(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		_, q := taskScheduler(t, d)
		ctx := t.Context()
		var wg sync.WaitGroup
		ids := make(chan int64, 20)
		for i := 0; i < 20; i++ {
			wg.Go(func() {
				cmd, err := q.Push(ctx, "Housekeeping", nil, "manual")
				if err != nil {
					t.Error(err)
					return
				}
				ids <- cmd.ID
			})
		}
		wg.Wait()
		close(ids)
		var id int64
		for next := range ids {
			if id != 0 && next != id {
				t.Fatal("concurrent duplicate")
			}
			id = next
		}
		rows, err := q.Recent(ctx, 20, "Housekeeping")
		if err != nil || len(rows) != 1 {
			t.Fatalf("history: %v %v", rows, err)
		}
		rows, err = q.Recent(ctx, 20, "Unknown")
		if err != nil || len(rows) != 0 {
			t.Fatalf("filtered history: %v %v", rows, err)
		}
	})
}

func TestTaskPreviewAndEnvironment(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		s, _ := taskScheduler(t, d)
		ctx := t.Context()
		finished := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Millisecond) // databases keep at most microseconds
		if _, err := d.NewUpdate().Model((*model.ScheduledTask)(nil)).Set("last_execution = ?", finished).Where("name = ?", "Housekeeping").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		preview, err := s.Preview(ctx, Schedule{Kind: "interval", IntervalMinutes: 30}, 3, "Housekeeping")
		if err != nil || len(preview) != 3 || !preview[0].Equal(finished.Add(30*time.Minute)) {
			t.Fatalf("preview must start from completion: %v %v", preview, err)
		}
		if err := s.Add(ctx, Task{Name: "SyncReadProgress", Interval: 30 * time.Minute}); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSchedule(ctx, "SyncReadProgress", Schedule{Kind: "interval", IntervalMinutes: 45}); err != nil {
			t.Fatal(err)
		}
		s.store.SetOverlay(settings.KeyReadSync, []byte(`{"intervalMinutes":60}`), []settings.Lock{{Path: "intervalMinutes", Env: "MANGARR_READSYNC_INTERVAL_MINUTES"}})
		effective, err := s.store.ReadSync(ctx)
		if err != nil || effective.IntervalMinutes != 60 {
			t.Fatalf("environment ignored by reader settings: %+v %v", effective, err)
		}
		row, err := s.Get(ctx, "SyncReadProgress")
		if err != nil || EffectiveSchedule(row).IntervalMinutes != 60 {
			t.Fatalf("environment ignored by scheduler: %+v %v", row, err)
		}
		if err := s.SetSchedule(ctx, "SyncReadProgress", Schedule{Kind: "interval", IntervalMinutes: 90}); err != ErrScheduleLocked {
			t.Fatalf("overwrote environment: %v", err)
		}
		if err := s.ResetSchedule(ctx, "SyncReadProgress"); err != ErrScheduleLocked {
			t.Fatalf("reset environment: %v", err)
		}
		s.store.SetOverlay(settings.KeyReadSync, nil, nil)
		row, err = s.Get(ctx, "SyncReadProgress")
		if err != nil || EffectiveSchedule(row).IntervalMinutes != 45 {
			t.Fatalf("stored override lost: %+v %v", row, err)
		}
	})
}

func TestTaskPauseDoesNotCancelAndEmitsChanges(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		s, q := taskScheduler(t, d)
		ctx := t.Context()
		changes := make(chan string, 20)
		unsub := q.bus.Subscribe(func(e events.Event) {
			if r, ok := e.Payload.(events.Resource); ok && r.Name == "tasks" {
				changes <- r.Action
			}
		}, events.ResourceChanged)
		defer unsub()
		started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
		q.Register(Definition{Name: "Housekeeping", Handler: func(ctx context.Context, run *Run) error { close(started); <-release; return nil }})
		cmd, err := q.Push(ctx, "Housekeeping", nil, "manual")
		if err != nil {
			t.Fatal(err)
		}
		running, def := q.next()
		go func() { q.execute(ctx, running, def); close(done) }()
		<-started
		if err := s.SetPaused(ctx, "Housekeeping", true); err != nil {
			close(release)
			<-done
			t.Fatal(err)
		}
		if q.Running() != 1 {
			t.Error("pause cancelled running command")
		}
		close(release)
		<-done
		got, err := q.Get(ctx, cmd.ID)
		if err != nil || got.Status != model.CommandCompleted {
			t.Fatalf("running task did not finish: %+v %v", got, err)
		}
		if len(changes) != 4 {
			t.Fatalf("expected queued, started, paused and completed events, got %d", len(changes))
		}
		s.checkAt(ctx, time.Now().Add(48*time.Hour))
		if len(q.Active()) != 0 {
			t.Fatal("paused task rescheduled after completion")
		}
	})
}

func TestTaskRecoversQueuedCommand(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		s, q := taskScheduler(t, d)
		ctx := t.Context()
		persisted := &model.Command{Name: "Housekeeping", Body: map[string]any{}, Status: model.CommandQueued, Trigger: "scheduled", QueuedAt: time.Now().Add(-48 * time.Hour)}
		if _, err := d.NewInsert().Model(persisted).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		q.Hold(true)
		if err := q.Start(ctx); err != nil {
			t.Fatal(err)
		}
		s.check(ctx)
		active := q.Active()
		if len(active) != 1 || active[0].ID != persisted.ID {
			t.Fatalf("restart duplicated persisted command: %+v", active)
		}
	})
}
