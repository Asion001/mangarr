package app_test

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	_ "github.com/Asion001/mangarr/internal/modules/all"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

func pageWidths(t *testing.T, path string) []int {
	pages, _, err := cbz.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []int
	for _, p := range pages {
		cfg, _, err := image.DecodeConfig(bytesReader(p.Data))
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, cfg.Width)
	}
	return out
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// TestReprocessWithNothingToDo checks that reprocess jobs that have no work
// (upscaling disabled, or every page already wide enough) complete without
// rewriting the chapter file.
func TestReprocessWithNothingToDo(t *testing.T) {
	dsn := dbtest.DSNs(t)["sqlite"]
	sc := fakesource.NewScenario("reprocess-noop")
	sc.PageWidth = 200
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Wide Enough", Status: source.StatusOngoing,
		Chapters: []fakesource.Chapter{{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})

	e := newTestApp(t, dsn)
	mod := e.addFakeModule(t, "reprocess-noop")
	// an upscaler is configured but never asked: every page is wide enough
	up := &model.ProviderDefinition{Kind: "upscale", Implementation: "workers", Name: "Workers", Enabled: true,
		Settings: map[string]any{}}
	if err := e.App.Modules.Create(e.Ctx, up); err != nil {
		t.Fatal(err)
	}
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Wide Enough", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "download", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })
	before := e.chapterFiles(t, ser.ID)["1"]

	reprocessDone := func(n int) func() bool {
		return func() bool {
			c, _ := e.App.DB.NewSelect().Model((*model.DownloadJob)(nil)).
				Where("kind = ? AND status = ?", model.JobKindReprocess, model.JobCompleted).Count(e.Ctx)
			return c >= n
		}
	}
	// 1. processing disabled on the profile: nothing is queued
	e.runCommand(t, "UpscaleExisting", map[string]any{"seriesId": ser.ID, "force": true})
	if n, _ := e.App.DB.NewSelect().Model((*model.DownloadJob)(nil)).Where("kind = ?", model.JobKindReprocess).Count(e.Ctx); n != 0 {
		t.Fatalf("%d reprocess jobs for a profile without processing", n)
	}

	// 2. enabled, but pages are already wider than MinWidth
	var prof model.Profile
	_ = e.App.DB.NewSelect().Model(&prof).Where("id = ?", ser.ProfileID).Scan(e.Ctx)
	prof.Config.Upscale = model.UpscaleConfig{Enabled: true, MinWidth: 32, Model: "waifu2x-cunet", Noise: 1, Format: "png", Quality: 90}
	if _, err := e.App.DB.NewUpdate().Model(&prof).WherePK().Exec(e.Ctx); err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "UpscaleExisting", map[string]any{"seriesId": ser.ID, "force": true})
	waitFor(t, 20*time.Second, "no-op reprocess", reprocessDone(1))

	after := e.chapterFiles(t, ser.ID)["1"]
	if after.ID != before.ID || after.SHA256 != before.SHA256 || after.Upscaled {
		t.Fatalf("file was rewritten: before %+v after %+v\n%s", before, after, jobsAndHistory(t, e, ser.ID))
	}
	if after.ProcessState != model.ProcessDone {
		t.Fatalf("file should be marked processed: %+v", after)
	}
	if after.ProcessSeconds != 0 || after.ProcessPages != 0 {
		t.Fatalf("no-op processing must not report throughput: %v s, %d pages", after.ProcessSeconds, after.ProcessPages)
	}
	var failed int
	failed, _ = e.App.DB.NewSelect().Model((*model.DownloadJob)(nil)).Where("status = ?", model.JobFailed).Count(e.Ctx)
	if failed != 0 {
		t.Fatalf("%d failed jobs", failed)
	}
}

// TestBackgroundAVIF imports originals first, then re-encodes them to AVIF
// in the background at the same path, queued as soon as the import is done.
func TestBackgroundAVIF(t *testing.T) {
	dsn := dbtest.DSNs(t)["sqlite"]
	sc := fakesource.NewScenario("avif")
	sc.PageWidth, sc.PageNoise = 240, true
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Encoded", Status: source.StatusOngoing,
		Chapters: []fakesource.Chapter{{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now(), Pages: 3}}})
	e := newTestApp(t, dsn)
	mod := e.addFakeModule(t, "avif")
	// the sweep a module change triggers must not be what processes the chapter
	waitFor(t, 20*time.Second, "module-change sweep", func() bool {
		n, _ := e.App.DB.NewSelect().Model((*model.Command)(nil)).Where("name = ? AND trigger = ? AND status = ?", "ProcessBacklog", "modules-changed", model.CommandCompleted).Count(e.Ctx)
		return n > 0
	})
	var prof model.Profile
	_ = e.App.DB.NewSelect().Model(&prof).Where("is_default = ?", true).Scan(e.Ctx)
	prof.Config.Encode = model.EncodeConfig{Format: "avif", Preset: "fast", Grayscale: true, MinSavingsPct: 5, RecycleOriginals: false}
	prof.Config.ProcessTiming = "background"
	if _, err := e.App.DB.NewUpdate().Model(&prof).WherePK().Exec(e.Ctx); err != nil {
		t.Fatal(err)
	}
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Encoded", RootFolderID: e.RFID, ProfileID: prof.ID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "download", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })
	orig := e.chapterFiles(t, ser.ID)["1"]
	if orig.Format != "png" || orig.ProcessState != "" {
		t.Fatalf("background timing must import the original first: %+v", orig)
	}
	// the import queues its processing itself, without waiting for a sweep
	waitFor(t, 30*time.Second, "encoded file", func() bool { return e.chapterFiles(t, ser.ID)["1"].Format == "avif" })
	f := e.chapterFiles(t, ser.ID)["1"]
	if f.RelativePath != orig.RelativePath || f.ProcessState != model.ProcessDone || f.SizeOriginal != orig.Size || f.Size >= orig.Size {
		t.Fatalf("encoded file: %+v (original %+v)", f, orig)
	}
	if f.ProcessSeconds <= 0 || f.ProcessPages != 3 {
		t.Fatalf("processing time wasn't recorded: %v s, %d pages", f.ProcessSeconds, f.ProcessPages)
	}
	waitFor(t, 5*time.Second, "finished jobs leave the live progress list", func() bool { return len(e.App.Downloads.Live.All()) == 0 })
	pages, _, err := cbz.Read(filepath.Join(e.Root, "Encoded", f.RelativePath))
	if err != nil || len(pages) != 3 || filepath.Ext(pages[0].Name) != ".avif" {
		t.Fatalf("cbz pages: %v %v", pages, err)
	}
	// nothing is queued again for the same settings
	e.runCommand(t, "ProcessBacklog", nil)
	if n, _ := e.App.DB.NewSelect().Model((*model.DownloadJob)(nil)).Where("kind = ? AND status = ?", model.JobKindReprocess, model.JobQueued).Count(e.Ctx); n != 0 {
		t.Fatalf("%d jobs queued again", n)
	}
}

// TestProcessingBacklogMaterializesEveryPlannedChapter keeps planned work
// visible and manageable even when execution is paused. The old 50-job window
// hid the rest until earlier jobs completed.
func TestProcessingBacklogMaterializesEveryPlannedChapter(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			if err := e.App.Settings.Set(e.Ctx, settings.KeyQueueState, settings.QueueState{Paused: true}); err != nil {
				t.Fatal(err)
			}
			var profile model.Profile
			if err := e.App.DB.NewSelect().Model(&profile).Where("is_default = ?", true).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			profile.Config.Encode = model.EncodeConfig{Format: "avif", Preset: "fast"}
			profile.Config.ProcessExisting = true
			if _, err := e.App.DB.NewUpdate().Model(&profile).WherePK().Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			ser := &model.Series{Title: "Planned Processing", SortTitle: "planned processing", Status: model.StatusOngoing,
				RootFolderID: e.RFID, Path: "Planned Processing", ProfileID: profile.ID, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 75; i++ {
				chapter := &model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(i), NumberSort: float64(i), State: model.ChapterImported,
					FirstSeenAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(chapter).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				file := &model.ChapterFile{ChapterID: chapter.ID, SeriesID: ser.ID, RelativePath: fmt.Sprintf("%d.cbz", i),
					Size: 1000, PageCount: 10, Format: "png", ImportedAt: now.Add(time.Duration(i) * time.Second)}
				if _, err := e.App.DB.NewInsert().Model(file).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
			}

			e.runCommand(t, "ProcessBacklog", nil)
			first, err := e.App.DLQueue.ListPage(e.Ctx, downloads.ListFilter{Kind: model.JobKindReprocess}, 1, 50)
			if err != nil {
				t.Fatal(err)
			}
			second, err := e.App.DLQueue.ListPage(e.Ctx, downloads.ListFilter{Kind: model.JobKindReprocess}, 2, 50)
			if err != nil {
				t.Fatal(err)
			}
			if first.Total != 75 || len(first.Items) != 50 || len(second.Items) != 25 {
				t.Fatalf("processing queue pages: total=%d first=%d second=%d", first.Total, len(first.Items), len(second.Items))
			}
		})
	}
}

// jobsAndHistory describes what ran for a series, so a test that finds an
// unexpected rewrite can say which job did it.
func jobsAndHistory(t *testing.T, e *testEnv, seriesID int64) string {
	t.Helper()
	var out strings.Builder
	var jobs []model.DownloadJob
	_ = e.App.DB.NewSelect().Model(&jobs).Where("series_id = ?", seriesID).Order("id").Scan(e.Ctx)
	out.WriteString("jobs:\n")
	for _, j := range jobs {
		fmt.Fprintf(&out, "  #%d %s %s upgrade=%v priority=%d created=%s error=%q\n",
			j.ID, j.Kind, j.Status, j.IsUpgrade, j.Priority, j.CreatedAt.Format(time.TimeOnly), j.Error)
	}
	var events []model.History
	_ = e.App.DB.NewSelect().Model(&events).Where("series_id = ?", seriesID).Order("id").Scan(e.Ctx)
	out.WriteString("history:\n")
	for _, h := range events {
		fmt.Fprintf(&out, "  %s %s %v\n", h.CreatedAt.Format(time.TimeOnly), h.EventType, h.Data)
	}
	return out.String()
}
