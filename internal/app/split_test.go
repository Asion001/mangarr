package app_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

func TestReprocessSplitsTallPagesAndRemapsProgress(t *testing.T) {
	scenario := fakesource.NewScenario("split-tall")
	scenario.PageWidth = 400 // fake pages are 400 x 600
	scenario.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	scenario.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Tall Pages", Status: source.StatusOngoing,
		Chapters: []fakesource.Chapter{{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now(), Pages: 2}}})

	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	moduleID := e.addFakeModule(t, "split-tall")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Tall Pages", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: moduleID, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "original chapter", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })

	var chapter model.Chapter
	if err := e.App.DB.NewSelect().Model(&chapter).Where("series_id = ?", ser.ID).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	readers := []*model.Reader{
		{Name: "in progress", CountForCleanup: true, CreatedAt: now},
		{Name: "completed", CountForCleanup: true, CreatedAt: now},
	}
	for _, reader := range readers {
		if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
			t.Fatal(err)
		}
	}
	states := []*model.ChapterReadState{
		{ReaderID: readers[0].ID, ChapterID: chapter.ID, SeriesID: ser.ID, Page: 2, SyncedAt: now},
		{ReaderID: readers[1].ID, ChapterID: chapter.ID, SeriesID: ser.ID, Page: 2, Completed: true, ReadAt: &now, SyncedAt: now},
	}
	for _, state := range states {
		if err := readstate.Save(e.Ctx, e.App.DB, state); err != nil {
			t.Fatal(err)
		}
	}

	var profile model.Profile
	if err := e.App.DB.NewSelect().Model(&profile).Where("id = ?", ser.ProfileID).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	profile.Config.Pages = model.PageRules{JunkUnder: -1, SplitTall: true, SplitRatio: 1.2, SegmentRatio: 0.625} // 400 x 600 → 3 x 200
	if _, err := e.App.DB.NewUpdate().Model(&profile).WherePK().Exec(e.Ctx); err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "UpscaleExisting", map[string]any{"seriesId": ser.ID, "force": true})
	waitFor(t, 20*time.Second, "split chapter", func() bool { return e.chapterFiles(t, ser.ID)["1"].PageCount == 6 })

	file := e.chapterFiles(t, ser.ID)["1"]
	pages, info, err := cbz.Read(filepath.Join(e.Root, ser.Path, file.RelativePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 6 || !strings.Contains(string(info), "<PageCount>6</PageCount>") {
		t.Fatalf("split archive has %d pages and ComicInfo %q", len(pages), info)
	}
	for i, page := range pages {
		if want := fmt.Sprintf("%04d.png", i+1); page.Name != want {
			t.Errorf("page %d name = %q, want %q", i+1, page.Name, want)
		}
	}
	for i, want := range []int{4, 6} {
		var state model.ChapterReadState
		if err := e.App.DB.NewSelect().Model(&state).Where("id = ?", states[i].ID).Scan(e.Ctx); err != nil {
			t.Fatal(err)
		}
		if state.Page != want {
			t.Errorf("reader %d page = %d, want %d", i, state.Page, want)
		}
	}
}
