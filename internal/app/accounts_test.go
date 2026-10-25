package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

// TestAccountsShareTheLibrary: two accounts read the same library with
// their own progress, through reading apps and the web; a group that
// excludes a tag doesn't see those series anywhere.
func TestAccountsShareTheLibrary(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			f := newKomgaFixture(t, dsn) // Blue Period (tagged "favourites", ch. 1 read by the fixture reader) and Another
			a := f.e.App
			ctx := f.e.Ctx
			boss, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"})
			if err != nil {
				t.Fatal(err)
			}
			// ann's group can't see series tagged "favourites"
			g := &model.Group{Name: "No favourites", Permissions: []string{"apps"}, IncludeTags: []int64{}, ExcludeTags: []int64{f.tag.ID},
				RootFolders: []int64{}, CreatedAt: time.Now().UTC()}
			if _, err := a.DB.NewInsert().Model(g).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			ann, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-12", GroupID: g.ID})
			if err != nil {
				t.Fatal(err)
			}
			bossKey, _, _ := a.Komga.CreateKey(ctx, boss.ID, "boss phone", "")
			annKey, _, _ := a.Komga.CreateKey(ctx, ann.ID, "ann ipad", "")

			komga := func(key, method, path, body string) (int, map[string]any) {
				req, _ := http.NewRequest(method, f.srv.URL+path, strings.NewReader(body))
				req.Header.Set("X-API-Key", key)
				req.Header.Set("Content-Type", "application/json")
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				var out map[string]any
				_ = json.NewDecoder(resp.Body).Decode(&out)
				return resp.StatusCode, out
			}
			// the library as each sees it
			if _, pg := komga(annKey, "GET", "/api/v1/series", ""); pg["totalElements"] != 1.0 {
				t.Fatalf("ann's series: %v", pg["totalElements"])
			}
			if code, _ := komga(annKey, "GET", "/api/v1/series/"+sid(f.ser.ID), ""); code != 404 {
				t.Fatalf("hidden series: %d", code)
			}
			if code, _ := komga(annKey, "GET", "/api/v1/books/"+sid(f.chs[0].ID)+"/pages", ""); code != 404 {
				t.Fatalf("hidden series' pages: %d", code)
			}
			if code, _ := komga(annKey, "PATCH", "/api/v1/books/"+sid(f.chs[0].ID)+"/read-progress", `{"completed":true}`); code != 404 {
				t.Fatalf("progress on a hidden series: %d", code)
			}
			if _, pg := komga(bossKey, "GET", "/api/v1/series", ""); pg["totalElements"] != 2.0 {
				t.Fatalf("boss's series: %v", pg["totalElements"])
			}
			// progress is per account: the fixture reader read ch. 1; boss hasn't
			if _, p := komga(bossKey, "GET", "/api/v2/series/"+sid(f.ser.ID)+"/read-progress/tachiyomi", ""); p["booksReadCount"] != 0.0 {
				t.Fatalf("boss starts unread: %v", p)
			}
			if code, _ := komga(bossKey, "PATCH", "/api/v1/books/"+sid(f.chs[2].ID)+"/read-progress", `{"completed":true}`); code != 204 {
				t.Fatalf("boss reads: %d", code)
			}
			var st model.ChapterReadState
			if err := a.DB.NewSelect().Model(&st).Where("chapter_id = ? AND user_id = ?", f.chs[2].ID, boss.ID).Scan(ctx); err != nil || !st.Completed {
				t.Fatalf("boss's progress on his account: %+v %v", st, err)
			}
			if n, _ := a.DB.NewSelect().Model((*model.ChapterReadState)(nil)).Where("chapter_id = ? AND user_id <> ?", f.chs[2].ID, boss.ID).Count(ctx); n != 0 {
				t.Fatal("boss's progress landed on another account")
			}
			// with a password too, and without the apps permission no access
			req, _ := http.NewRequest("GET", f.srv.URL+"/api/v2/users/me", nil)
			req.SetBasicAuth("ann", "ann-pass-12")
			resp, _ := http.DefaultClient.Do(req)
			var me map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&me)
			if resp.StatusCode != 200 || me["email"] != "ann" || me["id"] != sid(ann.ID) {
				t.Fatalf("ann's password login: %d %v", resp.StatusCode, me)
			}
			if _, err := a.DB.NewUpdate().Model(g).Set("permissions = ?", `[]`).WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
			a.Auth.Invalidate()
			a.Komga.InvalidateKeys()
			if code, _ := komga(annKey, "GET", "/api/v1/series", ""); code != 403 {
				t.Fatalf("without the apps permission: %d", code)
			}

			// the web, as ann
			srv := httptest.NewServer(api.New(a))
			defer srv.Close()
			jar, _ := cookiejar.New(nil)
			web := &http.Client{Jar: jar}
			if resp, err := web.Post(srv.URL+"/api/v1/auth/login", "application/json", strings.NewReader(`{"username":"ann","password":"ann-pass-12"}`)); err != nil || resp.StatusCode != 200 {
				t.Fatalf("web login: %v", err)
			}
			get := func(path string, out any) int {
				resp, err := web.Get(srv.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				if out != nil {
					_ = json.NewDecoder(resp.Body).Decode(out)
				}
				return resp.StatusCode
			}
			var list []api.SeriesResource
			get("/api/v1/series", &list)
			if len(list) != 1 || list[0].ID != f.other.ID {
				t.Fatalf("ann's web series: %d", len(list))
			}
			if code := get("/api/v1/series/"+sid(f.ser.ID), nil); code != 404 {
				t.Fatalf("hidden series on the web: %d", code)
			}
			if code := get("/api/v1/series/"+sid(f.ser.ID)+"/cover", nil); code != 404 {
				t.Fatalf("hidden cover: %d", code)
			}
			var chs []api.ChapterResource
			get("/api/v1/series/"+sid(f.other.ID)+"/chapters", &chs)
			for _, c := range chs {
				for _, r := range c.ReadBy {
					if r.UserID != ann.ID {
						t.Fatalf("ann sees someone else's progress: %+v", r)
					}
				}
			}
		})
	}
}
