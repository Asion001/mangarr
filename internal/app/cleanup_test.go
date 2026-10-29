package app_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakelibrary"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestReadSyncAndCleanup: two readers finish chapters in a (fake) Komga;
// cleanup deletes what both finished, keeps the last read chapter, never
// re-downloads cleaned chapters, and restore brings one back.
func TestReadSyncAndCleanup(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			name := "cleanup-" + dialect
			sc := fakesource.NewScenario(name)
			sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
			var chs []fakesource.Chapter
			for i := 1; i <= 4; i++ {
				chs = append(chs, fakesource.Chapter{URL: "/c" + string(rune('0'+i)), Name: "Chapter " + string(rune('0'+i)), Number: float64(i), Uploaded: time.Now().Add(-72 * time.Hour)})
			}
			sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Clean Me", Status: source.StatusOngoing, Chapters: chs})
			lib := fakelibrary.NewScenario(name)

			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, name)
			libDef := &model.ProviderDefinition{Kind: "library", Implementation: "fakelibrary", Name: "Komga", Enabled: true, Settings: map[string]any{"scenario": name}}
			if err := e.App.Modules.Create(e.Ctx, libDef); err != nil {
				t.Fatal(err)
			}
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Clean Me", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
				Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			waitFor(t, 20*time.Second, "4 chapters", func() bool { return len(e.chapterFiles(t, ser.ID)) == 4 })

			// two readers, both finished 1-3 long ago; reader 2 read 4 too
			dir := filepath.Join(e.Root, "Clean Me")
			path := func(n string) string { return filepath.Join(dir, "Clean Me Ch.000"+n+".cbz") }
			old := time.Now().Add(-30 * 24 * time.Hour)
			var p1, p2 []library.BookProgress
			for _, n := range []string{"1", "2", "3"} {
				p1 = append(p1, library.BookProgress{LocalPath: path(n), Completed: true, ReadAt: &old})
				p2 = append(p2, library.BookProgress{LocalPath: path(n), Completed: true, ReadAt: &old})
			}
			p2 = append(p2, library.BookProgress{LocalPath: path("4"), Completed: true, ReadAt: &old})
			lib.SetProgress("k1", p1)
			lib.SetProgress("k2", p2)
			for i, key := range []string{"k1", "k2"} {
				r := &model.User{Username: "reader" + key, CountForCleanup: true, CreatedAt: time.Now().UTC()}
				if _, err := e.App.DB.NewInsert().Model(r).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				acc := &model.LibraryAccount{UserID: r.ID, ModuleID: libDef.ID, Credentials: map[string]string{"apiKey": key}, CreatedAt: time.Now().UTC()}
				if _, err := e.App.DB.NewInsert().Model(acc).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				_ = i
			}
			cs := settings.DefaultCleanup()
			// dry run first: the automatic post-sync cleanup only previews
			cs.Enabled, cs.DryRun, cs.KeepLastRead, cs.GraceDays = true, true, 1, 7
			if err := e.App.Settings.Set(e.Ctx, settings.KeyCleanup, cs); err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "SyncReadProgress", nil)
			n, _ := e.App.DB.NewSelect().Model((*model.ChapterReadState)(nil)).Where("completed = ?", true).Count(e.Ctx)
			if n != 7 {
				t.Fatalf("expected 7 completed read states, got %d", n)
			}
			plan, err := e.App.Cleaner.Plan(e.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			// finished by both: 1,2,3 → keep last (3) → delete 1,2
			if len(plan.Candidates) != 2 || plan.Candidates[0].Chapter != "1" || plan.Candidates[1].Chapter != "2" {
				t.Fatalf("plan: %+v", plan.Candidates)
			}
			if len(e.chapterFiles(t, ser.ID)) != 4 {
				t.Fatal("dry run must not delete")
			}
			e.waitIdle(t) // the dry-run cleanup the sync queued
			cs.DryRun = false
			if err := e.App.Settings.Set(e.Ctx, settings.KeyCleanup, cs); err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "Cleanup", nil)
			files := e.chapterFiles(t, ser.ID)
			if len(files) != 2 {
				t.Fatalf("expected 2 files left, got %d", len(files))
			}
			if _, err := os.Stat(path("1")); !os.IsNotExist(err) {
				t.Fatal("chapter 1 file should be gone")
			}
			var ch1 model.Chapter
			_ = e.App.DB.NewSelect().Model(&ch1).Where("series_id = ? AND number_key = ?", ser.ID, "1").Scan(e.Ctx)
			if ch1.State != model.ChapterCleaned || ch1.CleanedAt == nil {
				t.Fatalf("chapter 1 should be cleaned: %+v", ch1)
			}
			// refresh + search must not bring cleaned chapters back
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID})
			time.Sleep(300 * time.Millisecond)
			if len(e.chapterFiles(t, ser.ID)) != 2 {
				t.Fatal("cleaned chapters were re-downloaded")
			}
			waitFor(t, 5*time.Second, "library rescan after cleanup", func() bool { return lib.RescanCount() > 0 })

			// restore chapter 1
			if _, err := e.App.Cleaner.Restore(e.Ctx, ch1.ID); err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{ch1.ID}, "explicit": true})
			waitFor(t, 20*time.Second, "restored chapter", func() bool { _, ok := e.chapterFiles(t, ser.ID)["1"]; return ok })

			// Manual deletion deduplicates the request, preserves the chapter and
			// records a distinct history event. Repeating it is a harmless skip.
			var ch4 model.Chapter
			if err := e.App.DB.NewSelect().Model(&ch4).Where("series_id = ? AND number_key = ?", ser.ID, "4").Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			result, err := e.App.Cleaner.RemoveChapters(e.Ctx, []int64{ch4.ID, ch4.ID})
			if err != nil {
				t.Fatal(err)
			}
			if result.Requested != 1 || result.Removed != 1 || result.Skipped != 0 || result.Freed <= 0 {
				t.Fatalf("manual removal result: %+v", result)
			}
			if _, err := os.Stat(path("4")); !os.IsNotExist(err) {
				t.Fatal("manually deleted chapter file should be gone")
			}
			var deleted model.Chapter
			if err := e.App.DB.NewSelect().Model(&deleted).Where("id = ?", ch4.ID).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			if deleted.State != model.ChapterCleaned || deleted.CleanedAt == nil || deleted.FileID != nil {
				t.Fatalf("manually deleted chapter should be cleaned: %+v", deleted)
			}
			var h model.History
			if err := e.App.DB.NewSelect().Model(&h).Where("chapter_id = ? AND event_type = ?", ch4.ID, model.HistoryDeleted).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			if h.Data["reason"] != "manual" {
				t.Fatalf("manual removal history: %+v", h)
			}
			repeat, err := e.App.Cleaner.RemoveChapters(e.Ctx, []int64{ch4.ID})
			if err != nil {
				t.Fatal(err)
			}
			if repeat.Requested != 1 || repeat.Removed != 0 || repeat.Skipped != 1 {
				t.Fatalf("repeat manual removal result: %+v", repeat)
			}

			// Deleting a whole series' files keeps the series and cleans every chapter that had one.
			left := len(e.chapterFiles(t, ser.ID))
			whole, err := e.App.Cleaner.RemoveSeriesFiles(e.Ctx, []int64{ser.ID})
			if err != nil {
				t.Fatal(err)
			}
			if whole.Removed != left || len(e.chapterFiles(t, ser.ID)) != 0 {
				t.Fatalf("series file removal: %+v, %d files before", whole, left)
			}
			if n, err := e.App.DB.NewSelect().Model((*model.Series)(nil)).Where("id = ?", ser.ID).Count(e.Ctx); err != nil || n != 1 {
				t.Fatalf("series should stay: %d %v", n, err)
			}
		})
	}
}
