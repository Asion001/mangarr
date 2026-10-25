package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestImportSiteAccount reads a site account's lists and progress: titles
// link to that site's catalog, lists act as categories (dropped ones are
// skipped), and every chapter before the one reached is marked read with
// the page in it.
func TestImportSiteAccount(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			sc := fakesource.NewScenario("account-" + dialect)
			sc.Sources = []source.SourceInfo{{ID: "8010", Name: "Site", Lang: "ru"}}
			var chs []fakesource.Chapter
			for i := 1; i <= 4; i++ {
				chs = append(chs, fakesource.Chapter{URL: "/manga/a/chapters/" + string(rune('0'+i)), Name: "Chapter", Number: float64(i), Uploaded: time.Now(), Pages: 2})
			}
			sc.AddManga(&fakesource.Manga{SourceID: "8010", URL: "/manga/a", Title: "Reading one", Status: source.StatusOngoing, Chapters: chs})
			sc.AddManga(&fakesource.Manga{SourceID: "8010", URL: "/manga/b", Title: "Dropped one", Status: source.StatusOngoing, Chapters: chs[:1]})
			sc.AccountToken = "secret"
			sc.Account = &source.Account{User: "someone",
				Lists: []source.AccountList{{ID: "L1", Name: "Reading", Status: source.ListReading}, {ID: "L2", Name: "Dropped", Status: source.ListDropped}},
				Entries: []source.AccountEntry{
					{Manga: source.Manga{MangaRef: source.MangaRef{SourceID: "8010", URL: "/manga/a"}, Title: "Reading one"}, Lists: []string{"L1"}, Chapter: 3, Page: 2},
					{Manga: source.Manga{MangaRef: source.MangaRef{SourceID: "8010", URL: "/manga/b"}, Title: "Dropped one"}, Lists: []string{"L2"}, Chapter: -1},
				}}

			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, "account-"+dialect)
			list, err := e.App.Imports.AccountSources(e.Ctx)
			if err != nil || len(list) != 1 || list[0].ModuleID != mod || list[0].SourceID != "8010" {
				t.Fatalf("account sources = %+v %v", list, err)
			}
			if _, err := e.App.Imports.CreateFromAccount(e.Ctx, mod, "8010", "wrong"); !errors.Is(err, imports.ErrUnauthorized) {
				t.Fatalf("wrong token: %v", err)
			}
			imp, err := e.App.Imports.CreateFromAccount(e.Ctx, mod, "8010", "secret")
			if err != nil {
				t.Fatal(err)
			}
			if imp.FileName != "Site account" || imp.Info.Lists["Dropped"] != source.ListDropped || !imp.Options.Categories["Dropped"].Skip {
				t.Fatalf("import = %+v", imp)
			}
			if err := e.App.Imports.Map(e.Ctx, imp.ID, nil, nil); err != nil {
				t.Fatal(err)
			}
			entries, _, err := e.App.Imports.Entries(e.Ctx, imp.ID, imports.EntryFilter{}, 0, 0)
			if err != nil || len(entries) != 2 {
				t.Fatalf("entries = %+v %v", entries, err)
			}
			reading, dropped := entries[0], entries[1]
			if reading.State != model.EntryReady || !reading.Selected || reading.Source.How != model.MatchExact || reading.Source.ModuleID != mod {
				t.Fatalf("reading = %+v %+v", reading, reading.Source)
			}
			if dropped.Selected {
				t.Fatal("a dropped title should start unselected")
			}
			res, err := e.App.Imports.Run(e.Ctx, imp.ID, nil)
			if err != nil || res.Added != 1 || res.Failed != 0 {
				t.Fatalf("run = %+v %v", res, err)
			}
			reloaded, _ := e.App.Imports.Get(e.Ctx, imp.ID)
			var r model.Reader
			if err := e.App.DB.NewSelect().Model(&r).Where("id = ?", reloaded.Options.ReaderID).Scan(e.Ctx); err != nil || r.Name != "Site account" {
				t.Fatalf("reader = %+v %v", r, err)
			}
			entries, _, _ = e.App.Imports.Entries(e.Ctx, imp.ID, imports.EntryFilter{}, 0, 0)
			var series []model.Chapter
			_ = e.App.DB.NewSelect().Model(&series).Where("series_id = ?", *entries[0].SeriesID).Order("number_sort").Scan(e.Ctx)
			if len(series) != 4 || series[1].Monitored || !series[2].Monitored {
				t.Fatalf("monitoring should start at the chapter reached: %+v", series)
			}
			var states []model.ChapterReadState
			_ = e.App.DB.NewSelect().Model(&states).Where("reader_id = ?", r.ID).Scan(e.Ctx)
			got := map[int64]model.ChapterReadState{}
			for _, st := range states {
				got[st.ChapterID] = st
			}
			if len(states) != 3 || !got[series[0].ID].Completed || !got[series[1].ID].Completed ||
				got[series[2].ID].Completed || got[series[2].ID].Page != 2 {
				t.Fatalf("read states = %+v", states)
			}

			// the chapter reached was open at its last page (its two pages):
			// read once it is downloaded and its page count is known
			waitFor(t, 20*time.Second, "chapter 3 read once downloaded", func() bool {
				var st model.ChapterReadState
				err := e.App.DB.NewSelect().Model(&st).Where("reader_id = ? AND chapter_id = ?", r.ID, series[2].ID).Scan(e.Ctx)
				return err == nil && st.Completed && st.Page == 0
			})
		})
	}
}
