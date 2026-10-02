package api_test

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/processing"
	"github.com/Asion001/mangarr/internal/progress"
	"github.com/Asion001/mangarr/internal/worker"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// TestProcessingOnAWorker runs MANGARR_PROCESSING=workers through the real
// worker protocol: a worker with the encode role says hello, takes the
// chapter's processing task and hands back the result — over HTTP, and in
// place when it shares the server's storage.
func TestProcessingOnAWorker(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "http"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			srv, a := newServer(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			g, _ := a.Settings.General(ctx)
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/workers", strings.NewReader(`{"name":"box","roles":["encode"]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Api-Key", g.APIKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != http.StatusCreated {
				t.Fatalf("create worker: %v %v", resp, err)
			}
			var made struct {
				Key string `json:"key"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&made); err != nil {
				t.Fatal(err)
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			w, err := worker.New(worker.Config{ServerURL: srv.URL, Key: made.Key, Roles: []string{"encode"}, SharedStorage: shared, Log: log})
			if err != nil {
				t.Fatal(err)
			}
			wctx, stop := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { _ = w.Run(wctx); close(done) }()
			defer func() { stop(); <-done }()

			// the worker is online (and says it can process) once its hello is in
			for {
				if ok, _ := a.Tasks.CanDo(ctx, model.TaskEncode); ok {
					break
				}
				if ctx.Err() != nil {
					t.Fatal("the worker never said hello")
				}
				time.Sleep(20 * time.Millisecond)
			}

			jobID := seedDownloadJob(t, a.DB)
			workDir := filepath.Join(a.Cfg.DataDir, "staging", "job-test")
			if err := os.MkdirAll(workDir, 0o775); err != nil {
				t.Fatal(err)
			}
			pages := []downloads.PageFile{pagePNG(t, workDir, "0001.png", 1000, 1500), pagePNG(t, workDir, "0002.png", 400, 600)}
			cfg := model.ProfileConfig{Pages: model.PageRules{MaxWidth: 500}}
			remote := &processing.Remote{Tasks: a.Tasks}
			var mu sync.Mutex
			var stages []string
			pctx := progress.With(worktasks.WithJob(ctx, jobID), func(ev progress.Event) {
				mu.Lock()
				stages = append(stages, ev.Stage)
				mu.Unlock()
			})
			res, err := remote.Process(pctx, cfg, pages, workDir)
			if err != nil {
				t.Fatal(err)
			}
			// queued pages wait for a worker; they aren't being encoded yet
			if len(stages) == 0 || stages[0] != progress.StageWait {
				t.Fatalf("stages: %v", stages)
			}
			if len(res.Pages) != 2 || res.Shrunk != 1 || res.Pages[0].Width != 500 || res.Pages[1] != pages[1] {
				t.Fatalf("result: %+v", res)
			}
			if _, err := os.Stat(res.Pages[0].Path); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func pagePNG(t *testing.T, dir, name string, w, h int) downloads.PageFile {
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

// seedDownloadJob makes the download job a processing task hangs off.
func seedDownloadJob(t *testing.T, d *db.DB) int64 {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	profile := &model.Profile{Name: "processing test", CreatedAt: now, UpdatedAt: now}
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
	job := &model.DownloadJob{Kind: model.JobKindDownload, Status: model.JobProcessing, SeriesID: ser.ID, ChapterID: ch.ID,
		NotBefore: now, CreatedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(job).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return job.ID
}
