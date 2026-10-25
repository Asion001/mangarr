package metadataagg

import (
	"reflect"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/source"
)

func TestMergePriorityAndUnion(t *testing.T) {
	a := metadata.SeriesMetadata{Provider: "anilist", ID: "1", Title: "Title A", Genres: []string{"Action"}, Status: "ongoing",
		ExternalIDs: map[string]string{"mal": "9"}}
	b := metadata.SeriesMetadata{Provider: "mangaupdates", ID: "x", Title: "Title B", Description: "desc B", Genres: []string{"action", "Drama"}, Publisher: "Pub"}
	src := FromSource(&source.MangaDetails{Manga: source.Manga{Title: "T"}, Author: "A, B", Genres: []string{"Content rating: Safe", "Comedy"}, Status: source.StatusUnknown})
	out, prov := Merge([]metadata.SeriesMetadata{a, b, src})
	if out.Title != "Title A" || out.Description != "desc B" || out.Publisher != "Pub" || out.Status != "ongoing" {
		t.Fatalf("scalars: %+v", out)
	}
	if len(out.Genres) != 3 || out.Genres[2] != "Comedy" {
		t.Fatalf("genres: %v", out.Genres)
	}
	if len(out.Authors) != 2 || prov["authors"] != "source" || prov["description"] != "mangaupdates" || prov["title"] != "anilist" {
		t.Fatalf("authors/provenance: %v %v", out.Authors, prov)
	}
	if out.ExternalIDs["anilist"] != "1" || out.ExternalIDs["mangaupdates"] != "x" || out.ExternalIDs["mal"] != "9" {
		t.Fatalf("ids: %v", out.ExternalIDs)
	}
	found := false
	for _, alt := range out.AltTitles {
		if alt == "Title B" {
			found = true
		}
	}
	if !found {
		t.Fatalf("alt titles: %v", out.AltTitles)
	}
}

func TestApplyRespectsLocks(t *testing.T) {
	s := &model.Series{Title: "My Title", Status: "ongoing", ReadingDirection: "rtl",
		Metadata: model.SeriesMetadata{Description: "mine", Locks: []string{"title", "description"}}}
	r := &Resolved{Metadata: metadata.SeriesMetadata{Title: "Other", Description: "theirs", Status: "completed", Format: "manhwa", Genres: []string{"A"}},
		Provenance: map[string]string{"title": "anilist", "description": "anilist", "genres": "anilist"}}
	if !Apply(s, r) {
		t.Fatal("expected change")
	}
	if s.Title != "My Title" || s.Metadata.Description != "mine" {
		t.Fatalf("locked fields changed: %+v", s)
	}
	if s.Status != "completed" || s.ReadingDirection != "webtoon" || len(s.Metadata.Genres) != 1 {
		t.Fatalf("unlocked fields not applied: %+v", s)
	}
	if s.Metadata.Provenance["title"] != "" || s.Metadata.Provenance["genres"] != "anilist" {
		t.Fatalf("provenance: %v", s.Metadata.Provenance)
	}
}

// While the main provider is down, a lower one fills only what the main one
// never gave: its title, description and names stay.
func TestApplyKeepsFieldsOfMissingProvider(t *testing.T) {
	s := &model.Series{Title: "Attack on Titan", Metadata: model.SeriesMetadata{Description: "English text",
		Titles:     map[string]string{"en": "Attack on Titan", "romanized": "Shingeki no Kyojin"},
		Provenance: map[string]string{"title": "anilist", "description": "anilist"}}}
	md, prov := Merge([]metadata.SeriesMetadata{{Provider: "shikimori", ID: "1", Title: "Атака титанов", Description: "Описание",
		Publisher: "Kodansha", Titles: map[string]string{"ru": "Атака титанов", "romanized": "Shingeki no Kyojin!"}}})
	Apply(s, &Resolved{Metadata: md, Provenance: prov, Missing: []string{"anilist"}})
	if s.Title != "Attack on Titan" || s.Metadata.Description != "English text" || s.Metadata.Provenance["title"] != "anilist" {
		t.Fatalf("main provider's fields changed: %q %q %v", s.Title, s.Metadata.Description, s.Metadata.Provenance)
	}
	if s.Metadata.Publisher != "Kodansha" || s.Metadata.Provenance["publisher"] != "shikimori" {
		t.Fatalf("missing field not filled: %+v", s.Metadata)
	}
	want := map[string]string{"en": "Attack on Titan", "romanized": "Shingeki no Kyojin", "ru": "Атака титанов"}
	if !reflect.DeepEqual(s.Metadata.Titles, want) {
		t.Fatalf("titles %v", s.Metadata.Titles)
	}
	// once it answers again, it updates as usual
	Apply(s, &Resolved{Metadata: md, Provenance: prov})
	if s.Title != "Атака титанов" || s.Metadata.Titles["romanized"] != "Shingeki no Kyojin!" {
		t.Fatalf("refresh with every provider: %q %v", s.Title, s.Metadata.Titles)
	}
}

func TestFindSame(t *testing.T) {
	list := []Candidate{{SeriesMetadata: metadata.SeriesMetadata{Provider: "anilist", ExternalIDs: map[string]string{"mal": "13"}}}}
	c := Candidate{SeriesMetadata: metadata.SeriesMetadata{Provider: "mangaupdates", ExternalIDs: map[string]string{"mal": "13"}}}
	if findSame(list, c) != 0 {
		t.Fatal("expected match by MAL id")
	}
	if Normalize("Re:Zero - Starting Life") != "rezerostartinglife" {
		t.Fatal(Normalize("Re:Zero - Starting Life"))
	}
}

func TestAdaptationsMergeAndApply(t *testing.T) {
	original := model.Adaptation{Title: "Cloud Lantern Screen", Format: "tv", ExternalIDs: map[string]string{"anilist": "700002"}}
	updated := model.Adaptation{Title: "Cloud Lantern Voyage", Format: "movie", ExternalIDs: map[string]string{"anilist": "700003"}}
	s := &model.Series{Metadata: model.SeriesMetadata{ExternalIDs: map[string]string{"anilist": "700001"}}}
	apply := func(parts ...metadata.SeriesMetadata) bool {
		md, prov := Merge(parts)
		return Apply(s, &Resolved{Metadata: md, Provenance: prov})
	}
	first := metadata.SeriesMetadata{Provider: "anilist", Adaptations: []model.Adaptation{original}}
	if !apply(metadata.SeriesMetadata{Provider: "shikimori"}, first) || len(s.Metadata.Adaptations) != 1 || s.Metadata.Provenance["adaptations"] != "anilist" {
		t.Fatalf("initial merge: %+v", s.Metadata)
	}
	if apply(first) {
		t.Fatal("unchanged adaptations reported changed")
	}
	if apply(metadata.SeriesMetadata{Provider: "shikimori"}) || len(s.Metadata.Adaptations) != 1 {
		t.Fatal("unavailable AniList cleared adaptations")
	}
	if !apply(metadata.SeriesMetadata{Provider: "anilist", Adaptations: []model.Adaptation{updated}}) || s.Metadata.Adaptations[0].Title != updated.Title {
		t.Fatal("refresh did not replace adaptations")
	}
	if !apply(metadata.SeriesMetadata{Provider: "anilist", Adaptations: []model.Adaptation{}}) || len(s.Metadata.Adaptations) != 0 {
		t.Fatal("empty refresh did not clear adaptations")
	}
	apply(first)
	s.Metadata.ExternalIDs = map[string]string{}
	if !apply(metadata.SeriesMetadata{Provider: "shikimori"}) || len(s.Metadata.Adaptations) != 0 || s.Metadata.Provenance["adaptations"] != "" {
		t.Fatal("relink retained adaptations")
	}
}
