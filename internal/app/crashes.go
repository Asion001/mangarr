package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/health"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/version"
)

// runStateFile records whether mangarr is running, so the next start can
// tell a clean stop from one it never got to finish (killed for memory, a
// panic, the host losing power). It lives in the data folder, not the
// database: moving the database or restoring a backup copies the settings of
// a server that was running at the time.
const runStateFile = "run.json"

// crashLogKey keeps the unclean stops found at start.
const crashLogKey = "crash_log"

// Heartbeat is how often a running server notes that it is still alive,
// which dates an unclean stop to within this interval.
var Heartbeat = time.Minute

// keepCrashes is how many unclean stops the log remembers.
const keepCrashes = 100

type runState struct {
	Running    bool      `json:"running"`
	StartedAt  time.Time `json:"startedAt"`
	LastSeenAt time.Time `json:"lastSeenAt"`
	Version    string    `json:"version,omitempty"`
}

// runTracker is this run's entry in run_state.
type runTracker struct {
	mu      sync.Mutex
	state   *runState
	stopped bool
}

// Crash is one unclean stop.
type Crash struct {
	// At is the last time the server was known to be alive.
	At        time.Time `json:"at"`
	StartedAt time.Time `json:"startedAt"`
	FoundAt   time.Time `json:"foundAt"`
	Version   string    `json:"version,omitempty"`
	// Busy is what the queue was doing when it stopped.
	Busy []string `json:"busy,omitempty"`
}

// CrashLog is every unclean stop found so far (the most recent ones kept).
type CrashLog struct {
	Total  int     `json:"total"`
	Recent []Crash `json:"recent"`
}

// Crashes returns the unclean stops found so far.
func (a *App) Crashes(ctx context.Context) CrashLog {
	var l CrashLog
	_ = a.Settings.Get(ctx, crashLogKey, &l)
	return l
}

// trackRuns records an unclean previous stop, marks this run as running
// and keeps its heartbeat until ctx ends. It runs before the download
// manager requeues what it found, so a crash can name the chapters it was
// busy with.
func (a *App) trackRuns(ctx context.Context) error {
	var prev runState
	if b, err := os.ReadFile(a.runStatePath()); err == nil {
		_ = json.Unmarshal(b, &prev)
	}
	now := time.Now().UTC()
	if prev.Running {
		c := Crash{At: prev.LastSeenAt, StartedAt: prev.StartedAt, FoundAt: now, Version: prev.Version, Busy: a.busyJobs(ctx)}
		if c.At.IsZero() {
			c.At = prev.StartedAt
		}
		l := a.Crashes(ctx)
		l.Total++
		l.Recent = append([]Crash{c}, l.Recent...)
		if len(l.Recent) > keepCrashes {
			l.Recent = l.Recent[:keepCrashes]
		}
		if err := a.Settings.Set(ctx, crashLogKey, l); err != nil {
			return err
		}
		a.Log.Warn("mangarr did not stop cleanly last time (killed, most likely out of memory, or crashed)",
			"lastSeen", c.At, "busy", strings.Join(c.Busy, "; "))
	}
	st := runState{Running: true, StartedAt: now, LastSeenAt: now, Version: version.Version + " " + version.Build}
	if err := a.writeRunState(st); err != nil {
		return err
	}
	a.run.state = &st
	go func() {
		t := time.NewTicker(Heartbeat)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				a.run.mu.Lock()
				if !a.run.stopped {
					st.LastSeenAt = time.Now().UTC()
					_ = a.writeRunState(st)
				}
				a.run.mu.Unlock()
			}
		}
	}()
	return nil
}

// stopped marks a clean stop.
func (a *App) stopped() {
	a.run.mu.Lock()
	defer a.run.mu.Unlock()
	if a.run.state == nil || a.run.stopped {
		return
	}
	a.run.stopped = true
	st := *a.run.state
	st.Running, st.LastSeenAt = false, time.Now().UTC()
	if err := a.writeRunState(st); err != nil {
		a.Log.Warn("could not record a clean stop", "err", err)
	}
}

func (a *App) runStatePath() string { return filepath.Join(a.Cfg.DataDir, runStateFile) }

// writeRunState replaces the file in one step, so a kill mid-write can't
// leave half of it.
func (a *App) writeRunState(st runState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	tmp := a.runStatePath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.runStatePath())
}

// busyJobs names the chapters the queue was working on here.
func (a *App) busyJobs(ctx context.Context) []string {
	var rows []struct {
		Kind   string `bun:"kind"`
		Status string `bun:"status"`
		Title  string `bun:"title"`
		Number string `bun:"number_key"`
	}
	err := a.DB.NewSelect().TableExpr("download_jobs AS j").
		ColumnExpr("j.kind, j.status, s.title, c.number_key").
		Join("JOIN series AS s ON s.id = j.series_id").
		Join("JOIN chapters AS c ON c.id = j.chapter_id").
		Where("j.status IN (?)", bun.List([]string{model.JobDownloading, model.JobProcessing, model.JobImporting})).
		OrderExpr("j.id").Limit(10).Scan(ctx, &rows)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, fmt.Sprintf("%s ch. %s (%s)", r.Title, r.Number, r.Status))
	}
	return out
}

// crashHealth counts the unclean stops of the last week on the Health
// page: an error once the server keeps dying, a warning before that.
func (a *App) crashHealth(ctx context.Context) []health.Check {
	l := a.Crashes(ctx)
	now := time.Now()
	day, week := 0, 0
	var items []health.CheckItem
	for _, c := range l.Recent {
		if now.Sub(c.At) > 7*24*time.Hour {
			continue
		}
		week++
		if now.Sub(c.At) <= 24*time.Hour {
			day++
		}
		if len(items) < 10 {
			detail := fmt.Sprintf("running since %s, back at %s", c.StartedAt.Local().Format("Jan 2 15:04"), c.FoundAt.Local().Format("Jan 2 15:04"))
			if len(c.Busy) > 0 {
				detail += "; busy with " + strings.Join(c.Busy, ", ")
			}
			items = append(items, health.CheckItem{Label: "Stopped around " + c.At.Local().Format("Jan 2 15:04"), Link: "/system/logs", Detail: detail})
		}
	}
	if week == 0 {
		return nil
	}
	typ := health.Warning
	if day >= 3 {
		typ = health.Error
	}
	msg := fmt.Sprintf("mangarr stopped unexpectedly %s in the last 24 hours and %s in the last 7 days (%d in total), most likely killed for running out of memory; logs/crash.txt has anything Go printed",
		times(day), times(week), l.Total)
	return []health.Check{{Source: "Crashes", Type: typ, Message: msg, Link: "/system/logs", Items: items, Key: "unclean stops"}}
}

func times(n int) string {
	if n == 1 {
		return "once"
	}
	return fmt.Sprintf("%d times", n)
}
