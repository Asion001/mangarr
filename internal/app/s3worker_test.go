package app_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/libfs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
	"github.com/Asion001/mangarr/internal/worker"
)

// TestWorkerPackagesIntoTheBucket: with the library on object storage a
// worker builds the chapter's archive itself — processing it when it has
// the encode role — and uploads it to the bucket; the pages never reach
// the server. A worker that may not process sends them to the server.
func TestWorkerPackagesIntoTheBucket(t *testing.T) {
	for _, tc := range []struct {
		name     string
		roles    []string
		packaged bool
	}{
		{"packages and processes", []string{model.RoleDownload, model.RoleEncode}, true},
		{"uploads pages to be processed elsewhere", []string{model.RoleDownload}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scenario := "s3-worker-" + strings.ReplaceAll(tc.name, " ", "-")
			sc := fakesource.NewScenario(scenario)
			sc.PageWidth = 200
			sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
			sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Bucket Work", Status: source.StatusOngoing,
				Chapters: []fakesource.Chapter{{URL: "/c1", Name: "Chapter 1", Number: 1, Pages: 4, Uploaded: time.Now()}}})
			site := sc.ServePages()
			defer site.Close()

			e, bucket := newS3TestApp(t)
			srv := httptest.NewServer(api.New(e.App))
			defer srv.Close()
			key, _, err := e.App.Auth.CreateWorker(e.Ctx, "bucket-worker", tc.roles, 0)
			if err != nil {
				t.Fatal(err)
			}
			dl, _ := e.App.Settings.Downloads(e.Ctx)
			dl.WorkerPlacement = settings.PlaceWorkers
			if err := e.App.Settings.Set(e.Ctx, settings.KeyDownloads, dl); err != nil {
				t.Fatal(err)
			}
			// pages wider than 100 px are shrunk while downloading
			var prof model.Profile
			if err := e.App.DB.NewSelect().Model(&prof).Order("id").Limit(1).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			prof.Config.Pages.MaxWidth, prof.Config.ProcessTiming = 100, "inline"
			if _, err := e.App.DB.NewUpdate().Model(&prof).WherePK().Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}

			ctx, stop := context.WithCancel(e.Ctx)
			defer stop()
			wk, err := worker.New(worker.Config{ServerURL: srv.URL, Key: key, Roles: tc.roles,
				Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Status: worker.NewStatus()})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { defer close(done); _ = wk.Run(ctx) }()
			defer func() { stop(); <-done }()

			mod := e.addFakeModule(t, scenario)
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Bucket Work", RootFolderID: e.RFID, ProfileID: prof.ID, Monitor: model.MonitorAll, SearchMissing: true,
				Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			waitFor(t, 30*time.Second, "the chapter", func() bool {
				if len(e.chapterFiles(t, ser.ID)) == 1 {
					return true
				}
				var jobs []model.DownloadJob
				_ = e.App.DB.NewSelect().Model(&jobs).Scan(e.Ctx)
				var tasks []model.WorkerTask
				_ = e.App.DB.NewSelect().Model(&tasks).Scan(e.Ctx)
				for _, j := range jobs {
					t.Logf("job %d %s %s", j.ID, j.Status, j.Error)
				}
				for _, k := range tasks {
					t.Logf("task %d %s %s %s", k.ID, k.Kind, k.State, k.Error)
				}
				return false
			})

			file := e.chapterFiles(t, ser.ID)["1"]
			if file.PageCount != 4 || file.AvgWidth != 100 || file.ProcessParams == "" {
				t.Fatalf("imported file: %+v", file)
			}
			path := filepath.Join(e.Root, "Bucket Work", file.RelativePath)
			pages, ci, err := cbz.Read(path)
			if err != nil || len(pages) != 4 || !strings.Contains(string(ci), "<PageCount>4</PageCount>") {
				t.Fatalf("the archive in the bucket: %v, %d pages, ComicInfo %s", err, len(pages), ci)
			}
			if st, _ := libfs.Stat(path); st == nil || st.Size() != file.Size {
				t.Fatalf("size: %v vs %d", st, file.Size)
			}
			for _, k := range bucket.Keys() {
				if strings.Contains(k, downloads.StagingDir) {
					t.Fatalf("an upload was left in staging: %s", k)
				}
			}
			// whether the pages came here says which way the chapter went
			uploads := 0
			var tasks []model.WorkerTask
			_ = e.App.DB.NewSelect().Model(&tasks).Scan(e.Ctx)
			for _, task := range tasks {
				if task.Kind == model.TaskDownload && task.State == model.TaskDone && task.BytesOut != file.Size {
					uploads++
				}
			}
			if tc.packaged && uploads != 0 {
				t.Fatalf("the pages went to the server: %+v", tasks)
			}
			if !tc.packaged && uploads != 1 {
				t.Fatalf("expected the pages to be uploaded to the server: %+v", tasks)
			}
		})
	}
}
