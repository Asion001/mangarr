package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/testutil/fakelibrary"
)

// TestProgressHub: mangarr between reading apps and library servers.
// App progress is logged per device and pushed to the servers (unreads
// too); what servers report is logged per server, with lower reports kept
// as conflicts; the sync health endpoint shows all of it.
func TestProgressHub(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			f := newKomgaFixture(t, dsn)
			f.e.App.FanOut.SetDelays(50*time.Millisecond, 20*time.Millisecond)
			lib := fakelibrary.NewScenario("hub-" + dialect)
			libDef := &model.ProviderDefinition{Kind: "library", Implementation: "fakelibrary", Name: "Komga", Enabled: true,
				Settings: map[string]any{"scenario": "hub-" + dialect}}
			if err := f.e.App.Modules.Create(f.e.Ctx, libDef); err != nil {
				t.Fatal(err)
			}
			acc := &model.LibraryAccount{UserID: f.reader, ModuleID: libDef.ID, Credentials: map[string]string{"apiKey": "k"}, CreatedAt: time.Now().UTC()}
			if _, err := f.e.App.DB.NewInsert().Model(acc).Exec(f.e.Ctx); err != nil {
				t.Fatal(err)
			}
			path := func(i int) string {
				return filepath.Join(f.e.Root, "Blue Period", "Blue Period Ch."+sid(int64(i+1))+".cbz")
			}
			// the server has chapter 1 read (like mangarr)
			lib.SetProgress("k", []library.BookProgress{{LocalPath: path(0), Completed: true}})

			send := func(method, path, body string) {
				t.Helper()
				req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
				req.Header.Set("X-API-Key", f.key)
				req.Header.Set("User-Agent", "KMReader/2.1")
				resp, err := http.DefaultClient.Do(req)
				if err != nil || resp.StatusCode != 204 {
					t.Fatalf("%s %s: %v", method, path, err)
				}
			}
			written := func() []library.BookProgress { return lib.WrittenFor("k") }

			// reading chapter 3 in KMReader: page turns and the last page
			for _, p := range []string{`{"page":3}`, `{"page":4}`, `{"page":20}`} {
				send("PATCH", "/api/v1/books/"+sid(f.chs[2].ID)+"/read-progress", p)
			}
			var evs []model.ReadEvent
			_ = f.e.App.DB.NewSelect().Model(&evs).Where("chapter_id = ?", f.chs[2].ID).Scan(f.e.Ctx)
			if len(evs) != 1 || !evs[0].Completed || evs[0].Page != 20 || evs[0].Client != "KMReader" || evs[0].Device != "test" ||
				evs[0].Origin != model.EventOriginApp || evs[0].Outcome != model.OutcomeApplied {
				t.Fatalf("page turns should be one event: %+v", evs)
			}
			// …reaches the library server
			waitFor(t, 5*time.Second, "chapter 3 pushed", func() bool {
				for _, w := range written() {
					if w.LocalPath == path(2) && w.Completed {
						return true
					}
				}
				return false
			})
			// unread in the app clears it on the server too
			send("DELETE", "/api/v1/books/"+sid(f.chs[0].ID)+"/read-progress", "")
			waitFor(t, 5*time.Second, "unread pushed", func() bool {
				for _, w := range written() {
					if w.LocalPath == path(0) && w.Unread {
						return true
					}
				}
				return false
			})

			// the server reports: chapter 2 read (applied), chapter 3 only on
			// page 2 (lower: mangarr keeps its own)
			lib.SetProgress("k", []library.BookProgress{{LocalPath: path(1), Completed: true}, {LocalPath: path(2), Page: 2}})
			for range 2 { // the same conflict on the next sync stays one event
				if _, err := f.e.App.ReadSync.SyncAccount(f.e.Ctx, acc); err != nil {
					t.Fatal(err)
				}
			}
			var server []model.ReadEvent
			_ = f.e.App.DB.NewSelect().Model(&server).Where("origin = ?", model.EventOriginServer).Order("id").Scan(f.e.Ctx)
			outcomes := map[string]int{}
			for _, e := range server {
				if e.Client != "Komga" {
					t.Fatalf("server event client %q", e.Client)
				}
				outcomes[e.Outcome]++
			}
			if outcomes[model.OutcomeApplied] != 1 || outcomes[model.OutcomeKept] != 1 {
				t.Fatalf("server events %+v", server)
			}

			// sync health
			srv := httptest.NewServer(api.New(f.e.App))
			defer srv.Close()
			g, _ := f.e.App.Settings.General(f.e.Ctx)
			req, _ := http.NewRequest("GET", srv.URL+"/api/v1/users/"+sid(f.reader)+"/sync", nil)
			req.Header.Set("X-Api-Key", g.APIKey)
			resp, err := http.DefaultClient.Do(req)
			if err != nil || resp.StatusCode != 200 {
				t.Fatalf("sync health: %v", err)
			}
			var health api.ProgressSync
			_ = json.NewDecoder(resp.Body).Decode(&health)
			resp.Body.Close()
			if len(health.Keys) != 1 || len(health.Devices) != 2 || len(health.Events) == 0 {
				t.Fatalf("health %+v", health)
			}
			byClient := map[string]int{}
			for _, d := range health.Devices {
				byClient[d.Client] = d.Kept
				if d.Last == nil || d.Last.SeriesTitle != "Blue Period" || d.Last.Chapter == "" {
					t.Fatalf("device %+v", d)
				}
			}
			if kept, ok := byClient["Komga"]; !ok || kept != 1 {
				t.Fatalf("devices %+v", health.Devices)
			}
			if _, ok := byClient["KMReader"]; !ok {
				t.Fatalf("devices %+v", health.Devices)
			}
		})
	}
}
