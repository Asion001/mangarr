package anilist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecommendations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		for _, field := range []string{"relations", "recommendations(sort: RATING_DESC", "mediaRecommendation { type", "coverImage"} {
			if !strings.Contains(req.Query, field) {
				t.Errorf("query missing %s", field)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"Media":{"id":1,
"relations":{"edges":[
 {"relationType":"SEQUEL","node":{"id":2,"type":"MANGA","title":{"english":"Part Two"},"genres":["Drama"]}},
 {"relationType":"ADAPTATION","node":{"id":3,"type":"ANIME","title":{"english":"The Anime"}}},
 {"relationType":"CHARACTER","node":{"id":4,"type":"MANGA","title":{"english":"Cameo"}}}
]},
"recommendations":{"nodes":[
 {"rating":40,"mediaRecommendation":{"id":5,"type":"MANGA","title":{"english":"Liked Too"},"countryOfOrigin":"KR","coverImage":{"large":"https://img/5.jpg"}}},
 {"rating":12,"mediaRecommendation":{"id":2,"type":"MANGA","title":{"english":"Part Two"}}},
 {"rating":0,"mediaRecommendation":{"id":6,"type":"MANGA","title":{"english":"Disliked"}}},
 {"rating":9,"mediaRecommendation":null}
]}}}}`))
	}))
	defer server.Close()
	m := &Module{s: &Settings{TitleLanguage: "english", Endpoint: server.URL}, http: server.Client()}
	got, err := m.Recommendations(context.Background(), "1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Related) != 1 || got.Related[0].Title != "Part Two" || got.Related[0].Relation != "sequel" || got.Related[0].ID != "2" {
		t.Fatalf("related: %+v", got.Related)
	}
	// a related title isn't recommended again; unrated ones are left out
	if len(got.Recommended) != 1 || got.Recommended[0].Title != "Liked Too" || got.Recommended[0].Votes != 40 ||
		got.Recommended[0].Format != "manhwa" || got.Recommended[0].CoverURL != "https://img/5.jpg" || got.Recommended[0].Provider != "anilist" {
		t.Fatalf("recommended: %+v", got.Recommended)
	}
	if _, err := m.Recommendations(context.Background(), "x"); err == nil {
		t.Fatal("want an error for a bad id")
	}
}
