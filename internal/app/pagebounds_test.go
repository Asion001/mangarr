package app_test

import (
	"os"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestPageBoundsStored: a downloaded file's page boxes are measured once,
// kept in the database, and served from there from then on.
func TestPageBoundsStored(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			sc := fakesource.NewScenario("bounds-" + name)
			sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
			sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/m", Title: "Bounds", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
				{URL: "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})
			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, "bounds-"+name)
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Bounds", RootFolderID: e.RFID, Monitor: model.MonitorNone,
				Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/m", SourceName: "Source A", Lang: "en"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			var chs []model.Chapter
			_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Scan(e.Ctx)
			e.runCommand(t, "SearchMissing", map[string]any{"seriesId": ser.ID, "chapterIds": []int64{chs[0].ID}, "explicit": true})
			waitFor(t, 20*time.Second, "chapter downloaded", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })
			var file model.ChapterFile
			for _, f := range e.chapterFiles(t, ser.ID) {
				file = f
			}

			n, err := e.App.Reading.MeasureFile(e.Ctx, file.ID)
			if err != nil || n != file.PageCount || n == 0 {
				t.Fatalf("measured %d of %d pages: %v", n, file.PageCount, err)
			}
			if again, _ := e.App.Reading.MeasureFile(e.Ctx, file.ID); again != 0 {
				t.Fatalf("stored pages were measured again: %d", again)
			}
			rows, _ := e.App.DB.NewSelect().Model((*model.PageBounds)(nil)).Where("file_id = ?", file.ID).Count(e.Ctx)
			if rows != file.PageCount {
				t.Fatalf("%d rows for %d pages", rows, file.PageCount)
			}

			// a fresh process (no memory cache) answers from the database,
			// even with the file unreadable
			b, err := e.App.Reading.FileBook(e.Ctx, file.ID)
			if err != nil {
				t.Fatal(err)
			}
			if path := e.App.Reading.FileAt(e.Ctx, ser.ID, b.Path); path != file.ID {
				t.Fatalf("FileAt = %d, want %d", path, file.ID)
			}
			e.App.Reading.ForgetBounds()
			if err := os.Rename(b.Path, b.Path+".away"); err != nil {
				t.Fatal(err)
			}
			if bd, err := e.App.Reading.PageBounds(e.Ctx, b, 1); err != nil || bd.Width == 0 {
				t.Fatalf("stored bounds: %+v %v", bd, err)
			}
		})
	}
}
