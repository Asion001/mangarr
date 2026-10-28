package app_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestDownloadFromDisabledModule: a link still on a module that was turned
// off (Suwayomi, once the built-in sites took over) downloads through another
// enabled module that has the same catalog; one no other module has fails
// with a clear error instead of waiting for the module forever.
func TestDownloadFromDisabledModule(t *testing.T) {
	old := fakesource.NewScenario("disabled-old")
	old.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}, {ID: "Gone", Name: "Source Gone", Lang: "en"}}
	newer := fakesource.NewScenario("disabled-new")
	newer.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	for _, sc := range []*fakesource.Scenario{old, newer} {
		for _, src := range sc.Sources {
			sc.AddManga(&fakesource.Manga{SourceID: src.ID, URL: "/" + src.ID + "/m", Title: src.ID + " Series", Status: source.StatusOngoing,
				Chapters: []fakesource.Chapter{{URL: "/" + src.ID + "/m/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now().Add(-time.Hour)}}})
		}
	}

	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	from := e.addFakeModule(t, "disabled-old")
	e.addFakeModule(t, "disabled-new")
	add := func(id string) model.Series {
		ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: id + " Series", RootFolderID: e.RFID, Monitor: model.MonitorNone,
			Sources: []series.SourceLink{{ModuleID: from, SourceID: id, URL: "/" + id + "/m", SourceName: "Source " + id, Lang: "en"}}})
		if err != nil {
			t.Fatal(err)
		}
		e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
		return *ser
	}
	moved, gone := add("A"), add("Gone")
	e.waitIdle(t)

	var def model.ProviderDefinition
	if err := e.App.DB.NewSelect().Model(&def).Where("id = ?", from).Scan(e.Ctx); err != nil {
		t.Fatal(err)
	}
	def.Enabled = false
	if err := e.App.Modules.Update(e.Ctx, &def); err != nil {
		t.Fatal(err)
	}

	for _, ser := range []model.Series{moved, gone} {
		if n, err := e.App.Searcher.Evaluate(e.Ctx, ser.ID, nil, true); err != nil || n != 1 {
			t.Fatalf("search %s: grabbed %d, %v", ser.Title, n, err)
		}
	}
	waitFor(t, 20*time.Second, "the chapter through the other module", func() bool { return len(e.chapterFiles(t, moved.ID)) == 1 })

	var job model.DownloadJob
	waitFor(t, 20*time.Second, "the job nothing can serve to fail", func() bool {
		return e.App.DB.NewSelect().Model(&job).Where("series_id = ?", gone.ID).Scan(e.Ctx) == nil && job.Status == model.JobFailed
	})
	if !strings.Contains(job.Error, "disabled") || !strings.Contains(job.Error, "Source Gone") {
		t.Fatalf("the error should say the module is off and nothing else has the catalog: %q", job.Error)
	}
	// the link itself stays where it was
	var link model.SeriesSource
	_ = e.App.DB.NewSelect().Model(&link).Where("series_id = ?", moved.ID).Scan(e.Ctx)
	if link.ModuleID != from {
		t.Fatalf("downloading through another module must not move the link: %+v", link)
	}
}
