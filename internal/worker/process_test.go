package worker

import (
	"archive/zip"
	"context"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/processing"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// TestProcessOnWorker runs the processing stage the way MANGARR_PROCESSING=
// workers does: the server's Remote writes a task, a worker takes it and
// processes the pages, and the server imports what came back — once with
// the worker on the server's storage, once over HTTP.
func TestProcessOnWorker(t *testing.T) {
	for _, shared := range []bool{true, false} {
		name := "http"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			d := dbtest.SQLite(t)
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			tasks := worktasks.New(d, log)
			jobID := seedJob(t, d)
			workerID := seedProcessWorker(t, d)

			workDir := t.TempDir()
			pages := []downloads.PageFile{
				writePNG(t, workDir, "0001.png", 1000, 1500), // too wide: shrunk
				writePNG(t, workDir, "0002.png", 400, 600),   // left alone
			}
			cfg := model.ProfileConfig{Pages: model.PageRules{MaxWidth: 500}}

			// the "server": the input and output endpoints a worker without
			// shared storage uses
			srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				task, err := tasks.Held(r.Context(), taskIDFrom(r.URL.Path), workerID)
				if err != nil {
					http.Error(rw, err.Error(), http.StatusConflict)
					return
				}
				spec, _ := processing.ParseSpec(task.Spec)
				switch {
				case strings.HasSuffix(r.URL.Path, "/input"):
					zw := zip.NewWriter(rw)
					for i, p := range spec.Pages {
						f, _ := zw.Create(processing.InputName(i, p))
						data, _ := os.ReadFile(p.Path)
						_, _ = f.Write(data)
					}
					_ = zw.Close()
				case strings.HasSuffix(r.URL.Path, "/output"):
					data, _ := io.ReadAll(r.Body)
					_ = os.WriteFile(spec.Output, data, 0o664)
				default:
					rw.WriteHeader(http.StatusNoContent)
				}
			}))
			defer srv.Close()
			w, err := New(Config{ServerURL: srv.URL, Key: "mgw_test", SharedStorage: shared, Log: log})
			if err != nil {
				t.Fatal(err)
			}
			w.welcome.OutputChunkBytes = 1 << 20

			go func() {
				for ctx.Err() == nil {
					task, err := tasks.Claim(ctx, workerID, []string{model.TaskEncode})
					if err != nil || task == nil {
						time.Sleep(20 * time.Millisecond)
						continue
					}
					res, err := w.process(ctx, Task{ID: task.ID, JobID: task.JobID, Kind: task.Kind, Spec: task.Spec})
					if err != nil {
						_ = tasks.Fail(ctx, task.ID, workerID, err.Error(), worktasks.Progress{})
						return
					}
					_ = tasks.Finish(ctx, task.ID, workerID, worktasks.Progress{PagesDone: res.Pages})
					return
				}
			}()

			remote := &processing.Remote{Tasks: tasks}
			res, err := remote.Process(worktasks.WithJob(ctx, jobID), cfg, pages, workDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Pages) != 2 || res.Shrunk != 1 || !res.Changed {
				t.Fatalf("result: %+v", res)
			}
			if res.Pages[0].Width != 500 || res.Pages[0].Path == pages[0].Path || !strings.HasPrefix(res.Pages[0].Path, workDir) {
				t.Fatalf("shrunk page: %+v", res.Pages[0])
			}
			if _, err := os.Stat(res.Pages[0].Path); err != nil {
				t.Fatal(err)
			}
			if res.Pages[1] != pages[1] {
				t.Fatalf("untouched page came back as %+v", res.Pages[1])
			}
		})
	}
}

func taskIDFrom(path string) int64 {
	var id int64
	for _, part := range strings.Split(path, "/") {
		n := int64(0)
		ok := part != ""
		for _, c := range part {
			if c < '0' || c > '9' {
				ok = false
				break
			}
			n = n*10 + int64(c-'0')
		}
		if ok {
			id = n
		}
	}
	return id
}

func TestSharedStorageNeedsTheFiles(t *testing.T) {
	w := &Worker{cfg: Config{SharedStorage: true}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	dir := t.TempDir()
	page := writePNG(t, dir, "0001.png", 10, 10)
	spec := processing.TaskSpec{OutDir: dir, Pages: []processing.TaskPage{{Name: page.Name, Path: page.Path}}}
	if !w.sees(spec) {
		t.Fatal("the files are here")
	}
	spec.Pages[0].Path = filepath.Join(dir, "elsewhere.png")
	if w.sees(spec) {
		t.Fatal("a page it can't see means HTTP")
	}
	w.cfg.SharedStorage = false
	spec.Pages[0].Path = page.Path
	if w.sees(spec) {
		t.Fatal("without the setting it always uses HTTP")
	}
}

func TestLoadConfigSharedStorage(t *testing.T) {
	env := map[string]string{"MANGARR_SERVER_URL": "http://mangarr:8787", "MANGARR_WORKER_KEY": "mgw_x", "MANGARR_WORKER_SHARED_STORAGE": "true"}
	c, err := LoadConfig(func(k string) string { return env[k] })
	if err != nil || !c.SharedStorage {
		t.Fatalf("shared storage: %v %v", c.SharedStorage, err)
	}
	env["MANGARR_WORKER_SHARED_STORAGE"] = "maybe"
	if _, err := LoadConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("a bad value must be an error")
	}
}

func writePNG(t *testing.T, dir, name string, w, h int) downloads.PageFile {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return downloads.PageFile{Name: name, Path: path, Format: "png", Width: w, Height: h}
}

func seedProcessWorker(t *testing.T, d *db.DB) int64 {
	t.Helper()
	now := time.Now().UTC()
	w := &model.Worker{Name: "box", KeyHash: "hash", Prefix: "mgw_box", Roles: []string{model.RoleEncode},
		Enabled: true, Info: map[string]any{worktasks.InfoProcess: true}, CreatedAt: now, LastSeenAt: &now}
	if _, err := d.NewInsert().Model(w).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w.ID
}

func seedJob(t *testing.T, d *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	profile := &model.Profile{Name: "test", CreatedAt: now, UpdatedAt: now}
	for _, m := range []any{root, profile} {
		if _, err := d.NewInsert().Model(m).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ser := &model.Series{Title: "Series", SortTitle: "series", RootFolderID: root.ID, ProfileID: profile.ID,
		Path: "Series", AddedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ch := &model.Chapter{SeriesID: ser.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterMissing,
		FirstSeenAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(ch).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	job := &model.DownloadJob{Kind: model.JobKindDownload, Status: model.JobQueued, SeriesID: ser.ID, ChapterID: ch.ID,
		NotBefore: now, CreatedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(job).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return job.ID
}

// TestRestartPicksUpProcessing: the server stops while a worker has a
// chapter's processing. The task stays with the worker, and when the server
// runs the job again it collects what the worker made instead of handing
// out the pages a second time.
func TestRestartPicksUpProcessing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	d := dbtest.SQLite(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	tasks := worktasks.New(d, log)
	jobID := seedJob(t, d)
	workerID := seedProcessWorker(t, d)
	workDir := t.TempDir()
	pages := []downloads.PageFile{writePNG(t, workDir, "0001.png", 1000, 1500)}
	cfg := model.ProfileConfig{Pages: model.PageRules{MaxWidth: 500}}

	// the server waits for a worker, then stops
	stopping, stop := context.WithCancel(worktasks.WithJob(ctx, jobID))
	go func() {
		for {
			if open, _ := tasks.OpenJobs(ctx); open[jobID] {
				stop()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	if _, err := (&processing.Remote{Tasks: tasks}).Process(stopping, cfg, pages, workDir); err == nil {
		t.Fatal("processing finished without a worker")
	}
	requeued := time.Now().UTC()

	// the worker carries on and finishes while the server is away
	task, err := tasks.Claim(ctx, workerID, []string{model.TaskEncode})
	if err != nil || task == nil || task.Cancel {
		t.Fatalf("the task didn't stay with the worker: %v %+v", err, task)
	}
	w, err := New(Config{ServerURL: "http://unused", Key: "mgw_test", SharedStorage: true, Log: log})
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.process(ctx, Task{ID: task.ID, JobID: task.JobID, Kind: task.Kind, Spec: task.Spec})
	if err != nil {
		t.Fatal(err)
	}
	if err := tasks.Finish(ctx, task.ID, workerID, worktasks.Progress{PagesDone: res.Pages}); err != nil {
		t.Fatal(err)
	}

	// the server is back and runs the job again
	orphan, err := tasks.Orphan(ctx, jobID, requeued)
	if err != nil || orphan == nil || orphan.ID != task.ID {
		t.Fatalf("the finished task wasn't found: %v %+v", err, orphan)
	}
	out, err := (&processing.Remote{Tasks: tasks}).Process(worktasks.WithAdopted(worktasks.WithJob(ctx, jobID), orphan), cfg, pages, workDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 1 || out.Pages[0].Width != 500 {
		t.Fatalf("result: %+v", out)
	}
	all, _ := tasks.OfJob(ctx, jobID)
	if len(all) != 1 {
		t.Fatalf("the pages went out again: %d tasks", len(all))
	}
	if again, _ := tasks.Orphan(ctx, jobID, time.Now().UTC()); again != nil {
		t.Fatalf("a task collected before the job ran again counts as an orphan: %+v", again)
	}
}
