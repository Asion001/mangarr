package api

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/processing"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/worktasks"
)

func init() { register((*Server).registerWorkerProtocol) }

// The worker protocol. A worker dials in, says hello, and then asks for one
// task at a time; the server never connects to it. Every task-scoped call
// checks that this worker still holds the task and answers 409 otherwise,
// which the worker treats as "drop it and ask again".

// WorkerHello is what a worker says about itself when it starts.
type WorkerHello struct {
	Version  string `json:"version,omitempty"`
	Platform string `json:"platform,omitempty"`
	// Roles it is able to do (the server answers with the ones it may).
	Roles []string `json:"roles,omitempty"`
	// Info is anything else worth showing: its upscaling devices, its cores.
	Info map[string]any `json:"info,omitempty"`
}

// WorkerWelcome is what the worker is told in return.
type WorkerWelcome struct {
	WorkerID int64  `json:"workerId"`
	Name     string `json:"name"`
	// Roles are the ones it may actually do here.
	Roles []string `json:"roles"`
	// LeaseSeconds is how long a task is held between heartbeats.
	LeaseSeconds int `json:"leaseSeconds"`
	// PollSeconds is how long a lease request waits before answering empty.
	PollSeconds int `json:"pollSeconds"`
	// Prefetch is how many pages to fetch ahead of what has been uploaded.
	Prefetch int `json:"prefetch"`
	// Concurrent is how many tasks it may hold at once.
	Concurrent int `json:"concurrent"`
	// PageConcurrency is how many pages it fetches at a time (0: its own
	// setting).
	PageConcurrency int `json:"pageConcurrency"`
	// OutputChunkBytes keeps processing-result uploads below proxy limits.
	OutputChunkBytes int    `json:"outputChunkBytes"`
	ServerTime       string `json:"serverTime"`
}

// WorkerTaskOutput is one task, as handed to a worker, with its limits as
// they are now: they can be changed in System → Workers while it runs.
type WorkerTaskOutput struct {
	Task *model.WorkerTask `json:"task,omitempty"`
	// Label names the task's chapter ("Series · Ch. 12") for the worker's
	// own status page.
	Label           string `json:"label,omitempty"`
	Concurrent      int    `json:"concurrent"`
	PageConcurrency int    `json:"pageConcurrency"`
}

// taskLabel names a job's chapter for a worker's status page ("" when the
// job is gone).
func (s *Server) taskLabel(ctx context.Context, jobID int64) string {
	var row struct {
		Title  string `bun:"title"`
		Number string `bun:"number_key"`
	}
	err := s.app.DB.NewSelect().TableExpr("download_jobs AS j").ColumnExpr("s.title, c.number_key").
		Join("JOIN series AS s ON s.id = j.series_id").Join("JOIN chapters AS c ON c.id = j.chapter_id").
		Where("j.id = ?", jobID).Scan(ctx, &row)
	if err != nil {
		return ""
	}
	return row.Title + " · Ch. " + row.Number
}

// workerConcurrent is how many tasks a worker may hold at once: its own
// limit, or the installation default.
func workerConcurrent(w *model.Worker, dl settings.Downloads) int {
	if w.Concurrent > 0 {
		return w.Concurrent
	}
	return max(dl.MaxConcurrentPerWorker, 1)
}

// workerPoll is how long a lease request waits for work before answering
// empty. Well under any sensible proxy timeout.
const workerPoll = 25 * time.Second

// pollEvery is how often the server looks for work while a worker waits.
const pollEvery = time.Second

func (s *Server) registerWorkerProtocol() {
	tags := []string{"Workers"}

	huma.Register(s.api, huma.Operation{OperationID: "worker-hello", Method: http.MethodPost, Path: "/api/v1/worker/hello", Tags: tags,
		Summary: "Announce a worker and learn what it may do"},
		func(ctx context.Context, in *struct{ Body WorkerHello }) (*struct{ Body WorkerWelcome }, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			info := in.Body.Info
			if info == nil {
				info = map[string]any{}
			}
			if len(in.Body.Roles) > 0 {
				// what it was set up to do, so work it never asks for is
				// not written for it
				info[model.InfoRoles] = in.Body.Roles
			}
			now := time.Now().UTC()
			w.Version, w.Platform, w.Info, w.LastSeenAt = in.Body.Version, in.Body.Platform, info, &now
			if _, err := s.app.DB.NewUpdate().Model(w).Column("version", "platform", "info", "last_seen_at").WherePK().Exec(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Auth.InvalidateWorkers()
			if w.HasRole(model.RoleUpscale) {
				// a GPU box only has to be given a key: the module that hands
				// batches to the workers appears with the first one
				s.app.OfferWorkersUpscaler(ctx)
			}
			dl, _ := s.app.Settings.Downloads(ctx)
			welcome := WorkerWelcome{WorkerID: w.ID, Name: w.Name, Roles: allowedRoles(w, in.Body.Roles),
				LeaseSeconds: int(worktasks.Lease / time.Second), PollSeconds: int(workerPoll / time.Second),
				Prefetch: dl.WorkerPrefetch, Concurrent: workerConcurrent(w, dl), PageConcurrency: w.PageConcurrency,
				OutputChunkBytes: workerOutputChunkBytes,
				ServerTime:       now.Format(time.RFC3339)}
			return &struct{ Body WorkerWelcome }{welcome}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-lease", Method: http.MethodPost, Path: "/api/v1/worker/lease", Tags: tags,
		Summary: "Ask for a task (waits a while when there is none)"},
		func(ctx context.Context, in *struct {
			Body struct {
				// Kinds it is ready to take right now (a busy worker asks for
				// less than it can do).
				Kinds []string `json:"kinds"`
			}
		}) (*struct{ Body WorkerTaskOutput }, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			kinds := allowedRoles(w, in.Body.Kinds)
			dl, _ := s.app.Settings.Downloads(ctx)
			out := WorkerTaskOutput{Concurrent: workerConcurrent(w, dl), PageConcurrency: w.PageConcurrency}
			deadline := time.Now().Add(workerPoll)
			for {
				task, err := s.app.Tasks.Claim(ctx, w.ID, kinds, dl.MaxWorkerTasks, dl.MaxConcurrentPerWorker)
				if err != nil {
					return nil, toHTTPError(err)
				}
				if task != nil {
					if err := s.app.Tasks.UseWorkerModel(ctx, task, w); err != nil {
						s.app.Log.Warn("could not give the task this worker's model", "task", task.ID, "worker", w.Name, "err", err)
					}
					if task.Kind == model.TaskEncode && w.UpscaleModel != "" {
						task.Spec["upscaleModel"] = w.UpscaleModel // this worker's own, not stored
					}
					out.Task, out.Label = task, s.taskLabel(ctx, task.JobID)
					return &struct{ Body WorkerTaskOutput }{out}, nil
				}
				if time.Now().After(deadline) {
					return &struct{ Body WorkerTaskOutput }{out}, nil
				}
				select {
				case <-ctx.Done():
					return &struct{ Body WorkerTaskOutput }{out}, nil
				case <-time.After(pollEvery):
				}
			}
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-page", Method: http.MethodPut, Path: "/api/v1/worker/tasks/{id}/pages/{n}", Tags: tags,
		Summary: "Upload one finished page of a download task", DefaultStatus: http.StatusNoContent,
		MaxBodyBytes: 256 << 20},
		func(ctx context.Context, in *struct {
			ID      int64  `path:"id"`
			N       int    `path:"n" minimum:"1"`
			RawBody []byte `contentType:"application/octet-stream"`
		}) (*struct{}, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			task, err := s.app.Tasks.Held(ctx, in.ID, w.ID)
			if err != nil {
				return nil, huma.Error409Conflict("this task is not yours any more")
			}
			if _, err := s.app.Downloads.AcceptPage(ctx, task.JobID, in.N-1, in.RawBody); err != nil {
				return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("page %d: %v", in.N, err))
			}
			return &struct{}{}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-input", Method: http.MethodGet, Path: "/api/v1/worker/tasks/{id}/input", Tags: tags,
		Summary: "Download what a processing task works on"},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
		}) (*huma.StreamResponse, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			task, err := s.app.Tasks.Held(ctx, in.ID, w.ID)
			if err != nil {
				return nil, huma.Error409Conflict("this task is not yours any more")
			}
			if task.Kind == model.TaskEncode {
				return s.workerProcessInput(task)
			}
			path, _ := task.Spec["input"].(string)
			f, err := os.Open(path)
			if err != nil {
				return nil, huma.Error404NotFound("this task's pages are gone")
			}
			st, _ := f.Stat()
			return &huma.StreamResponse{Body: func(hctx huma.Context) {
				defer f.Close()
				hctx.SetHeader("Content-Type", "application/zip")
				if st != nil {
					hctx.SetHeader("Content-Length", strconv.FormatInt(st.Size(), 10))
				}
				_, _ = io.Copy(hctx.BodyWriter(), f)
			}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-output", Method: http.MethodPost, Path: "/api/v1/worker/tasks/{id}/output", Tags: tags,
		Summary: "Upload what a processing task produced", DefaultStatus: http.StatusNoContent, MaxBodyBytes: 1 << 30},
		func(ctx context.Context, in *struct {
			ID      int64  `path:"id"`
			Chunk   int    `header:"X-Mangarr-Chunk"`
			Chunks  int    `header:"X-Mangarr-Chunks"`
			RawBody []byte `contentType:"application/zip"`
		}) (*struct{}, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			task, err := s.app.Tasks.Held(ctx, in.ID, w.ID)
			if err != nil {
				return nil, huma.Error409Conflict("this task is not yours any more")
			}
			path, _ := task.Spec["output"].(string)
			if path == "" {
				return nil, huma.Error422UnprocessableEntity("this task takes no output")
			}
			if !validWorkerOutputChunk(in.Chunk, in.Chunks) {
				return nil, huma.Error422UnprocessableEntity("invalid output chunk headers")
			}
			if err := storeWorkerOutput(path, in.Chunk, in.Chunks, in.RawBody); err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{}{}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-heartbeat", Method: http.MethodPost, Path: "/api/v1/worker/tasks/{id}/heartbeat", Tags: tags,
		Summary: "Report progress and keep the task"},
		func(ctx context.Context, in *struct {
			ID int64 `path:"id"`
			// Every field is optional: a heartbeat's job is to keep the lease,
			// and rejecting one for a missing counter loses the task.
			Body struct {
				PagesDone  int   `json:"pagesDone,omitempty"`
				PagesTotal int   `json:"pagesTotal,omitempty"`
				BytesIn    int64 `json:"bytesIn,omitempty"`
				BytesOut   int64 `json:"bytesOut,omitempty"`
				// Stage is the processing stage the pages count (upscale,
				// split or encode), for a processing task.
				Stage string `json:"stage,omitempty"`
			}
		}) (*struct {
			Body struct {
				// Cancel: stop and hand the task back.
				Cancel bool `json:"cancel"`
			}
		}, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			p := worktasks.Progress{PagesDone: in.Body.PagesDone, PagesTotal: in.Body.PagesTotal, BytesIn: in.Body.BytesIn, BytesOut: in.Body.BytesOut}
			cancel, err := s.app.Tasks.Heartbeat(ctx, in.ID, w.ID, p)
			if err != nil {
				return nil, workerConflict(err)
			}
			if task, err := s.app.Tasks.Held(ctx, in.ID, w.ID); err == nil {
				switch task.Kind {
				case model.TaskDownload:
					s.app.Downloads.TaskProgress(*task, in.Body.PagesDone, in.Body.PagesTotal, in.Body.BytesIn)
				case model.TaskEncode:
					s.app.Downloads.ProcessingProgress(*task, in.Body.Stage, in.Body.PagesDone, in.Body.PagesTotal)
				}
			}
			out := &struct {
				Body struct {
					Cancel bool `json:"cancel"`
				}
			}{}
			out.Body.Cancel = cancel
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-complete", Method: http.MethodPost, Path: "/api/v1/worker/tasks/{id}/complete", Tags: tags,
		Summary: "Say a task is done", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Pages    int    `json:"pages"`
				BytesIn  int64  `json:"bytesIn"`
				BytesOut int64  `json:"bytesOut"`
				GPU      string `json:"gpu,omitempty"`
			}
		}) (*struct{}, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			task, err := s.app.Tasks.Held(ctx, in.ID, w.ID)
			if err != nil {
				return nil, huma.Error409Conflict("this task is not yours any more")
			}
			p := worktasks.Progress{PagesDone: in.Body.Pages, PagesTotal: task.PagesTotal, BytesIn: in.Body.BytesIn, BytesOut: in.Body.BytesOut, GPU: in.Body.GPU}
			if err := s.app.Tasks.Finish(ctx, in.ID, w.ID, p); err != nil {
				return nil, workerConflict(err)
			}
			// A chapter's pages are now all here, so the rest of the download
			// (processing, import) runs on this machine — and takes as long as
			// it takes: the worker is free as soon as its pages are in. Other
			// kinds of task are awaited by whoever asked for them.
			if task.Kind == model.TaskDownload {
				go func() {
					bg := context.WithoutCancel(ctx)
					if err := s.app.Downloads.TaskDone(bg, *task); err != nil {
						s.app.Log.Warn("could not finish a chapter a worker downloaded", "task", task.ID, "job", task.JobID, "err", err)
					}
				}()
			}
			return &struct{}{}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-fail", Method: http.MethodPost, Path: "/api/v1/worker/tasks/{id}/fail", Tags: tags,
		Summary: "Say a task could not be done", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				Reason string `json:"reason"`
				Pages  int    `json:"pages,omitempty"`
			}
		}) (*struct{}, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			task, err := s.app.Tasks.Held(ctx, in.ID, w.ID)
			if err != nil {
				return nil, huma.Error409Conflict("this task is not yours any more")
			}
			reason := in.Body.Reason
			if reason == "" {
				reason = "the worker gave no reason"
			}
			if err := s.app.Tasks.Fail(ctx, in.ID, w.ID, reason, worktasks.Progress{PagesDone: in.Body.Pages, PagesTotal: task.PagesTotal}); err != nil {
				return nil, workerConflict(err)
			}
			if task.Kind == model.TaskDownload {
				s.app.Downloads.TaskFailed(context.WithoutCancel(ctx), *task, reason)
			}
			return &struct{}{}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "worker-bye", Method: http.MethodPost, Path: "/api/v1/worker/bye", Tags: tags,
		Summary: "Hand back everything this worker holds", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *struct {
			Body struct {
				// TaskIDs it is handing back (empty: everything it holds).
				TaskIDs []int64 `json:"taskIds,omitempty"`
			}
		}) (*struct{}, error) {
			w, err := s.worker(ctx)
			if err != nil {
				return nil, err
			}
			n, err := s.app.Tasks.HandBack(ctx, w.ID, in.Body.TaskIDs)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if n > 0 {
				s.app.Log.Info("a worker handed its tasks back", "worker", w.Name, "tasks", n)
			}
			return &struct{}{}, nil
		})
}

const workerOutputChunkBytes = 16 << 20

func validWorkerOutputChunk(chunk, chunks int) bool {
	if chunk == 0 && chunks == 0 { // workers before chunked uploads
		return true
	}
	return chunks > 0 && chunks <= 10_000 && chunk > 0 && chunk <= chunks
}

func storeWorkerOutput(path string, chunk, chunks int, data []byte) error {
	if chunks <= 1 {
		tmp := path + ".part"
		if err := os.WriteFile(tmp, data, 0o664); err != nil {
			return err
		}
		return os.Rename(tmp, path)
	}
	part := fmt.Sprintf("%s.part.%06d", path, chunk)
	if err := os.WriteFile(part, data, 0o664); err != nil {
		return err
	}
	if chunk != chunks {
		return nil
	}
	tmp := path + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		_ = out.Close()
		if !complete {
			_ = os.Remove(tmp)
		}
	}()
	for i := 1; i <= chunks; i++ {
		name := fmt.Sprintf("%s.part.%06d", path, i)
		in, err := os.Open(name)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := in.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	complete = true
	for i := 1; i <= chunks; i++ {
		_ = os.Remove(fmt.Sprintf("%s.part.%06d", path, i))
	}
	return nil
}

// worker is the worker a request is from.
func (s *Server) worker(ctx context.Context) (*model.Worker, error) {
	p := access.From(ctx)
	if p == nil || p.Kind != access.KindWorker || p.WorkerID == 0 {
		return nil, huma.Error401Unauthorized("a worker key is required")
	}
	var w model.Worker
	if err := s.app.DB.NewSelect().Model(&w).Where("id = ?", p.WorkerID).Scan(ctx); err != nil {
		return nil, huma.Error401Unauthorized("this worker is gone")
	}
	if !w.Enabled {
		return nil, huma.Error403Forbidden("this worker is switched off")
	}
	return &w, nil
}

// allowedRoles is what a worker asked for, limited to what it may do.
func allowedRoles(w *model.Worker, asked []string) []string {
	out := []string{}
	for _, r := range model.WorkerRoles {
		if !w.HasRole(r) {
			continue
		}
		if len(asked) > 0 && !contains(asked, r) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// workerConflict turns "not yours any more" into the 409 a worker knows how
// to handle.
func workerConflict(err error) error {
	if errors.Is(err, worktasks.ErrNotYours) {
		return huma.Error409Conflict("this task is not yours any more")
	}
	return toHTTPError(err)
}

// workerProcessInput streams a processing task's pages as a zip, entry i
// being page i, for a worker that can't read the job's folder itself.
func (s *Server) workerProcessInput(task *model.WorkerTask) (*huma.StreamResponse, error) {
	spec, err := processing.ParseSpec(task.Spec)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	for _, p := range spec.Pages {
		if _, err := os.Stat(p.Path); err != nil {
			return nil, huma.Error404NotFound("this task's pages are gone")
		}
	}
	return &huma.StreamResponse{Body: func(hctx huma.Context) {
		hctx.SetHeader("Content-Type", "application/zip")
		zw := zip.NewWriter(hctx.BodyWriter())
		for i, p := range spec.Pages {
			if err := zipFile(zw, processing.InputName(i, p), p.Path); err != nil {
				s.app.Log.Warn("could not send a page to a worker", "task", task.ID, "page", p.Name, "err", err)
				return
			}
		}
		_ = zw.Close()
	}}, nil
}

func zipFile(zw *zip.Writer, name, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}
