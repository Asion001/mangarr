package genres

import (
	"slices"
	"testing"
)

func TestNormalizeGivesOneNamePerGenre(t *testing.T) {
	got := Normalize([]string{"Romance", "Романтика", "Комедия", "Сэйнэн", "seinen", " Научная фантастика ", "Sci-Fi", "Сёдзё-ай", "Боевые искусства", "Isekai Villainess", ""})
	want := []string{"Romance", "Comedy", "Seinen", "Sci-Fi", "Shoujo Ai", "Martial Arts", "Isekai Villainess"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestEveryOtherNameIsOneGenres(t *testing.T) {
	seen := map[string]string{}
	for name, others := range known {
		for _, n := range append([]string{name}, others...) {
			if prev, ok := seen[key(n)]; ok && prev != name {
				t.Fatalf("%q names both %s and %s", n, prev, name)
			}
			seen[key(n)] = name
		}
	}
}
