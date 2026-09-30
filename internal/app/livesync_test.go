package app_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakelibrary"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestLiveReadSync: a library server that pushes progress changes updates
// read states without a scheduled sync; imported (backup) states survive an
// "unread" event until the server reports them.
func TestLiveReadSync(t *testing.T) {
	dsn := dbtest.DSNs(t)["sqlite"]
	sc := fakesource.NewScenario("live")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Live", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}, {URL: "/c2", Name: "Chapter 2", Number: 2, Uploaded: time.Now()}}})
	lib := fakelibrary.NewScenario("live")
	lib.Live = true

	e := newTestApp(t, dsn)
	mod := e.addFakeModule(t, "live")
	libDef := &model.ProviderDefinition{Kind: "library", Implementation: "fakelibrary", Name: "Komga", Enabled: true, Settings: map[string]any{"scenario": "live"}}
	if err := e.App.Modules.Create(e.Ctx, libDef); err != nil {
		t.Fatal(err)
	}
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Live", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "2 chapters", func() bool { return len(e.chapterFiles(t, ser.ID)) == 2 })
	files := e.chapterFiles(t, ser.ID)
	path := func(n string) string { return filepath.Join(e.Root, "Live", files[n].RelativePath) }

	r := &model.Reader{Name: "ann", CreatedAt: time.Now().UTC()}
	_, _ = e.App.DB.NewInsert().Model(r).Exec(e.Ctx)
	acc := &model.ReaderAccount{ReaderID: r.ID, ModuleID: libDef.ID, Credentials: map[string]string{"apiKey": "k"}, CreatedAt: time.Now().UTC()}
	_, _ = e.App.DB.NewInsert().Model(acc).Exec(e.Ctx)
	e.App.Watcher.Refresh(e.Ctx)
	waitFor(t, 5*time.Second, "account connected", func() bool { return e.App.Watcher.Status()[acc.ID].Connected })

	state := func(chapterID int64) *model.ChapterReadState {
		var st model.ChapterReadState
		if err := e.App.DB.NewSelect().Model(&st).Where("reader_id = ? AND chapter_id = ?", r.ID, chapterID).Scan(e.Ctx); err != nil {
			return nil
		}
		return &st
	}
	push := func(ev library.ProgressEvent) {
		ctx, cancel := context.WithTimeout(e.Ctx, 5*time.Second)
		defer cancel()
		if err := lib.Push(ctx, "k", ev); err != nil {
			t.Fatal(err)
		}
	}

	push(library.ProgressEvent{Book: &library.BookProgress{LocalPath: path("1"), Completed: true}})
	waitFor(t, 5*time.Second, "chapter 1 read", func() bool { st := state(files["1"].ChapterID); return st != nil && st.Completed })
	push(library.ProgressEvent{Book: &library.BookProgress{LocalPath: path("1"), Page: 0}, Deleted: true})
	waitFor(t, 5*time.Second, "chapter 1 unread", func() bool { return state(files["1"].ChapterID) == nil })

	// an imported state isn't cleared by the server not knowing it yet
	imported := &model.ChapterReadState{ReaderID: r.ID, ChapterID: files["2"].ChapterID, SeriesID: ser.ID, Completed: true,
		SyncedAt: time.Now().UTC(), Origin: model.ReadOriginBackup}
	_ = readstate.Save(e.Ctx, e.App.DB, imported)
	push(library.ProgressEvent{Book: &library.BookProgress{LocalPath: path("2")}, Deleted: true})
	push(library.ProgressEvent{Book: &library.BookProgress{LocalPath: filepath.Join(e.Root, "Elsewhere", "x.cbz"), Completed: true}})
	time.Sleep(200 * time.Millisecond)
	if st := state(files["2"].ChapterID); st == nil || !st.Completed || st.Origin != model.ReadOriginBackup {
		t.Fatalf("imported state changed: %+v", st)
	}
	// the server reporting it read takes it over
	push(library.ProgressEvent{Book: &library.BookProgress{LocalPath: path("2"), Completed: true}})
	waitFor(t, 5*time.Second, "server owns chapter 2", func() bool { st := state(files["2"].ChapterID); return st != nil && st.Origin == "" })

	// removing the account stops the watch
	_, _ = e.App.DB.NewDelete().Model(acc).WherePK().Exec(e.Ctx)
	e.App.Watcher.Refresh(e.Ctx)
	if _, ok := e.App.Watcher.Status()[acc.ID]; ok {
		t.Fatal("deleted account is still watched")
	}
}
