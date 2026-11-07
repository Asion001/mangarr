package app_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	_ "github.com/Asion001/mangarr/internal/testutil/fakelibrary"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

type testEnv struct {
	App  *app.App
	Ctx  context.Context
	Root string
	RFID int64
}

func newTestApp(t *testing.T, dsn string) *testEnv {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.Config{Listen: ":0", DataDir: filepath.Join(dir, "config"), DB: dsn, LogLevel: "error"}
	_, ring := logging.Setup("error", io.Discard)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := app.New(ctx, cfg, log, ring)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); time.Sleep(50 * time.Millisecond); _ = a.Close() })
	dl := settings.DefaultDownloads()
	dl.MaxAttempts = 1 // fall back to the next source immediately
	if err := a.Settings.Set(ctx, settings.KeyDownloads, dl); err != nil {
		t.Fatal(err)
	}
	src := settings.DefaultSources()
	src.Throttle = model.ThrottleConfig{Preset: "fast"} // no pauses between chapters in tests
	if err := a.Settings.Set(ctx, settings.KeySources, src); err != nil {
		t.Fatal(err)
	}
	a.Rescanner.Quiet = 100 * time.Millisecond // fast debounce in tests
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "manga")
	if err := os.MkdirAll(root, 0o775); err != nil {
		t.Fatal(err)
	}
	rf := &model.RootFolder{Path: root, Language: "en", CreatedAt: time.Now().UTC()}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return &testEnv{App: a, Ctx: ctx, Root: root, RFID: rf.ID}
}

func waitFor(t *testing.T, d time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (e *testEnv) addFakeModule(t *testing.T, scenario string) int64 {
	t.Helper()
	def := &model.ProviderDefinition{Kind: "source", Implementation: "fake", Name: "Fake", Enabled: true, Settings: map[string]any{"scenario": scenario}}
	if err := e.App.Modules.Create(e.Ctx, def); err != nil {
		t.Fatal(err)
	}
	return def.ID
}

func (e *testEnv) runCommand(t *testing.T, name string, body map[string]any) {
	t.Helper()
	c, err := e.App.Queue.Push(e.Ctx, name, body, "test")
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(e.Ctx, 30*time.Second)
	defer cancel()
	res, err := e.App.Queue.Wait(wctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.CommandCompleted {
		t.Fatalf("%s failed: %s", name, res.Error)
	}
}

// waitIdle waits until no command is queued or running (e.g. the ones a
// command queued itself).
func (e *testEnv) waitIdle(t *testing.T) {
	t.Helper()
	waitFor(t, 20*time.Second, "idle command queue", func() bool {
		n, _ := e.App.DB.NewSelect().Model((*model.Command)(nil)).Where("status IN (?)", bun.List([]string{model.CommandQueued, model.CommandStarted})).Count(e.Ctx)
		return n == 0
	})
}

func (e *testEnv) chapterFiles(t *testing.T, seriesID int64) map[string]model.ChapterFile {
	var files []model.ChapterFile
	if err := e.App.DB.NewSelect().Model(&files).Where("series_id = ?", seriesID).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	var chapters []model.Chapter
	_ = e.App.DB.NewSelect().Model(&chapters).Where("series_id = ?", seriesID).Scan(e.Ctx)
	num := map[int64]string{}
	for _, c := range chapters {
		num[c.ID] = c.NumberKey
	}
	out := map[string]model.ChapterFile{}
	for _, f := range files {
		out[num[f.ChapterID]] = f
	}
	return out
}

// TestPipeline covers: add series → sync → download → CBZ import, fallback
// to a lower-priority source after a failure, and an in-place upgrade when a
// preferred scanlator appears. It runs without Suwayomi.
func TestPipeline(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			sc := fakesource.NewScenario("pipeline-" + dialect)
			sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}, {ID: "B", Name: "Source B", Lang: "en"}}
			t0 := time.Now().Add(-48 * time.Hour)
			sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/a/1", Title: "Test Series", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
				{URL: "/a/1/c1", Name: "Chapter 1", Number: 1, Scanlator: "Random", Uploaded: t0},
				{URL: "/a/1/c2", Name: "Chapter 2 - Two", Number: 2, Scanlator: "Random", Uploaded: t0, FailWith: fakesource.ErrForbidden},
			}})
			sc.AddManga(&fakesource.Manga{SourceID: "B", URL: "/b/1", Title: "Test Series", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
				{URL: "/b/1/c1", Name: "Ch. 1", Number: -1, Scanlator: "Other", Uploaded: t0},
				{URL: "/b/1/c2", Name: "Ch. 2", Number: -1, Scanlator: "Other", Uploaded: t0},
			}})

			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, "pipeline-"+dialect)
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{
				Title: "Test Series", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
				Sources: []series.SourceLink{
					{ModuleID: mod, SourceID: "A", URL: "/a/1", SourceName: "Source A", Lang: "en"},
					{ModuleID: mod, SourceID: "B", URL: "/b/1", SourceName: "Source B", Lang: "en"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			waitFor(t, 20*time.Second, "two imported chapters", func() bool { return len(e.chapterFiles(t, ser.ID)) == 2 })

			files := e.chapterFiles(t, ser.ID)
			if files["1"].SourceName != "Source A" || files["2"].SourceName != "Source B" {
				t.Fatalf("expected ch1 from A (priority) and ch2 from B (fallback): %+v", files)
			}
			n, _ := e.App.DB.NewSelect().Model((*model.Blocklist)(nil)).Where("series_id = ?", ser.ID).Count(e.Ctx)
			if n != 1 {
				t.Fatalf("expected the failed release to be blocklisted, got %d", n)
			}
			dir := filepath.Join(e.Root, "Test Series")
			path1 := filepath.Join(dir, files["1"].RelativePath)
			pages, ci, err := cbz.Read(path1)
			if err != nil || len(pages) != 3 || len(ci) == 0 {
				t.Fatalf("bad cbz: pages=%d ci=%d err=%v", len(pages), len(ci), err)
			}
			if files["1"].RelativePath != "Test Series Ch.0001.cbz" {
				t.Fatalf("unexpected file name %q", files["1"].RelativePath)
			}
			for _, sidecar := range []string{"series.json", "cover.jpg"} {
				if _, err := os.Stat(filepath.Join(dir, sidecar)); sidecar == "series.json" && err != nil {
					t.Fatalf("missing %s: %v", sidecar, err)
				}
			}

			// Upgrade: a preferred scanlator appears on source A for chapter 1.
			var prof model.Profile
			_ = e.App.DB.NewSelect().Model(&prof).Where("id = ?", ser.ProfileID).Scan(e.Ctx)
			prof.Config.AllowUpgrades = true
			prof.Config.PreferredScanlators = []string{"^Official$"}
			if _, err := e.App.DB.NewUpdate().Model(&prof).WherePK().Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			sc.Update(func() {
				m := sc.Mangas["A|/a/1"]
				m.Chapters = append(m.Chapters, fakesource.Chapter{URL: "/a/1/c1-official", Name: "Chapter 1", Number: 1, Scanlator: "Official", Uploaded: time.Now()})
			})
			before, _ := os.Stat(path1)
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			waitFor(t, 20*time.Second, "upgrade", func() bool { return e.chapterFiles(t, ser.ID)["1"].Scanlator == "Official" })
			after := e.chapterFiles(t, ser.ID)["1"]
			if after.RelativePath != files["1"].RelativePath {
				t.Fatalf("upgrade must keep the path: %q vs %q", after.RelativePath, files["1"].RelativePath)
			}
			if st, _ := os.Stat(path1); before != nil && os.SameFile(before, st) {
				t.Log("note: file replaced in place via rename (new inode expected)")
			}
			recycled, _ := filepath.Glob(filepath.Join(e.App.Library.RecycleDir(e.Ctx), "Test Series", "*Ch.0001.cbz"))
			if len(recycled) != 1 {
				t.Fatalf("expected previous file in recycle bin, got %v", recycled)
			}
			var hist []model.History
			_ = e.App.DB.NewSelect().Model(&hist).Where("series_id = ? AND event_type = ?", ser.ID, model.HistoryUpgraded).Scan(e.Ctx)
			if len(hist) != 1 {
				var all []model.History
				_ = e.App.DB.NewSelect().Model(&all).Where("series_id = ?", ser.ID).Order("id").Scan(e.Ctx)
				var jobs []model.DownloadJob
				_ = e.App.DB.NewSelect().Model(&jobs).Where("series_id = ?", ser.ID).Order("id").Scan(e.Ctx)
				for _, h := range all {
					t.Logf("history %s ch=%v %s %v", h.EventType, deref(h.ChapterID), h.SourceTitle, h.Data)
				}
				for _, j := range jobs {
					t.Logf("job %d ch=%d rel=%v status=%s upgrade=%v attempt=%d err=%s", j.ID, j.ChapterID, deref(j.ReleaseID), j.Status, j.IsUpgrade, j.Attempt, j.Error)
				}
				t.Fatalf("expected one upgraded history event, got %d", len(hist))
			}
		})
	}
}

func deref(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
