package api_test

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
)

// TestUpdatesInbox: the feed starts with everything seen, new updates count
// as unseen, filters narrow both the list and what "mark all" marks, one
// update can be marked on its own, and marking everything moves the cursor.
func TestUpdatesInbox(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			ctx := t.Context()
			_, ring := logging.Setup("error", io.Discard)
			a, err := app.New(ctx, &config.Config{DataDir: t.TempDir(), DB: dsn, AuthDisabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			srv := httptest.NewServer(api.New(a))
			defer srv.Close()
			c := caller{t, http.DefaultClient, srv.URL}
			now := time.Now().UTC()
			root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
			if _, err := a.DB.NewInsert().Model(root).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			addSeries := func(title, lang string, added time.Time) *model.Series {
				s := &model.Series{Title: title, SortTitle: title, Language: lang, RootFolderID: root.ID, ProfileID: 1, Path: title, Tags: []int64{}, AddedAt: added, UpdatedAt: now}
				if _, err := a.DB.NewInsert().Model(s).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				return s
			}
			en := addSeries("Tin Rivers", "en", now.Add(-48*time.Hour))
			ru := addSeries("Mednye Reki", "ru", now.Add(-48*time.Hour))
			addChapter := func(s *model.Series, n int, at time.Time, release bool) int64 {
				ch := &model.Chapter{SeriesID: s.ID, NumberKey: fmt.Sprint(n), NumberSort: float64(n), State: model.ChapterMissing, FirstSeenAt: at, UpdatedAt: at}
				if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				if release {
					ss := &model.SeriesSource{SeriesID: s.ID, ModuleID: 1, SourceID: fmt.Sprint("src", n), MangaURL: "/m", NextCheckAt: now, CreatedAt: now}
					if _, err := a.DB.NewInsert().Model(ss).Exec(ctx); err != nil {
						t.Fatal(err)
					}
					rel := &model.ChapterRelease{SeriesID: s.ID, ChapterID: &ch.ID, SeriesSourceID: ss.ID, ChapterURL: fmt.Sprint("/c", n), CreatedAt: now}
					if _, err := a.DB.NewInsert().Model(rel).Exec(ctx); err != nil {
						t.Fatal(err)
					}
				}
				return ch.ID
			}
			addChapter(en, 1, now.Add(-24*time.Hour), false) // before anyone looked

			type page struct {
				Items []struct {
					Kind      string `json:"kind"`
					ChapterID int64  `json:"chapterId"`
					SeriesID  int64  `json:"seriesId"`
					Seen      bool   `json:"seen"`
					At        string `json:"at"`
				} `json:"items"`
				Total       int     `json:"total"`
				Unseen      int     `json:"unseen"`
				SeenThrough *string `json:"seenThrough"`
			}
			get := func(query string) page {
				t.Helper()
				var p page
				if code := c.do("GET", "/api/v1/updates?"+query, "", &p); code != http.StatusOK {
					t.Fatalf("updates?%s: %d", query, code)
				}
				return p
			}
			unseen := func() int {
				t.Helper()
				var u struct{ Count int }
				if code := c.do("GET", "/api/v1/updates/unseen", "", &u); code != http.StatusOK {
					t.Fatalf("unseen: %d", code)
				}
				return u.Count
			}

			first := get("")
			if first.Total != 3 || first.Unseen != 0 || !first.Items[0].Seen || !first.Items[2].Seen || first.SeenThrough == nil {
				t.Fatalf("first look: %+v", first)
			}

			later := time.Now().UTC().Add(time.Second)
			a1 := addChapter(en, 2, later, true)
			addChapter(en, 3, later.Add(time.Second), false)
			addChapter(ru, 1, later.Add(2*time.Second), false)
			addSeries("Glass Hills", "en", later.Add(3*time.Second))

			if p := get("unseen=true"); p.Total != 4 || p.Unseen != 4 {
				t.Fatalf("unseen list: %+v", p)
			}
			if n := unseen(); n != 4 {
				t.Fatalf("unseen count %d", n)
			}
			if p := get("unseen=true&availability=streamable"); p.Total != 1 || p.Items[0].ChapterID != a1 {
				t.Fatalf("streamable: %+v", p)
			}
			if p := get("language=ru"); p.Total != 2 || p.Unseen != 1 { // the title (seen) and its new chapter
				t.Fatalf("russian: %+v", p)
			}
			if p := get(fmt.Sprintf("seriesId=%d&kind=chapter", en.ID)); p.Total != 3 || p.Unseen != 2 {
				t.Fatalf("one title: %+v", p)
			}
			if p := get("followed=true"); p.Total != 0 {
				t.Fatalf("nobody follows without a login: %+v", p)
			}

			// one chapter on its own
			if code := c.do("POST", "/api/v1/updates/seen", fmt.Sprintf(`{"items":[{"kind":"chapter","id":%d,"at":%q}]}`, a1, later.Format(time.RFC3339Nano)), nil); code != http.StatusOK {
				t.Fatalf("mark one: %d", code)
			}
			if n := unseen(); n != 3 {
				t.Fatalf("after marking one: %d", n)
			}
			all := get("")
			for _, it := range all.Items {
				if it.ChapterID == a1 && !it.Seen {
					t.Fatalf("marked chapter still new: %+v", all)
				}
			}
			// everything Russian
			if code := c.do("POST", "/api/v1/updates/seen", `{"all":true,"language":"ru"}`, nil); code != http.StatusOK {
				t.Fatalf("mark russian: %d", code)
			}
			if n := unseen(); n != 2 {
				t.Fatalf("after marking Russian: %d", n)
			}
			// the rest
			if code := c.do("POST", "/api/v1/updates/seen", `{"all":true}`, nil); code != http.StatusOK {
				t.Fatalf("mark all: %d", code)
			}
			if n := unseen(); n != 0 {
				t.Fatalf("after marking all: %d", n)
			}
			left, err := a.DB.NewSelect().Model((*model.UpdateSeenItem)(nil)).Count(ctx)
			if err != nil || left != 0 {
				t.Fatalf("single marks left under the cursor: %d, %v", left, err)
			}
			if code := c.do("POST", "/api/v1/updates/seen", `{"items":[{"kind":"page","id":1}]}`, nil); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
				t.Fatalf("bad kind: %d", code)
			}

			// a new title in two languages: marked seen through its Russian
			// edition, it's seen without the filter too
			at := time.Now().UTC().Add(5 * time.Second)
			w := &model.Work{Title: "Iron Bay", SortTitle: "Iron Bay", CreatedAt: at, UpdatedAt: at}
			if _, err := a.DB.NewInsert().Model(w).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			e1, e2 := addSeries("Iron Bay", "en", at), addSeries("Zheleznaya Bukhta", "ru", at)
			if _, err := a.DB.NewUpdate().Model((*model.Series)(nil)).Set("work_id = ?", w.ID).Where("id IN (?, ?)", e1.ID, e2.ID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if n := unseen(); n != 1 {
				t.Fatalf("new work: %d", n)
			}
			p := get("unseen=true&language=ru")
			if p.Total != 1 || p.Items[0].SeriesID != e2.ID {
				t.Fatalf("russian edition: %+v", p)
			}
			if code := c.do("POST", "/api/v1/updates/seen", fmt.Sprintf(`{"items":[{"kind":"series","id":%d,"at":%q}]}`, e2.ID, p.Items[0].At), nil); code != http.StatusOK {
				t.Fatalf("mark the edition: %d", code)
			}
			if n := unseen(); n != 0 {
				t.Fatalf("work marked through one edition, unseen %d", n)
			}
			if p := get(""); !p.Items[0].Seen {
				t.Fatalf("work not shown seen: %+v", p.Items[0])
			}
		})
	}
}
