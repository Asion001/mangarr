package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

// TestLocalizedTitles checks that the library lists a title by its edition
// in the interface language, and that genres stored in other languages get
// their English names.
func TestLocalizedTitles(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			work := &model.Work{Title: "Abyss", SortTitle: "abyss", CreatedAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(work).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			add := func(name, lang string, genres ...string) *model.Series {
				ser := &model.Series{WorkID: work.ID, Title: name, SortTitle: name, Status: model.StatusOngoing, MonitorNew: model.MonitorAll,
					RootFolderID: e.RFID, Path: name, ProfileID: 1, Language: lang, Tags: []int64{}, AddedAt: now, UpdatedAt: now,
					Metadata: model.SeriesMetadata{Genres: genres}}
				if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				return ser
			}
			add("Abyss EN", "en", "Romance")
			ru := add("Бездна", "ru", "Романтика", "Комедия", "Romance")

			if n, err := e.App.Series.NormalizeAllGenres(e.Ctx); err != nil || n != 1 {
				t.Fatalf("normalized %d series (%v), want the russian one", n, err)
			}
			stored, _ := e.App.Series.Get(e.Ctx, ru.ID)
			if !slices.Equal(stored.Metadata.Genres, []string{"Romance", "Comedy"}) {
				t.Fatalf("genres %q, want English names once", stored.Metadata.Genres)
			}

			srv := httptest.NewServer(api.New(e.App))
			defer srv.Close()
			g, _ := e.App.Settings.General(e.Ctx)
			list := func(lang string) api.SeriesResource {
				t.Helper()
				req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/series", nil)
				req.Header.Set("X-Api-Key", g.APIKey)
				req.Header.Set("Accept-Language", lang)
				resp, err := http.DefaultClient.Do(req)
				if err != nil || resp.StatusCode != 200 {
					t.Fatalf("series list: %v %v", err, resp.StatusCode)
				}
				defer resp.Body.Close()
				var out []api.SeriesResource
				if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out) != 1 {
					t.Fatalf("library %+v (%v), want one title", out, err)
				}
				return out[0]
			}
			if r := list("ru"); r.Title != "Бездна" || r.ID != ru.ID || r.WorkTitle != "Abyss" || len(r.Editions) != 2 {
				t.Fatalf("russian interface: %q (series %d, work %q), want the russian edition", r.Title, r.ID, r.WorkTitle)
			}
			if r := list("de"); r.Title != "Abyss" {
				t.Fatalf("german interface: %q, want the title's own name", r.Title)
			}
		})
	}
}
