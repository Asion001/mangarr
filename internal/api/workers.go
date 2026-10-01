package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/version"
	"github.com/Asion001/mangarr/internal/workerupdate"
)

func init() { register((*Server).registerWorkers) }

// WorkerResource is a worker as the UI sees it, with what it is doing now
// and what it has done lately.
type WorkerResource struct {
	model.Worker
	// Online: the worker has been here recently.
	Online bool `json:"online"`
	// Busy is what it holds right now.
	Busy []WorkerBusy `json:"busy,omitempty"`
	// Recent is what it did in the last day.
	Recent WorkerRecent `json:"recent"`
	// UpdateTo is this server's version when the worker runs an older
	// release.
	UpdateTo string `json:"updateTo,omitempty"`
}

// WorkerBusy is one task a worker is doing now.
type WorkerBusy struct {
	TaskID     int64     `json:"taskId"`
	Kind       string    `json:"kind"`
	Series     string    `json:"series,omitempty"`
	Chapter    string    `json:"chapter,omitempty"`
	PagesDone  int       `json:"pagesDone"`
	PagesTotal int       `json:"pagesTotal"`
	BytesIn    int64     `json:"bytesIn"`
	Started    time.Time `json:"started"`
}

// WorkerRecent is what a worker did in the last 24 hours.
type WorkerRecent struct {
	Tasks    int   `json:"tasks"`
	Failed   int   `json:"failed"`
	Pages    int   `json:"pages"`
	BytesIn  int64 `json:"bytesIn"`
	BytesOut int64 `json:"bytesOut"`
	// Seconds is how long it was busy, so the UI can show a rate.
	Seconds float64 `json:"seconds"`
	// GPUs are the devices its finished upscale batches ran on.
	GPUs []string `json:"gpus,omitempty"`
}

// NewWorkerOutput carries the key, which is shown once and never again.
type NewWorkerOutput struct {
	Worker model.Worker `json:"worker"`
	// Key is what the worker container is configured with.
	Key string `json:"key"`
}

// onlineWithin is how long after its last word a worker still counts as
// online (it polls far more often than this).
const onlineWithin = 2 * time.Minute

func (s *Server) registerWorkers() {
	tags := []string{"Workers"}

	huma.Register(s.api, huma.Operation{OperationID: "workers-list", Method: http.MethodGet, Path: "/api/v1/workers", Tags: tags,
		Summary: "List the machines that do work for this server"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []WorkerResource }, error) {
			var list []model.Worker
			if err := s.app.DB.NewSelect().Model(&list).Order("name").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			busy, recent := s.workerWork(ctx)
			out := make([]WorkerResource, 0, len(list))
			for _, w := range list {
				r := WorkerResource{Worker: w, Online: online(w), Busy: busy[w.ID], Recent: recent[w.ID]}
				if workerupdate.Newer(version.Version, w.Version) {
					r.UpdateTo = version.Version
				}
				out = append(out, r)
			}
			return &struct{ Body []WorkerResource }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "workers-create", Method: http.MethodPost, Path: "/api/v1/workers", Tags: tags,
		DefaultStatus: http.StatusCreated, Summary: "Add a worker and issue its key"},
		func(ctx context.Context, in *struct {
			Body struct {
				Name  string   `json:"name" minLength:"1"`
				Roles []string `json:"roles"`
			}
		}) (*struct{ Body NewWorkerOutput }, error) {
			by := int64(0)
			if p := access.From(ctx); p != nil {
				by = p.UserID
			}
			key, w, err := s.app.Auth.CreateWorker(ctx, in.Body.Name, in.Body.Roles, by)
			if err != nil {
				if errors.Is(err, auth.ErrWorkerExists) {
					return nil, huma.Error409Conflict(err.Error())
				}
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return &struct{ Body NewWorkerOutput }{NewWorkerOutput{Worker: *w, Key: key}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "workers-update", Method: http.MethodPut, Path: "/api/v1/workers/{id}", Tags: tags,
		Summary: "Rename a worker, change its roles, model, priority or limits, or switch it off"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Name       *string   `json:"name,omitempty"`
				Roles      *[]string `json:"roles,omitempty"`
				Enabled    *bool     `json:"enabled,omitempty"`
				Priority   *int      `json:"priority,omitempty"`
				Concurrent *int      `json:"concurrent,omitempty" minimum:"0"`
				// PageConcurrency is pages fetched at a time per download (0 leaves it to the worker).
				PageConcurrency *int `json:"pageConcurrency,omitempty" minimum:"0" maximum:"64"`
				// UpscaleModel replaces the profile's model on this worker ("" uses the profile's).
				UpscaleModel *string `json:"upscaleModel,omitempty"`
			}
		}) (*struct{ Body model.Worker }, error) {
			var w model.Worker
			if err := s.app.DB.NewSelect().Model(&w).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("no such worker")
			}
			if in.Body.Name != nil {
				name := strings.TrimSpace(*in.Body.Name)
				if name == "" {
					return nil, huma.Error422UnprocessableEntity("a worker needs a name")
				}
				if n, _ := s.app.DB.NewSelect().Model((*model.Worker)(nil)).Where("name = ? AND id <> ?", name, in.ID).Count(ctx); n > 0 {
					return nil, huma.Error409Conflict(auth.ErrWorkerExists.Error())
				}
				w.Name = name
			}
			if in.Body.Roles != nil {
				kept := []string{}
				for _, r := range model.WorkerRoles {
					for _, want := range *in.Body.Roles {
						if r == want {
							kept = append(kept, r)
						}
					}
				}
				if len(kept) == 0 {
					return nil, huma.Error422UnprocessableEntity("a worker needs at least one role")
				}
				w.Roles = kept
			}
			if in.Body.Enabled != nil {
				w.Enabled = *in.Body.Enabled
			}
			if in.Body.Priority != nil {
				w.Priority = *in.Body.Priority
			}
			if in.Body.Concurrent != nil {
				w.Concurrent = *in.Body.Concurrent
			}
			if in.Body.PageConcurrency != nil {
				w.PageConcurrency = *in.Body.PageConcurrency
			}
			if in.Body.UpscaleModel != nil {
				w.UpscaleModel = strings.TrimSpace(*in.Body.UpscaleModel)
			}
			if _, err := s.app.DB.NewUpdate().Model(&w).Column("name", "roles", "enabled", "priority", "concurrent", "page_concurrency", "upscale_model").WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Auth.InvalidateWorkers()
			return &struct{ Body model.Worker }{w}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "workers-delete", Method: http.MethodDelete, Path: "/api/v1/workers/{id}", Tags: tags,
		DefaultStatus: http.StatusNoContent, Summary: "Remove a worker and its key"},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*struct{}, error) {
			res, err := s.app.DB.NewDelete().Model((*model.Worker)(nil)).Where("id = ?", in.ID).Exec(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return nil, huma.Error404NotFound("no such worker")
			}
			s.app.Auth.InvalidateWorkers()
			return &struct{}{}, nil
		})
}

func online(w model.Worker) bool {
	return w.Enabled && w.LastSeenAt != nil && time.Since(*w.LastSeenAt) < onlineWithin
}

// workerWork reads what each worker holds now and what it did in the last
// day, from the task ledger.
func (s *Server) workerWork(ctx context.Context) (map[int64][]WorkerBusy, map[int64]WorkerRecent) {
	busy := map[int64][]WorkerBusy{}
	recent := map[int64]WorkerRecent{}

	var live []struct {
		model.WorkerTask
		Series  string `bun:"series"`
		Chapter string `bun:"chapter"`
	}
	err := s.app.DB.NewSelect().TableExpr("worker_tasks AS t").
		ColumnExpr("t.*, COALESCE(s.title, '') AS series, COALESCE(c.number_key, '') AS chapter").
		Join("LEFT JOIN download_jobs AS j ON j.id = t.job_id").
		Join("LEFT JOIN series AS s ON s.id = j.series_id").
		Join("LEFT JOIN chapters AS c ON c.id = j.chapter_id").
		Where("t.state = ?", model.TaskLeased).Scan(ctx, &live)
	if err == nil {
		for _, t := range live {
			started := t.CreatedAt
			if t.StartedAt != nil {
				started = *t.StartedAt
			}
			busy[t.WorkerID] = append(busy[t.WorkerID], WorkerBusy{TaskID: t.ID, Kind: t.Kind, Series: t.Series, Chapter: t.Chapter,
				PagesDone: t.PagesDone, PagesTotal: t.PagesTotal, BytesIn: t.BytesIn, Started: started})
		}
	}

	var sums []struct {
		WorkerID int64   `bun:"worker_id"`
		Tasks    int     `bun:"tasks"`
		Failed   int     `bun:"failed"`
		Pages    int     `bun:"pages"`
		BytesIn  int64   `bun:"bytes_in"`
		BytesOut int64   `bun:"bytes_out"`
		Seconds  float64 `bun:"seconds"`
	}
	since := time.Now().UTC().Add(-24 * time.Hour)
	err = s.app.DB.NewSelect().TableExpr("worker_tasks").
		ColumnExpr("worker_id, COUNT(*) AS tasks").
		ColumnExpr("SUM(CASE WHEN state = ? THEN 1 ELSE 0 END) AS failed", model.TaskFailed).
		ColumnExpr("SUM(pages_done) AS pages, SUM(bytes_in) AS bytes_in, SUM(bytes_out) AS bytes_out").
		ColumnExpr("SUM(CASE WHEN started_at IS NOT NULL THEN ? ELSE 0 END) AS seconds", bun.Safe(elapsedSeconds(s.app.DB.Dialect().Name().String()))).
		Where("finished_at IS NOT NULL AND finished_at > ? AND worker_id IS NOT NULL", since).
		GroupExpr("worker_id").Scan(ctx, &sums)
	if err == nil {
		for _, r := range sums {
			recent[r.WorkerID] = WorkerRecent{Tasks: r.Tasks, Failed: r.Failed, Pages: r.Pages, BytesIn: r.BytesIn,
				BytesOut: r.BytesOut, Seconds: r.Seconds}
		}
	}
	var finished []model.WorkerTask
	if err := s.app.DB.NewSelect().Model(&finished).Column("worker_id", "spec").Where("finished_at IS NOT NULL AND finished_at > ? AND worker_id IS NOT NULL AND kind = ?", since, model.TaskUpscale).Scan(ctx); err == nil {
		seen := map[int64]map[string]bool{}
		for _, task := range finished {
			gpu := taskGPU(task.Spec)
			if gpu == "" {
				continue
			}
			if seen[task.WorkerID] == nil {
				seen[task.WorkerID] = map[string]bool{}
			}
			if !seen[task.WorkerID][gpu] {
				recent[task.WorkerID] = appendGPU(recent[task.WorkerID], gpu)
				seen[task.WorkerID][gpu] = true
			}
		}
	}
	return busy, recent
}

func taskGPU(spec map[string]any) string {
	if gpu, ok := spec["gpu"].(string); ok {
		return gpu
	}
	return ""
}

func appendGPU(recent WorkerRecent, gpu string) WorkerRecent {
	recent.GPUs = append(recent.GPUs, gpu)
	return recent
}

// elapsedSeconds is how long a finished task took, in the dialect's own way.
func elapsedSeconds(dialect string) string {
	if dialect == "pg" {
		return "EXTRACT(EPOCH FROM (finished_at - started_at))"
	}
	return "(julianday(finished_at) - julianday(started_at)) * 86400"
}
