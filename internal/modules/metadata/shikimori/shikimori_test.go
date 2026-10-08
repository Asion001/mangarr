package shikimori

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
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

	m := &Module{s: &Settings{TitleLanguage: "english", Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
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
		_, _ = w.Write([]byte(`{"id":23390,"name":"Shingeki no Kyojin","russian":"Атака титанов","english":["Attack on Titan"],"japanese":["進撃の巨人"],"synonyms":["Вторжение титанов"],"image":{"original":"/cover.jpg"},"url":"/mangas/23390","kind":"manga","status":"released","chapters":141,"aired_on":"2009-09-09","description":"Описание","myanimelist_id":23390,"genres":[{"name":"Action","russian":"Экшен"}],"publishers":[{"name":"Kodansha"}]}`))
	}))
	defer server.Close()

	m := &Module{s: &Settings{TitleLanguage: "russian", Endpoint: server.URL, UserAgent: "mangarr-test"}, http: server.Client()}
	got, err := m.Get(context.Background(), "23390")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Атака титанов" || got.Description != "Описание" || got.Publisher != "Kodansha" || got.Genres[0] != "Action" || got.TotalChapters != 141 || got.Status != "completed" {
		t.Fatalf("unexpected metadata: %+v", got)
	}
	if len(got.AltTitles) != 4 {
		t.Fatalf("expected all localized aliases, got %#v", got.AltTitles)
	}
}
