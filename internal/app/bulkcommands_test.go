package app_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// Bulk edit refreshes and searches several series with one command each.
func TestRefreshAndSearchSeveralSeries(t *testing.T) {
	sc := fakesource.NewScenario("bulk-commands")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	for _, m := range []struct{ url, title string }{{"/one", "First Series"}, {"/two", "Second Series"}} {
		sc.AddManga(&fakesource.Manga{SourceID: "A", URL: m.url, Title: m.title, Status: source.StatusOngoing,
			Chapters: []fakesource.Chapter{{URL: m.url + "/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})
	}
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	mod := e.addFakeModule(t, "bulk-commands")
	var ids []int64
	for _, m := range []struct{ url, title string }{{"/one", "First Series"}, {"/two", "Second Series"}} {
		ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: m.title, RootFolderID: e.RFID, Monitor: model.MonitorAll,
			Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: m.url, SourceName: "Source A", Lang: "en"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ser.ID)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesIds": ids})
	for _, id := range ids {
		n, err := e.App.DB.NewSelect().Model((*model.Chapter)(nil)).Where("series_id = ?", id).Count(e.Ctx)
		if err != nil || n != 1 {
			t.Fatalf("series %d: want 1 chapter after refresh, got %d (%v)", id, n, err)
		}
	}
	e.runCommand(t, "SearchMissing", map[string]any{"seriesIds": ids})
	waitFor(t, 20*time.Second, "both chapters downloaded", func() bool {
		return len(e.chapterFiles(t, ids[0])) == 1 && len(e.chapterFiles(t, ids[1])) == 1
	})
}

// A series that can't be refreshed doesn't stop the rest of a bulk refresh;
// the command still reports the failure.
func TestBulkRefreshContinuesPastAFailedSeries(t *testing.T) {
	sc := fakesource.NewScenario("bulk-refresh-failure")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/one", Title: "Only Series", Status: source.StatusOngoing,
		Chapters: []fakesource.Chapter{{URL: "/one/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now()}}})
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	mod := e.addFakeModule(t, "bulk-refresh-failure")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Only Series", RootFolderID: e.RFID, Monitor: model.MonitorAll,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/one", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	c, err := e.App.Queue.Push(e.Ctx, "RefreshSeries", map[string]any{"seriesIds": []int64{ser.ID + 1000, ser.ID}}, "test")
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(e.Ctx, 30*time.Second)
	defer cancel()
	res, err := e.App.Queue.Wait(wctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != model.CommandFailed || !strings.Contains(res.Error, "1 of 2 series") {
		t.Fatalf("want the command to report 1 of 2 series failed, got %s: %s", res.Status, res.Error)
	}
	n, err := e.App.DB.NewSelect().Model((*model.Chapter)(nil)).Where("series_id = ?", ser.ID).Count(e.Ctx)
	if err != nil || n != 1 {
		t.Fatalf("want the second series refreshed (1 chapter), got %d (%v)", n, err)
	}
}

// A metadata refresh for picked series covers only those, and reports the
// ones it couldn't refresh.
func TestRefreshMetadataOfPickedSeries(t *testing.T) {
	sc := fakesource.NewScenario("bulk-metadata")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	titles := []string{"First Series", "Second Series", "Third Series"}
	for i, title := range titles {
		sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/" + strconv.Itoa(i), Title: title, Status: source.StatusOngoing})
	}
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	mod := e.addFakeModule(t, "bulk-metadata")
	var ids []int64
	for i, title := range titles {
		ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: title, RootFolderID: e.RFID, Monitor: model.MonitorAll,
			Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/" + strconv.Itoa(i), SourceName: "Source A", Lang: "en"}}})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, ser.ID)
	}
	run := func(body map[string]any) *model.Command {
		t.Helper()
		c, err := e.App.Queue.Push(e.Ctx, "RefreshMetadata", body, "test")
		if err != nil {
			t.Fatal(err)
		}
		wctx, cancel := context.WithTimeout(e.Ctx, 30*time.Second)
		defer cancel()
		res, err := e.App.Queue.Wait(wctx, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := run(map[string]any{"seriesIds": ids[:2]}); res.Status != model.CommandCompleted || !strings.HasPrefix(res.Message, "2/2 series") {
		t.Fatalf("want two series refreshed, got %s: %q %s", res.Status, res.Message, res.Error)
	}
	if res := run(map[string]any{"seriesIds": []int64{ids[2] + 1000, ids[2]}}); res.Status != model.CommandFailed || !strings.Contains(res.Error, "1 of 2 series") {
		t.Fatalf("want 1 of 2 series failed, got %s: %s", res.Status, res.Error)
	}
	if res := run(nil); res.Status != model.CommandCompleted || !strings.HasPrefix(res.Message, "3/3 series") {
		t.Fatalf("want the whole library refreshed, got %s: %q %s", res.Status, res.Message, res.Error)
	}
}
