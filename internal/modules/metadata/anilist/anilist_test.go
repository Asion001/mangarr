package anilist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/modules/metadata"
)

func TestAdaptations(t *testing.T) {
	raw, err := os.ReadFile("testdata/adaptations.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"get", "search", "mal"} {
		t.Run(method, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Query string `json:"query"`
				}
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
				}
				if method == "search" {
					// A page of search results doesn't pay for relations.
					if strings.Contains(req.Query, "relations") {
						t.Error("search asked for relations")
					}
				} else {
					for _, field := range []string{"relations", "relationType", "idMal", "type format title", "startDate { year }", "coverImage { extraLarge large }"} {
						if !strings.Contains(req.Query, field) {
							t.Errorf("query missing %s", field)
						}
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if method == "search" {
					// AniList only returns the fields asked for.
					var item map[string]any
					_ = json.Unmarshal(raw, &item)
					delete(item, "relations")
					page, _ := json.Marshal(item)
					_, _ = w.Write(append(append([]byte(`{"data":{"Page":{"media":[`), page...), []byte(`]}}}`)...))
				} else {
					_, _ = w.Write(append(append([]byte(`{"data":{"Media":`), raw...), []byte(`}}`)...))
				}
			}))
			defer server.Close()
			m := &Module{s: &Settings{Endpoint: server.URL}, http: server.Client()}
			var got *metadata.SeriesMetadata
			var err error
			switch method {
			case "get":
				got, err = m.Get(context.Background(), "700001")
			case "mal":
				got, err = m.LookupExternal(context.Background(), "mal", "800001")
			case "search":
				var list []metadata.SeriesMetadata
				list, err = m.Search(context.Background(), "Cloud Lantern", 1)
				if err == nil && len(list) == 1 {
					got = &list[0]
				}
			}
			if err != nil || got == nil {
				t.Fatalf("metadata: %v, %v", got, err)
			}
			if method == "search" {
				// Not fetched, so a refresh from search leaves stored ones alone.
				if got.Adaptations != nil {
					t.Fatalf("search adaptations: %+v", got.Adaptations)
				}
				return
			}
			if len(got.Adaptations) != 6 {
				t.Fatalf("adaptations: %+v", got.Adaptations)
			}
			a := got.Adaptations[0]
			if a.Title != "Cloud Lantern Screen" || a.Format != "tv" || a.Year != 2021 || a.CoverURL != "https://images.example/screen-large.jpg" || a.ExternalIDs["anilist"] != "700002" || a.ExternalIDs["mal"] != "800002" {
				t.Fatalf("adaptation: %+v", a)
			}
			if a.Links["AniList"] != "https://anilist.co/anime/700002" || a.Links["MyAnimeList"] != "https://myanimelist.net/anime/800002" {
				t.Fatalf("links: %v", a.Links)
			}
			var formats []string
			for _, a := range got.Adaptations {
				formats = append(formats, a.Format)
			}
			if !reflect.DeepEqual(formats, []string{"tv", "movie", "ova", "ona", "special", "tv_short"}) {
				t.Fatal(formats)
			}
			movie := got.Adaptations[1]
			if movie.Title != "Cloud Lantern Voyage" || movie.CoverURL != "https://images.example/voyage.jpg" || movie.Year != 0 || len(movie.ExternalIDs) != 1 || len(movie.Links) != 1 {
				t.Fatalf("nullable fields: %+v", movie)
			}
			if got.ExternalIDs["anilist"] != "700001" || got.ExternalIDs["mal"] != "800001" {
				t.Fatalf("parent ids changed: %v", got.ExternalIDs)
			}
		})
	}
}

func TestAdaptationsEmptyAndTitles(t *testing.T) {
	m := &Module{s: &Settings{}}
	var md media
	if err := json.Unmarshal([]byte(`{"title":{"romaji":"Kumo no Rantan","native":"雲のランタン"},"relations":{"edges":[{"relationType":"ADAPTATION","node":{"id":700002,"type":"ANIME","format":"TV","title":{"native":"Lantern Native","romaji":"Lantern Romaji"}}}]}}`), &md); err != nil {
		t.Fatal(err)
	}
	got := m.convert(md)
	// no English name: the romaji one, and every name by language
	if got.Title != "Kumo no Rantan" || !reflect.DeepEqual(got.Titles, map[string]string{"romanized": "Kumo no Rantan", "native": "雲のランタン"}) {
		t.Fatal(got.Title, got.Titles)
	}
	if len(got.Adaptations) != 1 || got.Adaptations[0].Title != "Lantern Romaji" {
		t.Fatal(got.Adaptations)
	}
	for raw, fetched := range map[string]bool{`{}`: false, `{"relations":null}`: false, `{"relations":{"edges":[]}}`: true} {
		var empty media
		if err := json.Unmarshal([]byte(raw), &empty); err != nil {
			t.Fatal(err)
		}
		got := m.convert(empty)
		if len(got.Adaptations) != 0 || (got.Adaptations != nil) != fetched {
			t.Fatalf("%s: adaptations %#v, fetched %v", raw, got.Adaptations, fetched)
		}
	}
}

func TestRateLimit(t *testing.T) {
	var mu sync.Mutex
	var calls []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, time.Now())
		n := len(calls)
		mu.Unlock()
		// A high limit keeps the test quick; the first call is refused.
		w.Header().Set("X-RateLimit-Limit", "600")
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"data":{"Media":{"id":1}}}`))
	}))
	defer server.Close()
	m := &Module{s: &Settings{Endpoint: server.URL}, http: server.Client()}
	if _, err := m.Get(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 3 {
		t.Fatalf("calls = %d, want 3", len(calls))
	}
	if gap := calls[1].Sub(calls[0]); gap < time.Second {
		t.Errorf("retried after %v, before Retry-After", gap)
	}
	// 600 a minute is 100ms apart, plus the margin.
	if gap := calls[2].Sub(calls[1]); gap < 100*time.Millisecond || gap > 900*time.Millisecond {
		t.Errorf("calls %v apart, want the header's limit", gap)
	}
}
