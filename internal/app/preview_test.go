package app_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// A preview streams a title without adding it: nothing is monitored or
// downloaded and it stays out of the library, until Add adopts it with its
// chapters and progress.
func TestPreviewStaysOutOfTheLibraryUntilAdded(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			name := "preview-" + dialect
			scenario := fakesource.NewScenario(name)
			scenario.Sources = []source.SourceInfo{{ID: "paper", Name: "Paper Source", Lang: "en"}}
			scenario.AddManga(&fakesource.Manga{SourceID: "paper", URL: "/tower", Title: "Tower of Ash", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
				{URL: "/1", Name: "Chapter 1", Number: 1, Uploaded: time.Now(), Pages: 2},
				{URL: "/2", Name: "Chapter 2", Number: 2, Uploaded: time.Now(), Pages: 2},
			}})
			e := newTestApp(t, dsn)
			moduleID := e.addFakeModule(t, name)
			link := series.SourceLink{ModuleID: moduleID, SourceID: "paper", URL: "/tower", SourceName: "Paper Source", Lang: "en", Title: "Tower of Ash"}

			pv, created, err := e.App.Series.Preview(e.Ctx, series.AddRequest{Title: "Tower of Ash", Language: "en", Sources: []series.SourceLink{link}})
			if err != nil || !created || !pv.Preview || pv.Monitored {
				t.Fatalf("preview: %+v created=%v err=%v", pv, created, err)
			}
			again, created, err := e.App.Series.Preview(e.Ctx, series.AddRequest{Title: "Tower of Ash", Sources: []series.SourceLink{link}})
			if err != nil || created || again.ID != pv.ID {
				t.Fatalf("reopened preview: %+v created=%v err=%v", again, created, err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": pv.ID})
			e.waitIdle(t)
			var chapters []model.Chapter
			if err := e.App.DB.NewSelect().Model(&chapters).Where("series_id = ?", pv.ID).Order("number_sort").Scan(e.Ctx); err != nil || len(chapters) != 2 {
				t.Fatalf("chapters: %d %v", len(chapters), err)
			}
			for _, c := range chapters {
				if c.Monitored {
					t.Fatalf("preview chapter monitored: %+v", c)
				}
			}
			if n := e.countStatus(t, model.JobQueued) + e.countStatus(t, model.JobCompleted); n != 0 {
				t.Fatalf("preview queued %d downloads", n)
			}
			if _, _, err := e.App.DLQueue.Enqueue(e.Ctx, pv.ID, chapters[0].ID, nil, model.JobKindDownload, false); !errors.Is(err, downloads.ErrPreview) {
				t.Fatalf("enqueue a preview chapter: %v", err)
			}
			if n, err := e.App.Searcher.Evaluate(e.Ctx, pv.ID, nil, true); err != nil || n != 0 {
				t.Fatalf("evaluate a preview: %d %v", n, err)
			}
			if _, err := e.App.Reading.ReadAhead(e.Ctx, 1, pv.ID); err != nil {
				t.Fatalf("read ahead: %v", err)
			}
			all, err := e.App.Reading.AllSeries(e.Ctx, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, s := range all {
				if s.Series.ID == pv.ID {
					t.Fatal("preview listed by the reading service")
				}
			}
			if one, err := e.App.Reading.AllSeries(e.Ctx, 0, pv.ID); err != nil || len(one) != 1 {
				t.Fatalf("preview by id: %d %v", len(one), err)
			}

			reader := &model.Reader{Name: "ann", CreatedAt: time.Now().UTC()}
			if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			state := &model.ChapterReadState{ReaderID: reader.ID, ChapterID: chapters[0].ID, SeriesID: pv.ID, Page: 2, SyncedAt: time.Now().UTC()}
			if _, err := e.App.DB.NewInsert().Model(state).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}

			added, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Tower of Ash", Language: "en", Sources: []series.SourceLink{link},
				Monitor: model.MonitorAll, SearchMissing: false})
			if err != nil || added.ID != pv.ID || added.Preview || !added.Monitored {
				t.Fatalf("adopt: %+v %v", added, err)
			}
			e.waitIdle(t)
			var kept model.ChapterReadState
			if err := e.App.DB.NewSelect().Model(&kept).Where("id = ?", state.ID).Scan(e.Ctx); err != nil || kept.ChapterID != chapters[0].ID || kept.Page != 2 {
				t.Fatalf("progress after adding: %+v %v", kept, err)
			}
			if err := e.App.DB.NewSelect().Model(&chapters).Where("series_id = ?", pv.ID).Order("number_sort").Scan(e.Ctx); err != nil || len(chapters) != 2 || !chapters[0].Monitored {
				t.Fatalf("chapters after adding: %+v %v", chapters, err)
			}
			if all, _ := e.App.Reading.AllSeries(e.Ctx, 0, 0); len(all) != 1 {
				t.Fatalf("added series not listed: %d", len(all))
			}
		})
	}
}

func TestPurgePreviewsNobodyOpened(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			name := "preview-purge-" + dialect
			scenario := fakesource.NewScenario(name)
			scenario.Sources = []source.SourceInfo{{ID: "paper", Name: "Paper Source", Lang: "en"}}
			scenario.AddManga(&fakesource.Manga{SourceID: "paper", URL: "/old", Title: "Old Preview", Chapters: []fakesource.Chapter{{URL: "/1", Name: "1", Number: 1, Uploaded: time.Now(), Pages: 1}}})
			scenario.AddManga(&fakesource.Manga{SourceID: "paper", URL: "/new", Title: "New Preview", Chapters: []fakesource.Chapter{{URL: "/1", Name: "1", Number: 1, Uploaded: time.Now(), Pages: 1}}})
			e := newTestApp(t, dsn)
			moduleID := e.addFakeModule(t, name)
			open := func(url, title string) *model.Series {
				ser, _, err := e.App.Series.Preview(e.Ctx, series.AddRequest{Title: title, Language: "en",
					Sources: []series.SourceLink{{ModuleID: moduleID, SourceID: "paper", URL: url, SourceName: "Paper Source", Lang: "en"}}})
				if err != nil {
					t.Fatal(err)
				}
				return ser
			}
			old, fresh := open("/old", "Old Preview"), open("/new", "New Preview")
			if _, err := e.App.DB.NewUpdate().Model((*model.Series)(nil)).Set("preview_seen_at = ?", time.Now().UTC().Add(-40*24*time.Hour)).Where("id = ?", old.ID).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			n, err := e.App.Series.PurgePreviews(e.Ctx, time.Now().UTC().Add(-series.PreviewTTL))
			if err != nil || n != 1 {
				t.Fatalf("purged %d, %v", n, err)
			}
			for id, want := range map[int64]bool{old.ID: false, fresh.ID: true} {
				if has, _ := e.App.DB.NewSelect().Model((*model.Series)(nil)).Where("id = ?", id).Exists(e.Ctx); has != want {
					t.Fatalf("series %d exists=%v, want %v", id, has, want)
				}
			}
		})
	}
}
