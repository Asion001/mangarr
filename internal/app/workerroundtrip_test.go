package app_test

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
	"github.com/Asion001/mangarr/internal/worker"
)

// TestWorkerDownloadsAChapter is the whole worker story over the real
// protocol: the server resolves a chapter's pages and writes a task, a
// worker process leases it, fetches the pages from the "site" with its own
// address, uploads them, and the server imports the chapter.
func TestWorkerDownloadsAChapter(t *testing.T) {
	sc := fakesource.NewScenario("worker-roundtrip")
	sc.PageWidth = 200
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Remote Work", Status: source.StatusOngoing,
		Chapters: []fakesource.Chapter{{URL: "/c1", Name: "Chapter 1", Number: 1, Pages: 5, Uploaded: time.Now()}}})
	site := sc.ServePages() // the pages the worker fetches itself
	defer site.Close()

	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	srv := httptest.NewServer(api.New(e.App))
	defer srv.Close()

	// a worker with a key of its own, as System → Workers issues it
	key, w, err := e.App.Auth.CreateWorker(e.Ctx, "test-worker", []string{model.RoleDownload}, 0)
	if err != nil {
		t.Fatal(err)
	}
	dl, _ := e.App.Settings.Downloads(e.Ctx)
	dl.WorkerPlacement = settings.PlaceWorkers // wait for the worker rather than doing it here
	if err := e.App.Settings.Set(e.Ctx, settings.KeyDownloads, dl); err != nil {
		t.Fatal(err)
	}

	ctx, stop := context.WithCancel(e.Ctx)
	defer stop()
	wk, err := worker.New(worker.Config{ServerURL: srv.URL, Key: key, Roles: []string{model.RoleDownload},
		Version: "test", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Status: worker.NewStatus()})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = wk.Run(ctx)
	}()

	mod := e.addFakeModule(t, "worker-roundtrip")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Remote Work", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 30*time.Second, "the worker to download the chapter", func() bool {
		return len(e.chapterFiles(t, ser.ID)) == 1
	})

	file := e.chapterFiles(t, ser.ID)["1"]
	if file.PageCount != 5 {
		t.Fatalf("imported file: %+v", file)
	}
	dir, err := e.App.Library.SeriesDir(e.Ctx, &model.Series{ID: ser.ID, RootFolderID: ser.RootFolderID, Path: ser.Path})
	if err != nil {
		t.Fatal(err)
	}
	pages, _, err := cbz.Read(filepath.Join(dir, file.RelativePath))
	if err != nil || len(pages) != 5 {
		t.Fatalf("the imported archive: %v %d pages", err, len(pages))
	}

	// the pages came from the site over HTTP, not from the module in process
	if sc.Served != 5 || sc.Fetches != 0 {
		t.Fatalf("%d pages served to the worker, %d fetched here", sc.Served, sc.Fetches)
	}

	// the worker's own status page saw it, by name
	waitFor(t, 10*time.Second, "the worker's status to count the chapter", func() bool { return wk.Snapshot().Totals.Done == 1 })
	snap := wk.Snapshot()
	if snap.State != worker.StateReady || snap.Name != "test-worker" || len(snap.Recent) != 1 ||
		snap.Recent[0].Label != "Remote Work · Ch. 1" || snap.Recent[0].Pages != 5 || snap.Totals.Pages != 5 || len(snap.Active) != 0 {
		t.Fatalf("status: %+v", snap)
	}

	// and the work is on the worker's account
	var got model.Worker
	if err := e.App.DB.NewSelect().Model(&got).Where("id = ?", w.ID).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if got.TasksDone != 1 || got.PagesDone != 5 || got.BytesIn == 0 || got.BytesOut == 0 || got.Version != "test" {
		t.Fatalf("worker after the chapter: %+v", got)
	}
	var tasks []model.WorkerTask
	if err := e.App.DB.NewSelect().Model(&tasks).Scan(e.Ctx); err != nil || len(tasks) != 1 || tasks[0].State != model.TaskDone {
		t.Fatalf("tasks: %v %+v", err, tasks)
	}
	var events []model.History
	if err := e.App.DB.NewSelect().Model(&events).Where("series_id = ?", ser.ID).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[len(events)-1].EventType != model.HistoryImported {
		t.Fatalf("history: %+v", events)
	}

	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker did not stop")
	}
	if st := wk.Snapshot().State; st != worker.StateStopped {
		t.Fatalf("state after stopping: %s", st)
	}
}
