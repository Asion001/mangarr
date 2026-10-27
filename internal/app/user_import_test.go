package app_test

import (
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/readinglists"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
	"github.com/Asion001/mangarr/internal/userdata"
	"github.com/Asion001/mangarr/internal/userimport"
)

// TestUserImport: a user brings a library from another app. A title on the
// server gets their progress and a follow at once; one that isn't becomes a
// request, and its progress is written once the request brings it in.
func TestUserImport(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			chapters := func(url string, n int) []fakesource.Chapter {
				var out []fakesource.Chapter
				for i := 1; i <= n; i++ {
					out = append(out, fakesource.Chapter{URL: url + "/" + string(rune('0'+i)), Name: "Chapter", Number: float64(i), Uploaded: time.Now(), Pages: 4})
				}
				return out
			}
			sc := fakesource.NewScenario("userimport-" + dialect)
			sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
			sc.Account, sc.AccountToken = &source.Account{}, "x" // the site reads accounts (and lists chapters for them)
			sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/bl", Title: "Blue Lock", Status: source.StatusOngoing, Chapters: chapters("/bl", 3)})
			sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/k8", Title: "Kaiju No. 8", Status: source.StatusOngoing, Chapters: chapters("/k8", 2)})
			e := newTestApp(t, dsn)
			src := e.addFakeModule(t, "userimport-"+dialect)
			meta := &model.ProviderDefinition{Kind: "metadata", Implementation: "fakemeta", Name: "Meta", Enabled: true}
			if err := e.App.Modules.Create(e.Ctx, meta); err != nil {
				t.Fatal(err)
			}
			ctx := e.Ctx
			ann, err := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
			if err != nil {
				t.Fatal(err)
			}
			pAnn, _ := e.App.Auth.UserPrincipal(ctx, ann.ID)
			bl, err := e.App.Series.Add(ctx, series.AddRequest{Title: "Blue Lock", RootFolderID: e.RFID, Monitor: model.MonitorNone,
				Sources: []series.SourceLink{{ModuleID: src, SourceID: "A", URL: "/bl"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": bl.ID})

			b := &backupimport.Backup{Format: backupimport.FormatAccount, Sources: map[string]string{"A": "Source A"}, Entries: []backupimport.BackupManga{
				{SourceID: "A", URL: "/bl", Title: "Blue Lock", Favorite: true, ReadUpTo: &backupimport.ReadUpTo{Chapter: 3, Page: 2},
					Categories: []string{"Читаю", "Любимое", "Sports"}},
				{SourceID: "A", URL: "/k8", Title: "Kaiju No. 8", Favorite: true, Chapters: []backupimport.BackupChapter{{Number: 1, Read: true}},
					Categories: []string{"В планах"}},
			}}
			// the site's own names for its lists map to the standard ones
			d := userdata.FromBackup(b, "ann", map[string]string{"Читаю": source.ListReading, "В планах": source.ListPlanned, "Любимое": source.ListFavorite, "Sports": ""})
			if t0, t1 := d.Titles[0], d.Titles[1]; t0.Status != model.ReadReading || !t0.Favorite || len(t0.Lists) != 1 || t0.Lists[0] != "Sports" || t1.Status != model.ReadPlanned {
				t.Fatalf("lists = %+v / %+v", d.Titles[0], d.Titles[1])
			}
			svc := &userdata.Service{DB: e.App.DB}
			matches, err := svc.Preview(ctx, d, nil)
			if err != nil || matches[0].SeriesID != bl.ID || matches[1].SeriesID != 0 || matches[0].Reached == nil || matches[0].Read != 2 {
				t.Fatalf("preview = %+v %v", matches, err)
			}
			acc := userdata.Account{UserID: ann.ID}
			if _, err := svc.Import(ctx, acc, d, userdata.DataImportOptions{Progress: true, Follows: true, Lists: true}); err != nil {
				t.Fatal(err)
			}
			entry := func(seriesID int64) readinglists.TitleLists {
				ser, err := e.App.Series.Get(ctx, seriesID)
				if err != nil {
					t.Fatal(err)
				}
				got, err := readinglists.Get(ctx, e.App.DB, ann.ID, readstate.TitleID(ser))
				if err != nil {
					t.Fatal(err)
				}
				return got
			}
			lists, err := readinglists.Lists(ctx, e.App.DB, ann.ID)
			if err != nil || len(lists) != 2 || lists[0].Kind != model.ListFavorites || lists[1].Name != "Sports" {
				t.Fatalf("ann's lists = %+v %v", lists, err)
			}
			if got := entry(bl.ID); got.Status != model.ReadReading || len(got.Lists) != 2 {
				t.Fatalf("blue lock lists = %+v", got)
			}
			states := func(seriesID int64) map[float64]model.ChapterReadState {
				var chs []model.Chapter
				_ = e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", seriesID).Scan(ctx)
				var sts []model.ChapterReadState
				_ = e.App.DB.NewSelect().Model(&sts).Where("user_id = ? AND series_id = ?", ann.ID, seriesID).Scan(ctx)
				out := map[float64]model.ChapterReadState{}
				for _, st := range sts {
					for _, c := range chs {
						if c.ID == st.ChapterID {
							out[c.NumberSort] = st
						}
					}
				}
				return out
			}
			if n, ok := readstate.ChapterNumberByPlace(ctx, e.App.DB, bl.ID, 2); !ok || n != 2 {
				t.Fatalf("second chapter = %v %v", n, ok)
			}
			if _, ok := readstate.ChapterNumberByPlace(ctx, e.App.DB, bl.ID, 9); ok {
				t.Fatal("no ninth chapter")
			}
			got := states(bl.ID)
			if len(got) != 3 || !got[1].Completed || !got[2].Completed || got[3].Completed || got[3].Page != 2 {
				t.Fatalf("blue lock = %+v", got)
			}
			if n, _ := e.App.DB.NewSelect().Model((*model.Follow)(nil)).Where("user_id = ? AND series_id = ?", ann.ID, bl.ID).Count(ctx); n != 1 {
				t.Fatal("ann follows what she imported")
			}

			res, err := e.App.UserImport.Request(ctx, pAnn, []userdata.ExportTitle{d.Titles[1]})
			if err != nil || res.Requested != 1 {
				t.Fatalf("request = %+v %v", res, err)
			}
			var req model.Request
			if err := e.App.DB.NewSelect().Model(&req).Scan(ctx); err != nil || req.Title != "Kaiju No. 8" || req.Status != model.RequestPending {
				t.Fatalf("request = %+v %v", req, err)
			}
			// an admin adds it; once its chapters are listed ann's progress is there
			k8, err := e.App.Series.Add(ctx, series.AddRequest{Metadata: &metadataagg.Ref{ModuleID: meta.ID, Provider: "fakemeta", ID: "7"},
				Sources: []series.SourceLink{{ModuleID: src, SourceID: "A", URL: "/k8"}}, RootFolderID: e.RFID, Monitor: model.MonitorNone})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": k8.ID})
			waitFor(t, 10*time.Second, "kept progress written", func() bool {
				_ = e.App.UserImport.ApplyReady(ctx)
				st := states(k8.ID)
				return st[1].Completed
			})
			if got := entry(k8.ID); got.Status != model.ReadPlanned {
				t.Fatalf("kaiju lists = %+v", got)
			}
			if n, _ := e.App.DB.NewSelect().Model((*model.RequestedProgress)(nil)).Count(ctx); n != 0 {
				t.Fatalf("kept progress left behind: %d", n)
			}

			// bob's site kept only the place of the chapter reached: it is
			// looked up in the site's chapter list
			bob, _ := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "bob-pass-1"})
			placed := userdata.ExportTitle{Title: "Blue Lock", Sources: []userdata.ExportSource{{SourceID: "A", URL: "/bl"}},
				Reached: &userdata.ExportReached{Chapter: -1, ChapterRef: "gone", ChapterPlace: 3}}
			if n, err := e.App.UserImport.Place(ctx, bob.ID, []userimport.Placement{{SeriesID: bl.ID, Title: placed}}, 0); err != nil || n != 1 {
				t.Fatalf("place = %d %v", n, err)
			}
			if n, _ := e.App.DB.NewSelect().Model((*model.ChapterReadState)(nil)).Where("user_id = ? AND series_id = ? AND completed", bob.ID, bl.ID).Count(ctx); n != 2 {
				t.Fatalf("bob read %d chapters", n)
			}
		})
	}
}
