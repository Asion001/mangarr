package moodscore

import "testing"

func pagesOf(moods ...int64) []Scores {
	out := make([]Scores, len(moods))
	for i, m := range moods {
		if m == 0 {
			out[i] = Scores{}
			continue
		}
		out[i] = Scores{1: 0.1, 2: 0.1, 3: 0.1}
		out[i][m] = 0.8
	}
	return out
}

func TestChanges(t *testing.T) {
	// calm opening, a single action page (noise), then a real fight
	pages := pagesOf(1, 1, 1, 1, 2, 1, 1, 1, 2, 2, 2, 2, 2, 2, 2, 2)
	got := Changes(pages, 0)
	if len(got) != 2 || got[0] != (Change{1, 1, got[0].Confidence}) || got[1].Mood != 2 || got[1].Page < 9 || got[1].Page > 11 {
		t.Fatalf("changes %+v", got)
	}
	if got[0].Confidence < 0.5 || got[0].Confidence > 1 {
		t.Fatalf("confidence %v", got[0].Confidence)
	}
	// carried in: the chapter doesn't restart its mood
	if got := Changes(pagesOf(2, 2, 2, 2, 2), 2); len(got) != 0 {
		t.Fatalf("a chapter going on as the last ended: %+v", got)
	}
	// a short burst doesn't switch
	if got := Changes(pagesOf(1, 1, 1, 3, 3, 1, 1, 1, 1), 1); len(got) != 0 {
		t.Fatalf("a two-page burst switched: %+v", got)
	}
	// unscored pages are passed over
	if got := Changes(pagesOf(0, 0, 3, 3, 3, 3, 3), 1); len(got) != 1 || got[0].Mood != 3 {
		t.Fatalf("with gaps: %+v", got)
	}
	// unsure pages set nothing
	flat := []Scores{{1: 0.34, 2: 0.33, 3: 0.33}, {1: 0.33, 2: 0.34, 3: 0.33}, {1: 0.33, 2: 0.33, 3: 0.34}}
	if got := Changes(flat, 0); len(got) != 1 || got[0].Page != 1 {
		t.Fatalf("an unsure opening: %+v", got)
	}
	// ten moods: a clear page scores about 0.3, its neighbours less
	ten := func(m int64, v float64) Scores {
		s := Scores{}
		for id := int64(1); id <= 10; id++ {
			s[id] = (1 - v) / 9
		}
		s[m] = v
		return s
	}
	var many []Scores
	for range 6 {
		many = append(many, ten(1, 0.3))
	}
	for range 6 {
		many = append(many, ten(4, 0.3))
	}
	if got := Changes(many, 0); len(got) != 2 || got[0].Mood != 1 || got[1].Mood != 4 {
		t.Fatalf("ten moods: %+v", got)
	}
	var even []Scores
	for range 8 {
		even = append(even, ten(3, 0.12))
	}
	if got := Changes(even, 1); len(got) != 0 {
		t.Fatalf("ten moods, none clear: %+v", got)
	}
	if got := Changes(nil, 0); len(got) != 0 {
		t.Fatalf("no pages: %+v", got)
	}
}
