package app_test

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/testutil/fakelibrary"
)

// TestKomgaAPIProgress covers what reading apps write: KMReader's per-page
// PATCH, Mihon's tracker (tachiyomi v1/v2), marking books and series read or
// unread, and that app progress survives a library server sync.
func TestKomgaAPIProgress(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			f := newKomgaFixture(t, dsn)
			send := func(method, path, body string) {
				t.Helper()
				req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
				req.Header.Set("X-API-Key", f.key)
				req.Header.Set("User-Agent", "KMReader/2.1 CFNetwork")
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("%s %s: %d", method, path, resp.StatusCode)
				}
			}
			state := func(i int) *model.ChapterReadState {
				var st model.ChapterReadState
				if err := f.e.App.DB.NewSelect().Model(&st).Where("reader_id = ? AND chapter_id = ?", f.reader, f.chs[i].ID).Scan(f.e.Ctx); err != nil {
					return nil
				}
				return &st
			}
			tracker := func() map[string]any {
				var p map[string]any
				f.get(t, "/api/v2/series/"+sid(f.ser.ID)+"/read-progress/tachiyomi", &p)
				requireKeys(t, "tachiyomi", p, "booksCount", "booksReadCount", "booksUnreadCount", "booksInProgressCount",
					"lastReadContinuousNumberSort", "maxNumberSort")
				return p
			}
			series := sid(f.ser.ID)
			book := func(i int) string { return "/api/v1/books/" + sid(f.chs[i].ID) + "/read-progress" }

			// chapter 1 read, 2 on page 5
			if p := tracker(); p["booksCount"] != 4.0 || p["booksReadCount"] != 1.0 || p["booksInProgressCount"] != 1.0 ||
				p["booksUnreadCount"] != 2.0 || p["lastReadContinuousNumberSort"] != 1.0 || p["maxNumberSort"] != 4.0 {
				t.Fatalf("tracker %v", p)
			}

			// KMReader turns pages in chapter 3 (20 pages): fast, and the
			// last page finishes it
			start := time.Now()
			send("PATCH", book(2), `{"page":7,"completed":false}`)
			if d := time.Since(start); d > 500*time.Millisecond {
				t.Fatalf("PATCH took %v (KMReader gives up after 1 s)", d)
			}
			if st := state(2); st == nil || st.Page != 7 || st.Completed || st.Origin != model.ReadOriginApp {
				t.Fatalf("page 7: %+v", st)
			}
			send("PATCH", book(2), `{"page":20}`)
			if st := state(2); st == nil || !st.Completed || st.ReadAt == nil {
				t.Fatalf("last page: %+v", st)
			}
			// a page update doesn't un-finish it
			send("PATCH", book(2), `{"page":2,"completed":false}`)
			if st := state(2); !st.Completed || st.Page != 20 {
				t.Fatalf("un-finished: %+v", st)
			}
			// Paperback marks the undownloaded chapter 4 read
			send("PATCH", book(3), `{"completed":true}`)
			if st := state(3); st == nil || !st.Completed {
				t.Fatalf("chapter 4: %+v", st)
			}
			if p := tracker(); p["booksReadCount"] != 3.0 || p["lastReadContinuousNumberSort"] != 1.0 {
				t.Fatalf("tracker with a gap %v", p)
			}

			// Mihon's tracker: read up to chapter 2
			send("PUT", "/api/v2/series/"+series+"/read-progress/tachiyomi", `{"lastBookNumberSortRead":2}`)
			if p := tracker(); p["booksReadCount"] != 4.0 || p["lastReadContinuousNumberSort"] != 4.0 {
				t.Fatalf("after PUT %v", p)
			}
			// …which never lowers progress
			send("PUT", "/api/v2/series/"+series+"/read-progress/tachiyomi", `{"lastBookNumberSortRead":1}`)
			if p := tracker(); p["booksReadCount"] != 4.0 {
				t.Fatalf("PUT lowered progress %v", p)
			}

			// explicit unread
			send("DELETE", book(2), "")
			if state(2) != nil {
				t.Fatal("chapter 3 still read")
			}
			if p := tracker(); p["lastReadContinuousNumberSort"] != 2.0 {
				t.Fatalf("after unread %v", p)
			}
			var v1 map[string]any
			f.get(t, "/api/v1/series/"+series+"/read-progress/tachiyomi", &v1)
			if v1["lastReadContinuousIndex"] != 2.0 || v1["booksReadCount"] != 3.0 {
				t.Fatalf("v1 %v", v1)
			}
			send("PUT", "/api/v1/series/"+series+"/read-progress/tachiyomi", `{"lastBookRead":3}`)
			if st := state(2); st == nil || !st.Completed {
				t.Fatalf("v1 PUT: %+v", st)
			}

			// the whole series
			send("DELETE", "/api/v1/series/"+series+"/read-progress", "")
			if p := tracker(); p["booksReadCount"] != 0.0 || p["booksInProgressCount"] != 0.0 || p["lastReadContinuousNumberSort"] != 0.0 {
				t.Fatalf("series unread %v", p)
			}
			send("POST", "/api/v1/series/"+series+"/read-progress", "")
			if p := tracker(); p["booksReadCount"] != 4.0 {
				t.Fatalf("series read %v", p)
			}
			var b map[string]any
			f.get(t, "/api/v1/books/"+sid(f.chs[0].ID), &b)
			if rp, _ := b["readProgress"].(map[string]any); rp == nil || rp["completed"] != true {
				t.Fatalf("book progress %v", b["readProgress"])
			}

			// bad requests and unknown books
			req, _ := http.NewRequest("PATCH", f.srv.URL+book(0), strings.NewReader(`{}`))
			req.Header.Set("X-API-Key", f.key)
			if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 400 {
				t.Fatalf("empty PATCH: %d", resp.StatusCode)
			}
			req, _ = http.NewRequest("PATCH", f.srv.URL+"/api/v1/books/999999/read-progress", strings.NewReader(`{"completed":true}`))
			req.Header.Set("X-API-Key", f.key)
			if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 404 {
				t.Fatalf("unknown book: %d", resp.StatusCode)
			}

			// a library server that doesn't know chapter 3 yet doesn't clear
			// what the app wrote; chapter 1 (server-owned) follows the server
			lib := fakelibrary.NewScenario("progress-" + dialect)
			libDef := &model.ProviderDefinition{Kind: "library", Implementation: "fakelibrary", Name: "Komga", Enabled: true,
				Settings: map[string]any{"scenario": "progress-" + dialect}}
			if err := f.e.App.Modules.Create(f.e.Ctx, libDef); err != nil {
				t.Fatal(err)
			}
			acc := &model.ReaderAccount{ReaderID: f.reader, ModuleID: libDef.ID, Credentials: map[string]string{"apiKey": "k"}, CreatedAt: time.Now().UTC()}
			_, _ = f.e.App.DB.NewInsert().Model(acc).Exec(f.e.Ctx)
			var server model.ChapterReadState
			_ = f.e.App.DB.NewSelect().Model(&server).Where("reader_id = ? AND chapter_id = ?", f.reader, f.chs[0].ID).Scan(f.e.Ctx)
			_, _ = f.e.App.DB.NewUpdate().Model((*model.TitleReadState)(nil)).Set("origin = ''").Where("id = ?", server.ID).Exec(f.e.Ctx)
			lib.SetProgress("k", []library.BookProgress{})
			if _, err := f.e.App.ReadSync.SyncAccount(f.e.Ctx, acc); err != nil {
				t.Fatal(err)
			}
			if st := state(2); st == nil || !st.Completed || st.Origin != model.ReadOriginApp {
				t.Fatalf("app state cleared by the sync: %+v", st)
			}
			if state(0) != nil {
				t.Fatal("server-owned state survived the server's unread")
			}
			// once the server reports it, the server owns it
			lib.SetProgress("k", []library.BookProgress{{LocalPath: filepath.Join(f.e.Root, "Blue Period", "Blue Period Ch.3.cbz"), Completed: true}})
			if _, err := f.e.App.ReadSync.SyncAccount(f.e.Ctx, acc); err != nil {
				t.Fatal(err)
			}
			if st := state(2); st == nil || st.Origin != "" {
				t.Fatalf("server didn't take over: %+v", st)
			}
		})
	}
}
