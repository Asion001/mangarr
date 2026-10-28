package mangaupdates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/Asion001/mangarr/internal/modules/metadata"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile("testdata/series.json")
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/series/55099564912":
			_, _ = w.Write(raw)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/series/search":
			var req struct {
				Search string `json:"search"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Search != "One Piece" {
				t.Errorf("searched %q", req.Search)
			}
			// a search record has none of the details
			_, _ = w.Write([]byte(`{"results":[{"record":{"series_id":55099564912,"title":"One Piece","url":"https://www.mangaupdates.com/series/pb8uwds/one-piece","type":"Manga","year":"1997","genres":[{"genre":"Action"}]}},{"record":{"series_id":2,"title":"One Piece Party","type":"Manga"}}]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestGet(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	m := &Module{s: &Settings{MinCategoryVotes: 10, Endpoint: server.URL}, http: server.Client()}
	// the base36 slug in the site's urls is the same series
	for _, id := range []string{"55099564912", "pb8uwds"} {
		md, err := m.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if md.ID != "55099564912" || md.Title != "One Piece" || md.Year != 1997 || md.Status != "ongoing" ||
			md.Format != "manga" || md.Country != "JP" || md.Publisher != "Shueisha" || md.TotalChapters != 1196 {
			t.Fatalf("got %+v", md)
		}
		if !reflect.DeepEqual(md.Authors, []string{"ODA Eiichiro"}) || !reflect.DeepEqual(md.Artists, []string{"ODA Eiichiro"}) {
			t.Errorf("authors %v artists %v", md.Authors, md.Artists)
		}
		if want := []string{"Adapted to Anime", "Absent Parent/s", "Abusive Authority Figure/s", "Abuse of Power"}; !reflect.DeepEqual(md.Tags, want) {
			t.Errorf("tags %v, want %v", md.Tags, want)
		}
		if want := "As a child, Monkey D. Luffy was inspired to become a pirate.\n\nNote: Won an award in 2012."; md.Description != want {
			t.Errorf("description %q", md.Description)
		}
		if md.ExternalIDs["mangaupdates"] != "55099564912" || md.CoverURL == "" || len(md.AltTitles) != 3 {
			t.Errorf("ids %v cover %q alt %v", md.ExternalIDs, md.CoverURL, md.AltTitles)
		}
	}
	if _, err := m.Get(context.Background(), "1"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("missing series: %v", err)
	}
	if _, err := m.LookupExternal(context.Background(), "anilist", "30013"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("anilist id: %v", err)
	}
}

func TestSearch(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL}, http: server.Client()}
	list, err := m.Search(context.Background(), "One Piece", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "One Piece" || list[0].Status != "" || list[0].Year != 1997 {
		t.Fatalf("got %+v", list)
	}
}

func TestStatus(t *testing.T) {
	for in, want := range map[string]string{
		"115 Volumes (Ongoing)":            "ongoing",
		"14 Volumes (Complete)":            "completed",
		"3 Volumes (Hiatus)":               "hiatus",
		"2 Volumes (Discontinued)":         "cancelled",
		"10 Volumes (Complete) 2 Side (?)": "completed",
		"":                                 "unknown",
	} {
		if got := status(in, false); got != want {
			t.Errorf("status(%q) = %q, want %q", in, got, want)
		}
	}
}
