package app_test

import (
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

func (e *testEnv) jobs(t *testing.T) map[int64]model.DownloadJob {
	t.Helper()
	var list []model.DownloadJob
	if err := e.App.DB.NewSelect().Model(&list).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	out := map[int64]model.DownloadJob{}
	for _, j := range list {
		out[j.ID] = j
	}
	return out
}

func (e *testEnv) countStatus(t *testing.T, status string) int {
	n := 0
	for _, j := range e.jobs(t) {
		if j.Status == status {
			n++
		}
	}
	return n
}

// TestQueuePauseAndBulk covers the global pause, pausing a running job
// (it must stay paused even though its goroutine is still finishing), bulk
// resume, priorities and filter-based selection.
func TestQueuePauseAndBulk(t *testing.T) {
	dsn := dbtest.DSNs(t)["sqlite"]
	sc := fakesource.NewScenario("queue-mgmt")
	sc.PageDelay = 150 * time.Millisecond
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	var chs []fakesource.Chapter
	for i := 1; i <= 4; i++ {
		chs = append(chs, fakesource.Chapter{URL: "/c" + string(rune('0'+i)), Name: "Chapter", Number: float64(i), Uploaded: time.Now(), Pages: 4})
	}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Queue Test", Status: source.StatusOngoing, Chapters: chs})

	e := newTestApp(t, dsn)
	dl, _ := e.App.Settings.Downloads(e.Ctx)
	dl.MaxConcurrent, dl.MaxPerSource = 1, 1
	_ = e.App.Settings.Set(e.Ctx, settings.KeyDownloads, dl)
	// pause everything before the series is added
	if err := e.App.Settings.Set(e.Ctx, settings.KeyQueueState, settings.QueueState{Paused: true}); err != nil {
		t.Fatal(err)
	}
	mod := e.addFakeModule(t, "queue-mgmt")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Queue Test", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 10*time.Second, "4 queued jobs", func() bool { return e.countStatus(t, model.JobQueued) == 4 })
	time.Sleep(700 * time.Millisecond)
	if n := e.countStatus(t, model.JobQueued); n != 4 {
		t.Fatalf("paused queue started jobs: %d queued", n)
	}

	// priorities: move the last job to the top, then resume the queue
	var ids []int64
	for id := range e.jobs(t) {
		ids = append(ids, id)
	}
	last := ids[0]
	for _, id := range ids {
		last = max(last, id)
	}
	if n, err := e.App.Downloads.Bulk(e.Ctx, []int64{last}, "top"); err != nil || n != 1 {
		t.Fatalf("top: %d %v", n, err)
	}
	page, err := e.App.DLQueue.ListPage(e.Ctx, downloads.ListFilter{}, 1, 10)
	if err != nil || page.Items[0].ID != last || page.Total != 4 || page.Counts[model.JobQueued] != 4 {
		t.Fatalf("ordering after top: %+v %v", page, err)
	}
	_ = e.App.Settings.Set(e.Ctx, settings.KeyQueueState, settings.QueueState{})
	e.App.DLQueue.Wake()
	waitFor(t, 10*time.Second, "top job running", func() bool { return e.jobs(t)[last].Status == model.JobDownloading })

	// pause the running job: it's cancelled and stays paused
	if _, err := e.App.Downloads.Bulk(e.Ctx, []int64{last}, "pause"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(800 * time.Millisecond)
	if st := e.jobs(t)[last].Status; st != model.JobPaused {
		t.Fatalf("paused running job ended up %s", st)
	}
	var ch model.Chapter
	_ = e.App.DB.NewSelect().Model(&ch).Where("id = ?", e.jobs(t)[last].ChapterID).Scan(e.Ctx)
	if ch.State != model.ChapterQueued {
		t.Fatalf("chapter of a paused job: %s", ch.State)
	}

	// select-all-matching: pause every pending job, then resume all paused ones by filter.
	// The job that started after the first pause may be processing or importing by now;
	// processing is pausable, an import finishes, so wait for every job to land on one side.
	pending, _ := e.App.DLQueue.IDs(e.Ctx, downloads.ListFilter{Statuses: []string{model.JobQueued, model.JobDownloading, model.JobProcessing}})
	_, _ = e.App.Downloads.Bulk(e.Ctx, pending, "pause")
	waitFor(t, 10*time.Second, "every job paused or completed", func() bool {
		return e.countStatus(t, model.JobPaused)+e.countStatus(t, model.JobCompleted) == 4
	})
	all, _ := e.App.DLQueue.IDs(e.Ctx, downloads.ListFilter{Statuses: []string{model.JobPaused}})
	if want := 4 - e.countStatus(t, model.JobCompleted); len(all) != want || want < 2 {
		t.Fatalf("expected %d paused, got %d", want, len(all))
	}
	if n, err := e.App.Downloads.Bulk(e.Ctx, all, "resume"); err != nil || n != len(all) {
		t.Fatalf("resume: %d %v", n, err)
	}
	waitFor(t, 30*time.Second, "all chapters imported", func() bool { return len(e.chapterFiles(t, ser.ID)) == 4 })
	if n := e.countStatus(t, model.JobCompleted); n != 4 {
		t.Fatalf("completed jobs: %d", n)
	}
}

// TestReprocessIgnoresSourceLimits: processing a file already on disk
// doesn't wait for its source's download slot. With one download per
// source, a long download from source A used to hold back every reprocess
// job of files that came from A.
func TestReprocessIgnoresSourceLimits(t *testing.T) {
	sc := fakesource.NewScenario("reprocess-src")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Busy Source", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}, {URL: "/c2", Name: "Chapter 2", Number: 2, Uploaded: time.Now(), Pages: 10}}})
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	dl, _ := e.App.Settings.Downloads(e.Ctx)
	dl.MaxConcurrent, dl.MaxPerSource = 3, 1
	_ = e.App.Settings.Set(e.Ctx, settings.KeyDownloads, dl)
	mod := e.addFakeModule(t, "reprocess-src")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Busy Source", RootFolderID: e.RFID, Monitor: model.MonitorNone,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	var chs []model.Chapter
	_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Order("number_sort").Scan(e.Ctx)
	e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{chs[0].ID}, "explicit": true})
	waitFor(t, 20*time.Second, "chapter 1 downloaded", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })
	f := e.chapterFiles(t, ser.ID)["1"]
	if f.ReleaseID == nil {
		t.Fatal("file without release")
	}

	// a slow download from source A holds its only slot…
	sc.Update(func() { sc.PageDelay = 500 * time.Millisecond })
	e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{chs[1].ID}, "explicit": true})
	waitFor(t, 10*time.Second, "chapter 2 downloading", func() bool { return e.countStatus(t, model.JobDownloading) == 1 })
	// …while chapter 1 (also from A) is processed
	job, _, err := e.App.DLQueue.EnqueuePriority(e.Ctx, ser.ID, f.ChapterID, f.ReleaseID, model.JobKindReprocess, true, -100)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, "reprocess done during the download", func() bool {
		j := e.jobs(t)[job.ID]
		return j.Status == model.JobCompleted
	})
	if n := e.countStatus(t, model.JobDownloading); n != 1 {
		t.Fatalf("the download should still be running: %d downloading", n)
	}
	// Run on the Tasks page: the whole library, no series needed
	e.runCommand(t, "ProcessExisting", nil)
}
