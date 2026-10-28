package mangadex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

const onePiece = "a1c7c817-4e59-43b7-9365-09675a149a6f"

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	raw, err := os.ReadFile("testdata/manga.json")
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent")
		}
		if q := r.URL.Query(); !slices.Contains(q["contentRating[]"], "pornographic") || !slices.Contains(q["includes[]"], "cover_art") {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/manga/" + onePiece:
			_, _ = w.Write(raw)
		case "/manga":
			if r.URL.Query().Get("title") != "One Piece" {
				t.Errorf("searched %q", r.URL.Query().Get("title"))
			}
			body := strings.Replace(string(raw), `"data": {`, `"data": [{`, 1)
			_, _ = w.Write([]byte(strings.TrimSuffix(strings.TrimSpace(body), "}") + "]}"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestGet(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	md, err := m.Get(context.Background(), onePiece)
	if err != nil {
		t.Fatal(err)
	}
	if md.Title != "One Piece" || md.Year != 1997 || md.Status != "ongoing" || md.Format != "manga" || md.Country != "JP" {
		t.Fatalf("got %+v", md)
	}
	wantTitles := map[string]string{model.TitleRomanized: "One Piece", model.TitleNative: "ワンピース", "uk": "Ван Піс", "ru": "One Piece. Большой куш", "en": "One Piece"}
	if !reflect.DeepEqual(md.Titles, wantTitles) {
		t.Errorf("titles %v", md.Titles)
	}
	wantIDs := map[string]string{"mangadex": onePiece, "anilist": "30013", "mal": "13", "mangaupdates": "55099564912"}
	if !reflect.DeepEqual(md.ExternalIDs, wantIDs) {
		t.Errorf("ids %v", md.ExternalIDs)
	}
	if !reflect.DeepEqual(md.Genres, []string{"Shounen", "Sci-Fi", "Action", "Comedy"}) || !reflect.DeepEqual(md.Tags, []string{"Monsters", "Animals"}) {
		t.Errorf("genres %v tags %v", md.Genres, md.Tags)
	}
	if len(md.Authors) != 1 || len(md.Artists) != 1 || !strings.HasPrefix(md.CoverURL, coversURL+onePiece+"/") {
		t.Errorf("authors %v artists %v cover %q", md.Authors, md.Artists, md.CoverURL)
	}
	if want := "From Viz:\nAs a child, Monkey D. Luffy was inspired to become a pirate.\n\nNote: won an award."; md.Description != want {
		t.Errorf("description %q", md.Description)
	}

	ru, err := m.GetLanguage(context.Background(), onePiece, "ru")
	if err != nil {
		t.Fatal(err)
	}
	if ru.Title != "One Piece. Большой куш" || ru.Description != "Описание." {
		t.Errorf("ru: %q %q", ru.Title, ru.Description)
	}
	if _, err := m.Get(context.Background(), "00000000-0000-0000-0000-000000000000"); !errors.Is(err, metadata.ErrNotFound) {
		t.Errorf("missing series: %v", err)
	}
	if _, err := m.Get(context.Background(), "30013"); err == nil {
		t.Error("an AniList id passed as a MangaDex one")
	}
}

func TestSearchLanguage(t *testing.T) {
	server := newServer(t)
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	list, err := m.SearchLanguage(context.Background(), "One Piece", "uk", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "Ван Піс" {
		t.Fatalf("got %+v", list)
	}
}

func TestCovers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cover" || r.URL.Query().Get("manga[]") != onePiece {
			t.Errorf("asked %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"data":[{"attributes":{"fileName":"b.jpg","volume":"110","locale":"ja"}},{"attributes":{"fileName":"a.png","volume":"1","locale":"uk"}},{"attributes":{"fileName":""}}]}`))
	}))
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	got, err := m.Covers(context.Background(), onePiece)
	if err != nil {
		t.Fatal(err)
	}
	want := []metadata.Cover{{URL: coversURL + onePiece + "/b.jpg", Label: "vol. 110"}, {URL: coversURL + onePiece + "/a.png", Label: "vol. 1 uk"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("covers %+v", got)
	}
}
