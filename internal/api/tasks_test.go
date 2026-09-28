package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func TestTaskControlsAPI(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			_, ring := logging.Setup("error", io.Discard)
			a, err := app.New(ctx, &config.Config{DataDir: t.TempDir(), DB: dsn, AuthDisabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			srv := httptest.NewServer(api.New(a))
			defer srv.Close()
			c := caller{t, http.DefaultClient, srv.URL}
			getTasks := func() map[string]api.TaskInfo {
				t.Helper()
				var rows []api.TaskInfo
				if code := c.do("GET", "/api/v1/system/tasks", "", &rows); code != 200 {
					t.Fatal(code)
				}
				out := map[string]api.TaskInfo{}
				for _, row := range rows {
					out[row.Name] = row
				}
				return out
			}
			mutate := func(method, path, body string) {
				t.Helper()
				if code := c.do(method, "/api/v1/system/tasks"+path, body, nil); code < 200 || code >= 300 {
					t.Fatalf("%s %s: %d", method, path, code)
				}
			}
			initial := getTasks()["Housekeeping"]
			if !initial.Scheduled || initial.Paused || initial.Schedule.IntervalMinutes != 1440 || initial.MinIntervalMinutes != 15 || len(initial.NextRuns) != 3 {
				t.Fatalf("initial task: %+v", initial)
			}
			for _, body := range []string{`{"kind":"interval","intervalMinutes":14}`, `{"kind":"daily","timesOfDay":[]}`, `{"kind":"daily","timesOfDay":["24:00"]}`, `{"kind":"daily","timesOfDay":["03:00"],"weekdays":["no"]}`, `{"kind":"bad"}`} {
				if code := c.do("PUT", "/api/v1/system/tasks/Housekeeping/schedule", body, nil); code != 400 {
					t.Fatalf("validation %s: %d", body, code)
				}
			}
			if code := c.do("POST", "/api/v1/system/tasks/Unknown/pause", "", nil); code != 404 {
				t.Fatalf("unknown: %d", code)
			}
			mutate("PUT", "/Housekeeping/schedule", `{"kind":"interval","intervalMinutes":60}`)
			info := getTasks()["Housekeeping"]
			if !info.Custom || info.Schedule.IntervalMinutes != 60 || info.DefaultSchedule.IntervalMinutes != 1440 {
				t.Fatalf("custom: %+v", info)
			}
			mutate("POST", "/pause-all", "")
			for _, row := range getTasks() {
				if row.Scheduled && (!row.Paused || row.NextExecution != nil || len(row.NextRuns) != 0) {
					t.Fatalf("pause-all: %+v", row)
				}
			}
			var first, duplicate model.Command
			for _, cmd := range []*model.Command{&first, &duplicate} {
				if code := c.do("POST", "/api/v1/commands", `{"name":"Housekeeping"}`, cmd); code != 200 {
					t.Fatalf("run while paused: %d", code)
				}
			}
			if first.ID == 0 || first.ID != duplicate.ID {
				t.Fatal("run now duplicated")
			}
			info = getTasks()["Housekeeping"]
			if info.Running == nil || info.Running.CommandID != first.ID || !info.Paused {
				t.Fatalf("queued status: %+v", info)
			}
			mutate("POST", "/resume-all", "")
			for _, row := range getTasks() {
				if row.Paused {
					t.Fatalf("resume-all: %+v", row)
				}
			}
			mutate("POST", "/Housekeeping/pause", "")
			mutate("DELETE", "/Housekeeping/schedule", "")
			info = getTasks()["Housekeeping"]
			if info.Custom || !info.Paused || info.Schedule.IntervalMinutes != 1440 {
				t.Fatalf("reset altered pause/default: %+v", info)
			}
			mutate("POST", "/Housekeeping/resume", "")
			if getTasks()["Housekeeping"].Paused {
				t.Fatal("resume failed")
			}
			mutate("PUT", "/SyncReadProgress/schedule", `{"kind":"interval","intervalMinutes":45}`)
			var rs settings.ReadSync
			if code := c.do("GET", "/api/v1/settings/readsync", "", &rs); code != 200 || rs.IntervalMinutes != 45 {
				t.Fatalf("reader did not reflect task: %d %+v", code, rs)
			}
			if code := c.do("PUT", "/api/v1/settings/readsync", `{"intervalMinutes":55}`, &rs); code != 200 || rs.IntervalMinutes != 55 {
				t.Fatalf("reader save: %d %+v", code, rs)
			}
			if getTasks()["SyncReadProgress"].Schedule.IntervalMinutes != 55 {
				t.Fatal("task did not reflect reader")
			}
			if code := c.do("PUT", "/api/v1/settings/readsync", `{"intervalMinutes":4}`, nil); code != 400 {
				t.Fatalf("reader minimum: %d", code)
			}
			mutate("DELETE", "/SyncReadProgress/schedule", "")
			if code := c.do("GET", "/api/v1/settings/readsync", "", &rs); code != 200 || rs.IntervalMinutes != 30 {
				t.Fatalf("reader reset: %d %+v", code, rs)
			}
			if err := a.Settings.Set(ctx, settings.KeySchedule, settings.Schedule{Timezone: "Europe/Warsaw"}); err != nil {
				t.Fatal(err)
			}
			var preview api.TaskPreview
			if code := c.do("POST", "/api/v1/system/tasks/preview", `{"name":"Housekeeping","kind":"daily","timesOfDay":["03:30"],"weekdays":["mon","thu"]}`, &preview); code != 200 || len(preview.NextRuns) != 3 || preview.Timezone != "Europe/Warsaw" {
				t.Fatalf("preview: %d %+v", code, preview)
			}
			loc, _ := time.LoadLocation("Europe/Warsaw")
			for _, run := range preview.NextRuns {
				local := run.In(loc)
				if local.Hour() != 3 || local.Minute() != 30 || (local.Weekday() != time.Monday && local.Weekday() != time.Thursday) {
					t.Fatalf("bad preview slot %v", run)
				}
			}
			mutate("PUT", "/RefreshMetadata/schedule", `{"kind":"daily","timesOfDay":["03:30"],"weekdays":["mon","thu"]}`)
			info = getTasks()["RefreshMetadata"]
			if info.Schedule.Kind != "daily" || info.Schedule.TimesOfDay[0] != "03:30" || info.Timezone != "Europe/Warsaw" {
				t.Fatalf("daily response: %+v", info)
			}
			end := time.Now().UTC()
			start := end.Add(-38 * time.Second)
			completed := &model.Command{Name: "Housekeeping", Body: map[string]any{}, Status: model.CommandCompleted, Trigger: "scheduled", QueuedAt: start, StartedAt: &start, EndedAt: &end, DurationMs: 38000, Message: "Finished"}
			if _, err := a.DB.NewInsert().Model(completed).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			info = getTasks()["Housekeeping"]
			if info.LastRun == nil || info.LastRun.DurationMs != 38000 || info.LastRun.Message != "Finished" {
				t.Fatalf("last run: %+v", info)
			}
			var history []model.Command
			if code := c.do("GET", "/api/v1/commands?name=Housekeeping&limit=1", "", &history); code != 200 || len(history) != 1 || history[0].ID != completed.ID {
				t.Fatalf("history: %d %+v", code, history)
			}
			if code := c.do("GET", "/api/v1/commands?name=Unknown&limit=20", "", &history); code != 200 || len(history) != 0 {
				t.Fatalf("history filter: %d %+v", code, history)
			}
		})
	}
}
