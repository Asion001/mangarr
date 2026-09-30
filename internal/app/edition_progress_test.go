package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/Asion001/mangarr/internal/testutil/fakelibrary"
)

// TestEditionProgress checks that a title's language editions share one
// reading progress by chapter number, and that progress follows an edition
// when it is grouped with or separated from a title.
func TestEditionProgress(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			work := &model.Work{Title: "Abyss", SortTitle: "abyss", CreatedAt: now, UpdatedAt: now}
			other := &model.Work{Title: "Other", SortTitle: "other", CreatedAt: now, UpdatedAt: now}
			for _, w := range []*model.Work{work, other} {
				if _, err := e.App.DB.NewInsert().Model(w).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
			}
			chapters := map[string]map[float64]int64{}
			add := func(name, lang string, workID int64, numbers ...float64) *model.Series {
				ser := &model.Series{WorkID: workID, Title: name, SortTitle: name, Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
					RootFolderID: e.RFID, Path: name, ProfileID: 1, Language: lang, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				chapters[name] = map[float64]int64{}
				for _, n := range numbers {
					key := strconv.FormatFloat(n, 'f', -1, 64)
					c := &model.Chapter{SeriesID: ser.ID, NumberKey: key, NumberSort: n, Title: "Ch " + key, Monitored: true,
						State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
					if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					chapters[name][n] = c.ID
				}
				return ser
			}
			en := add("Abyss EN", "en", work.ID, 1, 2, 3, 4)
			ru := add("Abyss RU", "ru", work.ID, 1, 2, 3)
			add("Other", "en", other.ID, 1)
			reader := &model.Reader{Name: "ann", CountForCleanup: true, CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			state := func(series string, n float64) *model.ChapterReadState {
				var st model.ChapterReadState
				if err := e.App.DB.NewSelect().Model(&st).Where("reader_id = ? AND chapter_id = ?", reader.ID, chapters[series][n]).Scan(e.Ctx); err != nil {
					return nil
				}
				return &st
			}
			by := reading.By{Origin: model.EventOriginApp, Client: "test"}
			record := func(c reading.Change) []reading.Outcome {
				t.Helper()
				out, err := e.App.Reading.Record(e.Ctx, reader.ID, []reading.Change{c}, by)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}

			var mu sync.Mutex
			announced := map[int64][]int64{} // series → chapters
			e.App.Bus.Subscribe(func(ev events.Event) {
				if p, ok := ev.Payload.(reading.ProgressPayload); ok {
					mu.Lock()
					announced[ev.SeriesID] = append(announced[ev.SeriesID], p.ChapterIDs...)
					mu.Unlock()
				}
			}, reading.ProgressChanged)

			// finishing English 1 finishes Russian 1, and only that
			out := record(reading.Change{ChapterID: chapters["Abyss EN"][1], SeriesID: en.ID, Completed: true})
			if len(out) != 1 {
				t.Fatalf("outcomes %+v, want the reported chapter only", out)
			}
			if st := state("Abyss RU", 1); st == nil || !st.Completed || st.SeriesID != ru.ID || st.SourceChapterID != chapters["Abyss EN"][1] {
				t.Fatalf("russian 1 = %+v, want read from the english chapter", st)
			}
			if st := state("Other", 1); st != nil {
				t.Fatalf("another title's chapter 1 = %+v, want untouched", st)
			}
			if st := state("Abyss EN", 4); st != nil {
				t.Fatalf("english 4 = %+v, want untouched", st)
			}
			var rows int
			rows, _ = e.App.DB.NewSelect().Model((*model.TitleReadState)(nil)).Count(e.Ctx)
			if rows != 1 {
				t.Fatalf("%d stored states, want one for the title", rows)
			}
			events, _ := e.App.DB.NewSelect().Model((*model.ReadEvent)(nil)).Where("reader_id = ?", reader.ID).Count(e.Ctx)
			if events != 1 {
				t.Fatalf("%d read events, want only the reported one", events)
			}
			// the Russian edition's apps and servers hear about it
			waitFor(t, 2*time.Second, "russian 1 announced", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return len(announced[ru.ID]) == 1 && announced[ru.ID][0] == chapters["Abyss RU"][1] && len(announced[en.ID]) == 1
			})

			// where you are in Russian 2 is where you are in English 2
			record(reading.Change{ChapterID: chapters["Abyss RU"][2], SeriesID: ru.ID, Page: 5})
			if st := state("Abyss EN", 2); st == nil || st.Completed || st.Page != 5 {
				t.Fatalf("english 2 = %+v, want page 5", st)
			}
			record(reading.Change{ChapterID: chapters["Abyss EN"][2], SeriesID: en.ID, Completed: true})
			if st := state("Abyss RU", 2); st == nil || !st.Completed {
				t.Fatalf("russian 2 = %+v, want read", st)
			}

			// unreading English 1 unreads Russian 1
			record(reading.Change{ChapterID: chapters["Abyss EN"][1], SeriesID: en.ID, Unread: true})
			if st := state("Abyss RU", 1); st != nil {
				t.Fatalf("russian 1 = %+v, want unread", st)
			}

			// an edition read on its own brings its progress into the title
			// it joins, and keeps it when separated again
			de := add("Abyss DE", "de", 0, 1, 3)
			if err := e.App.Series.SetWork(e.Ctx, de.ID, 0); err != nil {
				t.Fatal(err)
			}
			record(reading.Change{ChapterID: chapters["Abyss DE"][3], SeriesID: de.ID, Completed: true})
			if st := state("Abyss EN", 3); st != nil {
				t.Fatalf("english 3 = %+v before grouping, want unread", st)
			}
			if err := e.App.Series.SetWork(e.Ctx, de.ID, work.ID); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"Abyss EN", "Abyss RU", "Abyss DE"} {
				if st := state(name, 3); st == nil || !st.Completed {
					t.Fatalf("%s 3 = %+v after grouping, want read", name, st)
				}
			}
			if st := state("Abyss DE", 1); st != nil {
				t.Fatalf("german 1 = %+v, want unread like the title", st)
			}
			if err := e.App.Series.SetWork(e.Ctx, de.ID, 0); err != nil {
				t.Fatal(err)
			}
			if st := state("Abyss DE", 3); st == nil || !st.Completed {
				t.Fatalf("german 3 = %+v after separating, want still read", st)
			}
			if st := state("Abyss RU", 3); st == nil || !st.Completed {
				t.Fatalf("russian 3 = %+v after separating german, want still read", st)
			}
		})
	}
}

// TestEditionProgressLibraryServer checks a library server's reports against
// a title with two editions: a book finished in one edition isn't undone by
// the other edition's book being unread or behind on the server, and is
// unread only when the server unreads the book it was reported on.
func TestEditionProgressLibraryServer(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			work := &model.Work{Title: "Abyss", SortTitle: "abyss", CreatedAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(work).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			chapter := map[string]int64{}
			path := map[string]string{}
			for _, lang := range []string{"en", "ru"} {
				ser := &model.Series{WorkID: work.ID, Title: "Abyss " + lang, SortTitle: "abyss", Status: model.StatusOngoing, Monitored: true,
					MonitorNew: model.MonitorAll, RootFolderID: e.RFID, Path: "Abyss " + lang, ProfileID: 1, Language: lang, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				c := &model.Chapter{SeriesID: ser.ID, NumberKey: "1", NumberSort: 1, Monitored: true, State: model.ChapterImported, FirstSeenAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				cf := &model.ChapterFile{ChapterID: c.ID, SeriesID: ser.ID, RelativePath: "Ch.1.cbz", Size: 1, PageCount: 20, Format: "cbz", ImportedAt: now}
				if _, err := e.App.DB.NewInsert().Model(cf).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				c.FileID = &cf.ID
				if _, err := e.App.DB.NewUpdate().Model(c).Column("file_id").WherePK().Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				chapter[lang], path[lang] = c.ID, filepath.Join(e.Root, ser.Path, cf.RelativePath)
			}
			reader := &model.Reader{Name: "ann", CountForCleanup: true, CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			lib := fakelibrary.NewScenario("editions-" + dialect)
			libDef := &model.ProviderDefinition{Kind: "library", Implementation: "fakelibrary", Name: "Komga", Enabled: true,
				Settings: map[string]any{"scenario": "editions-" + dialect}}
			if err := e.App.Modules.Create(e.Ctx, libDef); err != nil {
				t.Fatal(err)
			}
			acc := &model.ReaderAccount{ReaderID: reader.ID, ModuleID: libDef.ID, Credentials: map[string]string{"apiKey": "k"}, CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(acc).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			sync := func(progress ...library.BookProgress) {
				t.Helper()
				lib.SetProgress("k", progress)
				if _, err := e.App.ReadSync.SyncAccount(e.Ctx, acc); err != nil {
					t.Fatal(err)
				}
			}
			read := func(lang string) bool {
				var st model.ChapterReadState
				err := e.App.DB.NewSelect().Model(&st).Where("reader_id = ? AND chapter_id = ?", reader.ID, chapter[lang]).Scan(e.Ctx)
				return err == nil && st.Completed
			}

			// the server has English 1 read and doesn't report Russian 1
			sync(library.BookProgress{LocalPath: path["en"], Completed: true})
			if !read("en") || !read("ru") {
				t.Fatalf("after the english book was read: en=%v ru=%v, want both read", read("en"), read("ru"))
			}
			// the same report again: Russian 1 being unread there clears nothing
			sync(library.BookProgress{LocalPath: path["en"], Completed: true})
			if !read("en") || !read("ru") {
				t.Fatalf("after a second sync: en=%v ru=%v, want both read", read("en"), read("ru"))
			}
			// nor does Russian 1 being only started there
			sync(library.BookProgress{LocalPath: path["en"], Completed: true}, library.BookProgress{LocalPath: path["ru"], Page: 2})
			if !read("en") || !read("ru") {
				t.Fatalf("after the russian book was started: en=%v ru=%v, want both read", read("en"), read("ru"))
			}
			// English 1 unread on the server unreads the title's chapter
			sync(library.BookProgress{LocalPath: path["ru"], Page: 2})
			if read("en") || read("ru") {
				t.Fatalf("after the english book was unread: en=%v ru=%v, want both unread", read("en"), read("ru"))
			}
		})
	}
}

// TestEditionsSharedInfo checks that a title's editions show the first
// edition's cover and facts but keep their own text, that the library and
// the Continue reading shelf show the title once, and that its chapters are
// counted once.
func TestEditionsSharedInfo(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			work := &model.Work{Title: "Abyss", SortTitle: "abyss", CreatedAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(work).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			add := func(lang string, md model.SeriesMetadata, status string, chapters int) (*model.Series, []int64) {
				ser := &model.Series{WorkID: work.ID, Title: "Abyss " + lang, SortTitle: "abyss", Status: status, Monitored: true,
					MonitorNew: model.MonitorAll, RootFolderID: e.RFID, Path: "Abyss " + lang, ProfileID: 1, Language: lang, Tags: []int64{},
					Metadata: md, AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				var ids []int64
				for i := 1; i <= chapters; i++ {
					c := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(i), NumberSort: float64(i), Monitored: true,
						State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
					if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					ids = append(ids, c.ID)
				}
				return ser, ids
			}
			en, enCh := add("en", model.SeriesMetadata{Description: "A girl descends.", Year: 2012, Authors: []string{"Akihito Tsukushi"},
				Genres: []string{"Adventure"}, CoverURL: "https://covers.example/en.jpg"}, model.StatusOngoing, 3)
			ru, ruCh := add("ru", model.SeriesMetadata{Description: "Девочка спускается.", Year: 2013, Genres: []string{"Приключения"},
				Publisher: "Истари", CoverURL: "https://covers.example/ru.jpg", Locks: []string{"publisher"}}, model.StatusUnknown, 4)
			for _, ser := range []*model.Series{en, ru} {
				dir := filepath.Join(e.Root, ser.Path)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("cover "+ser.Language), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if n, err := e.App.Series.ShareMetadata(e.Ctx, work.ID); err != nil || n != 1 {
				t.Fatalf("shared %d, %v; want the russian edition changed", n, err)
			}
			got, err := e.App.Series.Get(e.Ctx, ru.ID)
			if err != nil {
				t.Fatal(err)
			}
			md := got.Metadata
			if got.Status != model.StatusOngoing || md.Year != 2012 || len(md.Genres) != 1 || md.Genres[0] != "Adventure" ||
				len(md.Authors) != 1 || md.CoverURL != "https://covers.example/en.jpg" {
				t.Fatalf("russian edition %q %+v, want the english edition's facts", got.Status, md)
			}
			if got.Title != "Abyss ru" || md.Description != "Девочка спускается." || md.Publisher != "Истари" {
				t.Fatalf("russian edition %q %+v, want its own text and its locked publisher", got.Title, md)
			}
			if data, _ := os.ReadFile(filepath.Join(e.Root, ru.Path, "cover.jpg")); string(data) != "cover en" {
				t.Fatalf("russian cover %q, want the english one", data)
			}
			if n, err := e.App.Series.ShareMetadata(e.Ctx, work.ID); err != nil || n != 0 {
				t.Fatalf("shared again %d, %v; want nothing to change", n, err)
			}

			// English 1 read, Russian 2 started: one title, read in Russian last
			reader := &model.Reader{Name: "ann", CountForCleanup: true, CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			by := reading.By{Origin: model.EventOriginApp, Client: "test"}
			for _, c := range []reading.Change{
				{ChapterID: enCh[0], SeriesID: en.ID, Completed: true},
				{ChapterID: ruCh[1], SeriesID: ru.ID, Page: 3},
			} {
				if _, err := e.App.Reading.Record(e.Ctx, reader.ID, []reading.Change{c}, by); err != nil {
					t.Fatal(err)
				}
				time.Sleep(10 * time.Millisecond) // the second report is the later one
			}
			deck, err := e.App.Reading.OnDeck(e.Ctx, reader.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(deck) != 1 || deck[0].Series.Series.ID != ru.ID || deck[0].Book.Chapter.ID != ruCh[1] {
				t.Fatalf("continue reading %+v, want the title once, in russian at chapter 2", deck)
			}

			srv := httptest.NewServer(api.New(e.App))
			defer srv.Close()
			g, _ := e.App.Settings.General(e.Ctx)
			req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/series", nil)
			req.Header.Set("X-Api-Key", g.APIKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("series list: %v %v", err, resp.StatusCode)
			}
			defer resp.Body.Close()
			var list []api.SeriesResource
			if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 || len(list[0].Editions) != 2 {
				t.Fatalf("library %+v, want one title with two editions", list)
			}
			if st := list[0].Stats; st.ChapterCount != 4 || st.ReadCount != 1 || st.InProgressCount != 1 {
				t.Fatalf("title stats %+v, want 4 chapters, 1 read, 1 started", st)
			}
		})
	}
}
