package model

import "testing"

func TestScore(t *testing.T) {
	md := SeriesMetadata{Ratings: map[string]Rating{
		"anilist":      {Score: 84, Max: 100, Votes: 900},
		"mangaupdates": {Score: 8.6, Max: 10},
		"kitsu":        {Score: 40, Max: 100, Votes: 3}, // too few people
	}}
	if got, ok := md.Score(nil); !ok || got < 8.49 || got > 8.51 {
		t.Errorf("average %v %v", got, ok)
	}
	if got, ok := md.Score([]string{"mangadex", "kitsu", "anilist"}); !ok || got != 4 {
		t.Errorf("first by order %v %v", got, ok)
	}
	if _, ok := md.Score([]string{"mangadex"}); ok {
		t.Error("score from a provider with none")
	}
	if _, ok := (SeriesMetadata{}).Score(nil); ok {
		t.Error("score without ratings")
	}
}
