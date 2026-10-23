package app_test

import (
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// TestImportMihonBackup maps a Mihon backup (exact catalog, missing
// extension, unknown source found by title, series already in the library),
// installs the extension and imports: series are added monitoring from the
// first unread chapter, with read chapters and blocked scanlators.
func TestImportMihonBackup(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			sc := fakesource.NewScenario("import-" + dialect)
			sc.Sources = []source.SourceInfo{{ID: "2499", Name: "MangaDex", Lang: "en"}, {ID: "555", Name: "Other", Lang: "en"}}
			sc.Extensions = []fakesource.Extension{{Extension: source.Extension{Pkg: "eu.kanade.weebcentral", Name: "Weeb Central", Lang: "en"},
				Sources: []source.SourceInfo{{ID: "777", Name: "Weeb Central", Lang: "en"}}}}
			chapters := func(n int) []fakesource.Chapter {
				var out []fakesource.Chapter
				for i := 1; i <= n; i++ {
					out = append(out, fakesource.Chapter{URL: "/c" + string(rune('0'+i)), Name: "Chapter", Number: float64(i), Uploaded: time.Now(), Pages: 1})
				}
				return out
			}
			sc.AddManga(&fakesource.Manga{SourceID: "2499", URL: "/manga/abc", Title: "One Piece", Status: source.StatusOngoing, Chapters: chapters(3)})
			sc.AddManga(&fakesource.Manga{SourceID: "777", URL: "/series/solo", Title: "Solo Leveling", Status: source.StatusCompleted, Chapters: chapters(2)})
			sc.AddManga(&fakesource.Manga{SourceID: "555", URL: "/frieren", Title: "Frieren", Status: source.StatusOngoing, Chapters: chapters(2)})
			sc.AddManga(&fakesource.Manga{SourceID: "555", URL: "/existing", Title: "Existing", Status: source.StatusOngoing, Chapters: chapters(2)})
			sc.AddManga(&fakesource.Manga{SourceID: "2499", URL: "/dupb", Title: "Dupe", Status: source.StatusOngoing, Chapters: chapters(1)})
			sc.AddManga(&fakesource.Manga{SourceID: "555", URL: "/dupa", Title: "Dupe", Status: source.StatusOngoing, Chapters: chapters(3)})

			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, "import-"+dialect)
			existing, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Existing", RootFolderID: e.RFID, Monitor: model.MonitorNone,
				Sources: []series.SourceLink{{ModuleID: mod, SourceID: "555", URL: "/existing"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": existing.ID})

			readAt := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
			backup := &backupimport.Backup{Categories: []string{"Reading"}, Sources: map[string]string{"2499": "MangaDex", "777": "Weeb Central", "999": "Gone"},
				Entries: []backupimport.BackupManga{
					{SourceID: "2499", URL: "/manga/abc", Title: "One Piece", Favorite: true, Categories: []string{"Reading"},
						ExcludedScanlators: []string{"Bad Scans"},
						Chapters: []backupimport.BackupChapter{{URL: "/c1", Number: 1, Read: true, ReadAt: &readAt}, {URL: "/c2", Number: 2, Read: true},
							{URL: "/c3", Number: 3, LastPageRead: 4}}},
					{SourceID: "777", URL: "/series/solo", Title: "Solo Leveling", Favorite: true},
					{SourceID: "999", URL: "/x", Title: "Frieren", Favorite: true},
					{SourceID: "555", URL: "/existing", Title: "Existing", Favorite: true, Chapters: []backupimport.BackupChapter{{URL: "/elsewhere", Number: 1, Read: true}}},
					{SourceID: "2499", URL: "/manga/abc", Title: "History only", Favorite: false},
					// the same series at two sources: the one read further is added first
					{SourceID: "2499", URL: "/dupb", Title: "Dupe B", Favorite: true},
					{SourceID: "555", URL: "/dupa", Title: "Dupe A", Favorite: true,
						Chapters: []backupimport.BackupChapter{{URL: "/c1", Number: 1, Read: true}, {URL: "/c2", Number: 2, Read: true}}},
				}}
			imp, err := e.App.Imports.Create(e.Ctx, "mihon.tachibk", backupimport.MarshalMihon(backup))
			if err != nil {
				t.Fatal(err)
			}
			if err := e.App.Imports.Map(e.Ctx, imp.ID, nil, nil); err != nil {
				t.Fatal(err)
			}
			byTitle := func() map[string]model.ImportEntry {
				list, _, err := e.App.Imports.Entries(e.Ctx, imp.ID, imports.EntryFilter{}, 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				out := map[string]model.ImportEntry{}
				for _, x := range list {
					out[x.Title] = x
				}
				return out
			}
			got := byTitle()
			want := map[string]string{"One Piece": model.EntryReady, "Solo Leveling": model.EntryExtension, "Frieren": model.EntryReady,
				"Existing": model.EntryLibrary, "History only": model.EntryReady}
			for title, state := range want {
				if got[title].State != state {
					t.Fatalf("%s: state %s (%s), want %s", title, got[title].State, got[title].Message, state)
				}
			}
			if got["One Piece"].Source.How != model.MatchExact || got["Frieren"].Source.How != model.MatchTitle || got["Frieren"].Source.URL != "/frieren" {
				t.Fatalf("sources: %+v %+v", got["One Piece"].Source, got["Frieren"].Source)
			}
			if got["History only"].Selected || !got["One Piece"].Selected || got["Solo Leveling"].Selected {
				t.Fatal("selection")
			}
			if ext := got["Solo Leveling"].Extension; ext == nil || ext.Pkg != "eu.kanade.weebcentral" {
				t.Fatalf("extension = %+v", ext)
			}

			ids, err := e.App.Imports.InstallExtensions(e.Ctx, imp.ID, nil)
			if err != nil || len(ids) != 1 {
				t.Fatalf("install: %v %v", ids, err)
			}
			if err := e.App.Imports.Map(e.Ctx, imp.ID, ids, nil); err != nil {
				t.Fatal(err)
			}
			if s := byTitle()["Solo Leveling"]; s.State != model.EntryReady || !s.Selected || s.Source.SourceID != "777" {
				t.Fatalf("after install: %+v", s)
			}

			for _, title := range []string{"Dupe A", "Dupe B"} {
				md := &model.ImportMetadata{ModuleID: 999, Provider: "anilist", ID: "42"}
				if _, err := e.App.Imports.UpdateEntries(e.Ctx, imp.ID, imports.EntryFilter{IDs: []int64{byTitle()[title].ID}}, imports.EntryPatch{Metadata: md}); err != nil {
					t.Fatal(err)
				}
			}
			res, err := e.App.Imports.Run(e.Ctx, imp.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Added != 4 || res.Merged != 2 || res.Failed != 0 {
				t.Fatalf("result = %+v (%+v)", res, byTitle())
			}
			got = byTitle()
			op := got["One Piece"]
			if op.State != model.EntryImported || op.SeriesID == nil {
				t.Fatalf("one piece = %+v", op)
			}
			ser, err := e.App.Series.Get(e.Ctx, *op.SeriesID)
			if err != nil {
				t.Fatal(err)
			}
			if len(ser.BlockedScanlators) != 1 || len(ser.Tags) != 1 {
				t.Fatalf("series = %+v", ser)
			}
			var chs []model.Chapter
			_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Order("number_sort").Scan(e.Ctx)
			if len(chs) != 3 || chs[0].Monitored || chs[1].Monitored || !chs[2].Monitored {
				t.Fatalf("monitoring should start after the last read chapter: %+v", chs)
			}
			reloaded, _ := e.App.Imports.Get(e.Ctx, imp.ID)
			if reloaded.Status != model.ImportDone || reloaded.Options.ReaderID == 0 {
				t.Fatalf("import = %+v", reloaded)
			}
			var states []model.ChapterReadState
			_ = e.App.DB.NewSelect().Model(&states).Where("reader_id = ?", reloaded.Options.ReaderID).Order("chapter_id").Scan(e.Ctx)
			if len(states) != 6 { // One Piece 3, Existing 1, Dupe 2
				t.Fatalf("read states = %+v", states)
			}
			for _, st := range states {
				if st.Origin != model.ReadOriginBackup {
					t.Fatalf("origin = %+v", st)
				}
				if st.ChapterID == chs[0].ID && (st.ReadAt == nil || !st.ReadAt.Equal(readAt) || !st.Completed) {
					t.Fatalf("chapter 1 = %+v", st)
				}
				if st.ChapterID == chs[2].ID && (st.Completed || st.Page != 5) {
					t.Fatalf("chapter 3 = %+v", st)
				}
			}
			// chapter 3 (the first unread) was searched and downloaded
			waitFor(t, 20*time.Second, "chapter 3 downloaded", func() bool { return len(e.chapterFiles(t, ser.ID)) == 1 })
			if _, err := e.App.Imports.Run(e.Ctx, imp.ID, nil); err != nil {
				t.Fatal(err)
			}
			var n int64
			n, _ = e.App.DB.NewSelect().Model((*model.Series)(nil)).Count(e.Ctx)
			if n != 5 {
				t.Fatalf("running again must not add series twice: %d", n)
			}
			dupe := byTitle()["Dupe B"]
			if dupe.SeriesID == nil || byTitle()["Dupe A"].SeriesID == nil || *dupe.SeriesID != *byTitle()["Dupe A"].SeriesID {
				t.Fatalf("dupes weren't merged: %+v / %+v", dupe, byTitle()["Dupe A"])
			}
			var dchs []model.Chapter
			_ = e.App.DB.NewSelect().Model(&dchs).Where("series_id = ?", *dupe.SeriesID).Order("number_sort").Scan(e.Ctx)
			if len(dchs) != 3 || dchs[0].Monitored || dchs[1].Monitored || !dchs[2].Monitored {
				t.Fatalf("merged source must not bring back read chapters: %+v", dchs)
			}
		})
	}
}
