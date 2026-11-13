package app_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

func TestCrossPointCatalog(t *testing.T) {
	for _, dsn := range dbtest.DSNs(t) {
		t.Run(dsn, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			page := image.NewGray(image.Rect(0, 0, 600, 900))
			for i := range page.Pix {
				page.Pix[i] = 0xff
			}
			page.SetGray(300, 450, color.Gray{0})
			var jb bytes.Buffer
			if err := jpeg.Encode(&jb, page, nil); err != nil {
				t.Fatal(err)
			}
			addSeries := func(title, direction string, chapters int, added time.Time) []*model.Chapter {
				s := &model.Series{Title: title, SortTitle: strings.ToLower(title), Status: model.StatusOngoing, MonitorNew: model.MonitorAll, RootFolderID: e.RFID, Path: strings.ReplaceAll(strings.ToLower(title), " ", "-"), ProfileID: 1, Tags: []int64{}, Language: "en", ReadingDirection: direction, AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(s).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				dir, err := e.App.Library.SeriesDir(e.Ctx, s)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				var out []*model.Chapter
				for n := 1; n <= chapters; n++ {
					ch := &model.Chapter{SeriesID: s.ID, NumberKey: fmt.Sprint(n), NumberSort: float64(n), State: model.ChapterImported, FirstSeenAt: added.Add(time.Duration(n) * time.Minute), UpdatedAt: now}
					if _, err := e.App.DB.NewInsert().Model(ch).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("chapter-%d.cbz", n)
					var zb bytes.Buffer
					zw := zip.NewWriter(&zb)
					fw, _ := zw.Create("0001.jpg")
					_, _ = fw.Write(jb.Bytes())
					_ = zw.Close()
					if err := os.WriteFile(filepath.Join(dir, name), zb.Bytes(), 0o644); err != nil {
						t.Fatal(err)
					}
					f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: s.ID, RelativePath: name, Size: int64(zb.Len()), PageCount: 1, Format: "cbz", ImportedAt: now}
					if _, err := e.App.DB.NewInsert().Model(f).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					ch.FileID = &f.ID
					if _, err := e.App.DB.NewUpdate().Model(ch).Column("file_id").WherePK().Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					out = append(out, ch)
				}
				return out
			}
			manga := addSeries("Paper Lantern", "rtl", 3, now.Add(-2*time.Hour))
			addSeries("Long Scroll", "webtoon", 2, now)

			groups, err := e.App.Auth.EnsureGroups(e.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			user, err := e.App.Auth.CreateUser(e.Ctx, auth.NewUser{Username: "reader", Password: "reader-password-1", GroupID: groups[model.GroupUsers]})
			if err != nil {
				t.Fatal(err)
			}
			key, _, err := e.App.Komga.CreateKey(e.Ctx, user.ID, "CrossPoint", "CrossPoint")
			if err != nil {
				t.Fatal(err)
			}
			call := func(method, path, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("X-API-Key", key)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				e.App.Komga.Handler().ServeHTTP(rec, req)
				return rec
			}
			type row struct {
				ID       int64  `json:"id"`
				Title    string `json:"title"`
				Subtitle string `json:"subtitle"`
			}
			rows := func(query string) []row {
				t.Helper()
				rec := call(http.MethodGet, "/crosspoint/v1/chapters?"+query, "")
				if rec.Code != 200 {
					t.Fatalf("%s: %d %s", query, rec.Code, rec.Body.String())
				}
				var page struct {
					Items []row `json:"items"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				return page.Items
			}

			if got := rows("list=continue"); len(got) != 0 {
				t.Fatalf("nothing started yet: %+v", got)
			}
			// The library leaves the webtoon out and offers the first chapter.
			lib := rows("list=library&page=1&limit=8")
			if len(lib) != 1 || lib[0].ID != manga[0].ID || lib[0].Title != "Paper Lantern · Ch. 1" || lib[0].Subtitle != "0 of 3 read · 3 new · Ongoing" {
				t.Fatalf("library: %+v", lib)
			}
			if got := rows("list=library&webtoons=1"); len(got) != 2 {
				t.Fatalf("library with webtoons: %+v", got)
			}
			if got := rows("list=search&q=scroll"); len(got) != 0 {
				t.Fatalf("search found a webtoon: %+v", got)
			}
			if got := rows("list=search&q=lantern"); len(got) != 1 {
				t.Fatalf("search: %+v", got)
			}
			if got := rows("list=library&page=2&limit=8"); len(got) != 0 {
				t.Fatalf("page 2: %+v", got)
			}
			recent := rows("list=recent")
			if len(recent) != 3 || recent[0].ID != manga[2].ID {
				t.Fatalf("recent: %+v", recent)
			}

			// Reading chapter 1 moves the title to Continue reading at chapter 2.
			if rec := call(http.MethodPatch, fmt.Sprintf("/api/v1/books/%d/read-progress", manga[0].ID), `{"completed":true}`); rec.Code != http.StatusNoContent {
				t.Fatalf("mark read: %d %s", rec.Code, rec.Body.String())
			}
			cont := rows("list=continue")
			if len(cont) != 1 || cont[0].ID != manga[1].ID || cont[0].Subtitle != "1 of 3 read · 2 new · Ongoing" {
				t.Fatalf("continue: %+v", cont)
			}
			if got := rows("list=library"); len(got) != 1 || got[0].ID != manga[1].ID {
				t.Fatalf("library after reading: %+v", got)
			}
			if rec := call(http.MethodGet, "/crosspoint/v1/chapters?list=shelves", ""); rec.Code != http.StatusBadRequest {
				t.Fatalf("unknown list: %d", rec.Code)
			}

			// The row's file: an XTCH laid out for the screen.
			rec := call(http.MethodGet, fmt.Sprintf("/crosspoint/v1/chapters/%d/xtch?screen=480x800", cont[0].ID), "")
			if rec.Code != 200 || !bytes.HasPrefix(rec.Body.Bytes(), []byte("XTCH")) {
				t.Fatalf("xtch: %d %q", rec.Code, rec.Body.String()[:min(rec.Body.Len(), 40)])
			}
			// The device's reader.exit event: the last page shown marks it read.
			post := func(body string) {
				t.Helper()
				if rec := call(http.MethodPost, "/crosspoint/v1/progress", body); rec.Code != http.StatusNoContent {
					t.Fatalf("progress %s: %d %s", body, rec.Code, rec.Body.String())
				}
			}
			post(fmt.Sprintf(`{"chapter":%d,"percent":"40","end_percent":"50"}`, manga[1].ID))
			if got := rows("list=continue"); got[0].ID != manga[1].ID {
				t.Fatalf("half read moved on: %+v", got)
			}
			post(fmt.Sprintf(`{"chapter":"%d","percent":"95","end_percent":"100"}`, manga[1].ID))
			if got := rows("list=continue"); len(got) != 1 || got[0].ID != manga[2].ID || got[0].Subtitle != "2 of 3 read · 1 new · Ongoing" {
				t.Fatalf("after finishing: %+v", got)
			}
			// Opening it again later doesn't mark it unread.
			post(fmt.Sprintf(`{"chapter":%d,"percent":0,"end_percent":10}`, manga[1].ID))
			if got := rows("list=continue"); got[0].ID != manga[2].ID {
				t.Fatalf("re-read moved back: %+v", got)
			}
			for _, bad := range []string{`{}`, `{"chapter":"x","end_percent":1}`, `{"chapter":1}`} {
				if rec := call(http.MethodPost, "/crosspoint/v1/progress", bad); rec.Code != http.StatusBadRequest {
					t.Fatalf("%s: %d", bad, rec.Code)
				}
			}
			if rec := call(http.MethodPost, "/crosspoint/v1/progress", `{"chapter":999999,"end_percent":100}`); rec.Code != http.StatusNotFound {
				t.Fatalf("unknown chapter: %d", rec.Code)
			}

			// The plugin folder is public, and its catalog URLs work as the
			// device fills them in (an unset screen arrives as "{cfg.screen}").
			zrec := httptest.NewRecorder()
			e.App.Komga.Handler().ServeHTTP(zrec, httptest.NewRequest(http.MethodGet, "/crosspoint/plugin.zip", nil))
			if zrec.Code != 200 {
				t.Fatalf("plugin zip: %d", zrec.Code)
			}
			zr, err := zip.NewReader(bytes.NewReader(zrec.Body.Bytes()), int64(zrec.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{}
			for _, f := range zr.File {
				rc, _ := f.Open()
				var b bytes.Buffer
				_, _ = b.ReadFrom(rc)
				_ = rc.Close()
				files[f.Name] = b.Bytes()
			}
			for _, name := range []string{"mangarr/manifest.json", "mangarr/device.json", "mangarr/plugin.js", "mangarr/README.md"} {
				if len(files[name]) == 0 {
					t.Fatalf("plugin zip lacks %s: %v", name, zr.File)
				}
			}
			var manifest struct {
				Browse struct {
					URL   string `json:"url"`
					Lists []struct {
						URL string `json:"url"`
					} `json:"lists"`
				} `json:"browse"`
				Download struct {
					URL string `json:"url"`
				} `json:"download"`
				Events map[string]struct {
					Request struct {
						URL  string `json:"url"`
						Body string `json:"body"`
					} `json:"request"`
				} `json:"events"`
			}
			if err := json.Unmarshal(files["mangarr/device.json"], &manifest); err != nil {
				t.Fatal(err)
			}
			fillIn := strings.NewReplacer("{cfg.server}", "", "{page}", "1", "{limit}", "8", "{cfg.webtoons}", "0", "{id}", fmt.Sprint(manga[2].ID), "{meta.mangarr_chapter}", fmt.Sprint(manga[2].ID), "{event.percent}", "10", "{event.end_percent}", "20")
			urls := []string{manifest.Browse.URL}
			for _, l := range manifest.Browse.Lists {
				if l.URL != "" {
					urls = append(urls, l.URL)
				}
			}
			for _, u := range urls {
				if got := call(http.MethodGet, fillIn.Replace(u), ""); got.Code != 200 || !strings.Contains(got.Body.String(), `"items"`) {
					t.Fatalf("plugin list %s: %d %s", u, got.Code, got.Body.String())
				}
			}
			if got := call(http.MethodGet, fillIn.Replace(manifest.Download.URL), ""); got.Code != 200 || !bytes.HasPrefix(got.Body.Bytes(), []byte("XTCH")) {
				t.Fatalf("plugin download %s: %d", manifest.Download.URL, got.Code)
			}
			for name, ev := range manifest.Events {
				if got := call(http.MethodPost, fillIn.Replace(ev.Request.URL), fillIn.Replace(ev.Request.Body)); got.Code != http.StatusNoContent {
					t.Fatalf("plugin event %s: %d %s", name, got.Code, got.Body.String())
				}
			}

			unauth := httptest.NewRecorder()
			e.App.Komga.Handler().ServeHTTP(unauth, httptest.NewRequest(http.MethodGet, "/crosspoint/v1/chapters", nil))
			if unauth.Code != http.StatusUnauthorized {
				t.Fatalf("no key: %d", unauth.Code)
			}
		})
	}
}
