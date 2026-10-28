package kitsu

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile("testdata/manga.json")
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.api+json")
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/manga/38":
			if q.Get("include") != "categories,mappings,staff.person" {
				t.Errorf("include %q", q.Get("include"))
			}
			_, _ = w.Write(raw)
		case r.URL.Path == "/mappings" && q.Get("include") != "item":
			_, _ = w.Write([]byte(`{"data":[{"id":"1","type":"mappings","relationships":{"item":{"links":{}}}}]}`))
		case r.URL.Path == "/mappings" && q.Get("filter[externalSite]") == "mangaupdates" && q.Get("filter[externalId]") == "pb8uwds",
			r.URL.Path == "/mappings" && q.Get("filter[externalSite]") == "anilist/manga" && q.Get("filter[externalId]") == "30013":
			_, _ = w.Write([]byte(`{"data":[{"id":"1","type":"mappings","relationships":{"item":{"data":{"type":"manga","id":"38"}}}}]}`))
		case r.URL.Path == "/mappings":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestGet(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	md, err := m.Get(context.Background(), "38")
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "One Piece" || md.Year != 1997 || md.Status != "ongoing" || md.Format != "manga" || md.Country != "JP" || md.CoverURL == "" {
		t.Fatalf("got %+v", md)
	}
	if md.Titles[model.TitleNative] != "ONE PIECE" || md.Titles[model.TitleRomanized] != "One Piece" || md.Titles["en"] != "One Piece" {
		t.Errorf("titles %v", md.Titles)
	}
	wantIDs := map[string]string{"kitsu": "38", "mal": "13", "anilist": "30013", "mangaupdates": "55099564912"}
	if !reflect.DeepEqual(md.ExternalIDs, wantIDs) {
		t.Errorf("ids %v", md.ExternalIDs)
	}
	if !reflect.DeepEqual(md.Authors, []string{"Eiichiro Oda"}) || !reflect.DeepEqual(md.Artists, []string{"Eiichiro Oda"}) {
		t.Errorf("authors %v artists %v", md.Authors, md.Artists)
	}
	// known genres stay genres; the rest of Kitsu's categories are tags
	if !reflect.DeepEqual(md.Genres, []string{"Comedy", "Super Power", "Fantasy", "Action", "Adventure", "Shounen", "Drama"}) ||
		!reflect.DeepEqual(md.Tags, []string{"Friendship", "Pirate"}) {
		t.Errorf("genres %v tags %v", md.Genres, md.Tags)
	}
}

func TestLookupExternal(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	for provider, id := range map[string]string{"anilist": "30013", "mangaupdates": "55099564912", "kitsu": "38"} {
		md, err := m.LookupExternal(context.Background(), provider, id)
		if err != nil || md.ID != "38" {
			t.Errorf("%s %s: %v %v", provider, id, md, err)
		}
	}
	if _, err := m.LookupExternal(context.Background(), "mal", "999"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unmapped id: %v", err)
	}
	if _, err := m.LookupExternal(context.Background(), "mangadex", "x"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("unknown provider: %v", err)
	}
}
