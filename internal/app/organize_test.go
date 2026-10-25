package app_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakelibrary"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestMoveRestoreAndRename moves a series to another root folder, restores a
// reader's progress on the (fake) library server once it knows the new
// files, then renames the files after the naming format changed.
func TestMoveRestoreAndRename(t *testing.T) {
	app.RestoreRetryDelay = 100 * time.Millisecond
	dsn := dbtest.DSNs(t)["sqlite"]
	sc := fakesource.NewScenario("move")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Mover", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}, {URL: "/c2", Name: "Chapter 2", Number: 2, Uploaded: time.Now()}}})
	lib := fakelibrary.NewScenario("move")

	e := newTestApp(t, dsn)
	mod := e.addFakeModule(t, "move")
	libDef := &model.ProviderDefinition{Kind: "library", Implementation: "fakelibrary", Name: "Komga", Enabled: true, Settings: map[string]any{"scenario": "move"}}
	if err := e.App.Modules.Create(e.Ctx, libDef); err != nil {
		t.Fatal(err)
	}
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Mover", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "2 chapters", func() bool { return len(e.chapterFiles(t, ser.ID)) == 2 })

	oldDir := filepath.Join(e.Root, "Mover")
	ch1 := e.chapterFiles(t, ser.ID)["1"].RelativePath
	lib.SetProgress("k", []library.BookProgress{{LocalPath: filepath.Join(oldDir, ch1), Completed: true}})
	r := &model.User{Username: "ann", CountForCleanup: true, CreatedAt: time.Now().UTC()}
	_, _ = e.App.DB.NewInsert().Model(r).Exec(e.Ctx)
	acc := &model.LibraryAccount{UserID: r.ID, ModuleID: libDef.ID, Credentials: map[string]string{"apiKey": "k"}, CreatedAt: time.Now().UTC()}
	_, _ = e.App.DB.NewInsert().Model(acc).Exec(e.Ctx)

	// second root folder
	root2 := filepath.Join(t.TempDir(), "manga-ja")
	_ = os.MkdirAll(root2, 0o775)
	rf2 := &model.RootFolder{Path: root2, Language: "ja", CreatedAt: time.Now().UTC()}
	if _, err := e.App.DB.NewInsert().Model(rf2).Exec(e.Ctx); err != nil {
		t.Fatal(err)
	}
	newDir := filepath.Join(root2, "Mover")
	// the server hasn't scanned the new folder yet: restore waits for it
	lib.SetKnown()
	e.runCommand(t, "MoveSeries", map[string]any{"seriesId": ser.ID, "rootFolderId": rf2.ID, "moveFiles": true})
	if _, err := os.Stat(filepath.Join(newDir, ch1)); err != nil {
		t.Fatalf("file not moved: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatal("old folder should be gone")
	}
	var moved model.Series
	_ = e.App.DB.NewSelect().Model(&moved).Where("id = ?", ser.ID).Scan(e.Ctx)
	if moved.RootFolderID != rf2.ID {
		t.Fatalf("root folder not updated: %d", moved.RootFolderID)
	}
	// a sync right after the move must not forget that ann read chapter 1
	e.runCommand(t, "SyncReadProgress", nil)
	lib.SetKnown(filepath.Join(newDir, ch1))
	waitFor(t, 10*time.Second, "progress restored", func() bool { return len(lib.WrittenFor("k")) == 1 })
	if w := lib.WrittenFor("k")[0]; w.LocalPath != filepath.Join(newDir, ch1) || !w.Completed {
		t.Fatalf("written: %+v", w)
	}

	// rename after the naming format changed
	mm, _ := e.App.Settings.MediaManagement(e.Ctx)
	mm.ChapterFormat = "{Series Title} - {Chapter:000}"
	_ = e.App.Settings.Set(e.Ctx, settings.KeyMediaManagement, mm)
	plan, err := e.App.Organize.Preview(e.Ctx, []int64{ser.ID}, false)
	if err != nil || len(plan) != 1 || len(plan[0].Files) != 2 || plan[0].Files[0].To == plan[0].Files[0].From {
		t.Fatalf("rename preview: %+v %v", plan, err)
	}
	e.runCommand(t, "RenameFiles", map[string]any{"seriesIds": []any{ser.ID}})
	f1 := e.chapterFiles(t, ser.ID)["1"]
	if f1.RelativePath != "Mover - 001.cbz" {
		t.Fatalf("renamed path %q", f1.RelativePath)
	}
	if _, err := os.Stat(filepath.Join(newDir, "Mover - 001.cbz")); err != nil {
		t.Fatal("renamed file missing on disk")
	}
	if plan, _ := e.App.Organize.Preview(e.Ctx, []int64{ser.ID}, false); len(plan) != 0 {
		t.Fatalf("nothing left to rename, got %+v", plan)
	}
}
