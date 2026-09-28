package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/quiet"
	"github.com/Asion001/mangarr/internal/settings"
)

type TaskRunning struct {
	CommandID int64  `json:"commandId"`
	Message   string `json:"message"`
	Status    string `json:"status"`
}

type TaskLastRun struct {
	Status     string     `json:"status"`
	DurationMs int64      `json:"durationMs"`
	Message    string     `json:"message"`
	Error      string     `json:"error"`
	EndedAt    *time.Time `json:"endedAt"`
}

type taskPath struct {
	Name string `path:"name"`
}

type TaskPreview struct {
	NextRuns []time.Time `json:"nextRuns"`
	Timezone string      `json:"timezone"`
}

func taskError(err error) error {
	if errors.Is(err, jobs.ErrScheduleLocked) {
		return huma.Error409Conflict(err.Error())
	}
	if errors.Is(err, sql.ErrNoRows) {
		return huma.Error404NotFound("scheduled task not found")
	}
	return toHTTPError(err)
}

func (s *Server) taskInfos(ctx context.Context) ([]TaskInfo, error) {
	rows, err := s.app.Scheduler.Tasks(ctx)
	if err != nil {
		return nil, err
	}
	loc, err := s.app.Scheduler.Location(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]model.ScheduledTask{}
	for _, row := range rows {
		byName[row.Name] = row
	}
	active := map[string]*model.Command{}
	for _, cmd := range s.app.Queue.Active() {
		if active[cmd.Name] == nil || cmd.Status == model.CommandStarted {
			active[cmd.Name] = cmd
		}
	}
	out := []TaskInfo{}
	for _, def := range s.app.Queue.Definitions() {
		info := TaskInfo{Name: def.Name, Description: def.Description, NextRuns: []time.Time{}, Timezone: quiet.ZoneName(loc)}
		if row, ok := byName[def.Name]; ok {
			effective, defaultSchedule := jobs.EffectiveSchedule(row), jobs.DefaultSchedule(row)
			info.Scheduled, info.Paused, info.Custom = true, row.Paused, jobs.CustomSchedule(row)
			info.Schedule, info.DefaultSchedule = &effective, &defaultSchedule
			info.IntervalMinutes, info.LastExecution = effective.IntervalMinutes, row.LastExecution
			info.MinIntervalMinutes = s.app.Scheduler.MinIntervalMinutes(def.Name)
			info.NextRuns, err = s.app.Scheduler.NextRuns(ctx, def.Name, 3)
			if err != nil {
				return nil, err
			}
			if len(info.NextRuns) > 0 {
				info.NextExecution = &info.NextRuns[0]
			}
		}
		if cmd := active[def.Name]; cmd != nil {
			info.Running = &TaskRunning{CommandID: cmd.ID, Message: cmd.Message, Status: cmd.Status}
		}
		var last model.Command
		err := s.app.DB.NewSelect().Model(&last).Where("name = ?", def.Name).Where("ended_at IS NOT NULL").Order("id DESC").Limit(1).Scan(ctx)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if err == nil {
			info.LastRun = &TaskLastRun{Status: last.Status, DurationMs: last.DurationMs, Message: last.Message, Error: last.Error, EndedAt: last.EndedAt}
		}
		out = append(out, info)
	}
	return out, nil
}

func (s *Server) registerTasks() {
	tags := []string{"System"}
	huma.Register(s.api, huma.Operation{OperationID: "system-tasks", Method: http.MethodGet, Path: "/api/v1/system/tasks", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []TaskInfo }, error) {
			infos, err := s.taskInfos(ctx)
			return &struct{ Body []TaskInfo }{infos}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "system-tasks-schedule", Method: http.MethodPut, Path: "/api/v1/system/tasks/{name}/schedule", Tags: tags},
		func(ctx context.Context, in *struct {
			Name string `path:"name"`
			Body jobs.Schedule
		}) (*struct{}, error) {
			if _, err := s.app.Scheduler.Get(ctx, in.Name); err != nil {
				return nil, taskError(err)
			}
			if err := in.Body.Validate(s.app.Scheduler.MinIntervalMinutes(in.Name)); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, taskError(s.app.Scheduler.SetSchedule(ctx, in.Name, in.Body))
		})
	huma.Register(s.api, huma.Operation{OperationID: "system-tasks-reset", Method: http.MethodDelete, Path: "/api/v1/system/tasks/{name}/schedule", Tags: tags},
		func(ctx context.Context, in *taskPath) (*struct{}, error) {
			return nil, taskError(s.app.Scheduler.ResetSchedule(ctx, in.Name))
		})
	for _, action := range []string{"pause", "resume"} {
		huma.Register(s.api, huma.Operation{OperationID: "system-tasks-" + action, Method: http.MethodPost, Path: "/api/v1/system/tasks/{name}/" + action, Tags: tags},
			func(ctx context.Context, in *taskPath) (*struct{}, error) {
				return nil, taskError(s.app.Scheduler.SetPaused(ctx, in.Name, action == "pause"))
			})
		huma.Register(s.api, huma.Operation{OperationID: "system-tasks-" + action + "-all", Method: http.MethodPost, Path: "/api/v1/system/tasks/" + action + "-all", Tags: tags},
			func(ctx context.Context, _ *struct{}) (*struct{}, error) {
				return nil, taskError(s.app.Scheduler.SetPausedAll(ctx, action == "pause"))
			})
	}
	huma.Register(s.api, huma.Operation{OperationID: "system-tasks-preview", Method: http.MethodPost, Path: "/api/v1/system/tasks/preview", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				jobs.Schedule
				Name string `json:"name,omitempty"`
			}
		}) (*struct{ Body TaskPreview }, error) {
			minimum := 1
			if in.Body.Name != "" {
				if _, err := s.app.Scheduler.Get(ctx, in.Body.Name); err != nil {
					return nil, taskError(err)
				}
				minimum = s.app.Scheduler.MinIntervalMinutes(in.Body.Name)
			}
			if err := in.Body.Schedule.Validate(minimum); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			runs, err := s.app.Scheduler.Preview(ctx, in.Body.Schedule, 3, in.Body.Name)
			if err != nil {
				return nil, toHTTPError(err)
			}
			loc, err := s.app.Scheduler.Location(ctx)
			return &struct{ Body TaskPreview }{TaskPreview{NextRuns: runs, Timezone: quiet.ZoneName(loc)}}, toHTTPError(err)
		})
}

// Reader sync keeps its existing settings route, with the task as the source
// of truth. Saving an interval replaces any daily schedule.
func (s *Server) registerReadSyncSettings() {
	tags := []string{"Settings"}
	huma.Register(s.api, huma.Operation{OperationID: "settings-get-readsync", Method: http.MethodGet, Path: "/api/v1/settings/readsync", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body settings.ReadSync }, error) {
			v, err := s.app.Settings.ReadSync(ctx)
			return &struct{ Body settings.ReadSync }{v}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-put-readsync", Method: http.MethodPut, Path: "/api/v1/settings/readsync", Tags: tags},
		func(ctx context.Context, in *struct{ Body settings.ReadSync }) (*struct{ Body settings.ReadSync }, error) {
			v := jobs.Schedule{Kind: "interval", IntervalMinutes: in.Body.IntervalMinutes}
			if err := v.Validate(s.app.Scheduler.MinIntervalMinutes("SyncReadProgress")); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if err := s.app.Scheduler.SetSchedule(ctx, "SyncReadProgress", v); err != nil {
				return nil, taskError(err)
			}
			effective, err := s.app.Settings.ReadSync(ctx)
			s.app.Bus.Changed("settings", "updated", 0)
			return &struct{ Body settings.ReadSync }{effective}, toHTTPError(err)
		})
}
