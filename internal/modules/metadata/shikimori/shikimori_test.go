package shikimori

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

func TestSearchLanguageReturnsRussianTitles(t *testing.T) {
	var userAgent string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent = r.Header.Get("User-Agent")
		if r.URL.Path != "/api/mangas" || r.URL.Query().Get("search") != "Атака титанов" {
			t.Errorf("unexpected request %s?%s", r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":23390,"name":"Shingeki no Kyojin","russian":"Атака титанов","image":{"original":"/cover.jpg"},"url":"/mangas/23390","kind":"manga","status":"released","chapters":141,"aired_on":"2009-09-09"}]`))
	}))
	defer server.Close()

	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	list, err := m.SearchLanguage(context.Background(), "Атака титанов", "ru", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Title != "Атака титанов" || list[0].ExternalIDs["mal"] != "23390" {
		t.Fatalf("unexpected metadata: %+v", list)
	}
	if list[0].CoverURL != server.URL+"/cover.jpg" || userAgent != "mangarr-test" {
		t.Fatalf("cover=%q user-agent=%q", list[0].CoverURL, userAgent)
	}
}

func TestGetMapsDetailedMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":23390,"name":"Shingeki no Kyojin","russian":"Атака титанов","english":["Attack on Titan"],"japanese":["進撃の巨人"],"synonyms":["Вторжение титанов"],"image":{"original":"/cover.jpg"},"url":"/mangas/23390","kind":"manga","status":"released","chapters":141,"aired_on":"2009-09-09","description":"Описание","myanimelist_id":23390,"genres":[{"name":"Action","russian":"Экшен"}],"publishers":[{"name":"Kodansha"}],"score":"8.56","rates_scores_stats":[{"name":10,"value":900},{"name":9,"value":100}]}`))
	}))
	defer server.Close()

	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	got, err := m.Get(context.Background(), "23390")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ru": "Атака титанов", "en": "Attack on Titan", "romanized": "Shingeki no Kyojin", "native": "進撃の巨人"}
	if !reflect.DeepEqual(got.Titles, want) {
		t.Fatalf("titles %v", got.Titles)
	}
	if got.Title != "Attack on Titan" || got.Description != "Описание" || got.Publisher != "Kodansha" || got.Genres[0] != "Action" || got.TotalChapters != 141 || got.Status != "completed" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if want := (&model.Rating{Score: 8.56, Max: 10, Votes: 1000}); !reflect.DeepEqual(got.Rating, want) {
		t.Errorf("rating %+v", got.Rating)
	}
	if len(got.AltTitles) != 4 {
		t.Fatalf("expected all localized aliases, got %#v", got.AltTitles)
	}
}

func TestOutagePausesCallsButNotTest(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(522)
		_, _ = w.Write([]byte(`{"title":"Error 522","status":522,"retry_after":120,"ray_id":"abc"}`))
	}))
	defer server.Close()

	m := &Module{s: &Settings{Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	_, err := m.Search(context.Background(), "x", 1)
	if err == nil || err.Error() != "shikimori: HTTP 522 (connection timed out)" {
		t.Fatalf("first call: %v", err)
	}
	_, err = m.Search(context.Background(), "x", 1)
	var paused *metadata.PausedError
	if !errors.As(err, &paused) || hits != 1 {
		t.Fatalf("second call should fail fast: %v, %d requests", err, hits)
	}
	if err := m.Test(context.Background()); err == nil || hits != 2 {
		t.Fatalf("test should ask the server: %v, %d requests", err, hits)
	}
}

func TestOldDomainMovesToCurrent(t *testing.T) {
	for _, old := range []string{"https://shikimori.one", "https://shikimori.me/", ""} {
		impl, _ := modules.Lookup(modules.KindMetadata, "shikimori")
		inst, err := impl.New(modules.Deps{}, &Settings{Endpoint: old})
		if err != nil {
			t.Fatal(err)
		}
		if got := inst.(*Module).s.Endpoint; got != defaultEndpoint {
			t.Fatalf("%q became %q", old, got)
		}
	}
}
