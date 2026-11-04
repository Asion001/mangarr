package moodscore

// Change is a mood a chapter switches to from a page on (1-based).
type Change struct {
	Page       int     `json:"page"`
	Mood       int64   `json:"moodId"`
	Confidence float64 `json:"confidence"`
}

// How changes are found.
const (
	// window is how many pages around each page its scores are averaged
	// over (the page and two either side).
	window = 2
	// Lead is how far ahead of the playing mood another must score (less
	// with many moods: see sureness).
	Lead = 0.08
	// Hold is how many pages in a row it must lead for before the mood
	// switches (the switch is placed at the first of them).
	Hold = 4
	// MinScore is how sure the model must be of a mood at all (less with
	// many moods: see sureness).
	MinScore = 0.3
)

// sureness is how sure the model must be of a mood, and how far ahead of
// the playing one, when it chooses among n moods. The scores share 1
// between them, so with ten moods a clear page scores about 0.3 and its
// neighbours average it down: a mood counts when it gets twice an even
// share, and leads by half of one.
func sureness(n int) (minScore, lead float64) {
	if n <= 0 {
		return MinScore, Lead
	}
	return min(MinScore, 2/float64(n)), min(Lead, 0.5/float64(n))
}

// Changes finds where a chapter's mood changes, from its pages' scores.
// carry is the mood the previous chapter ended on (0: none); when it has
// none, the chapter's first pages set one. Pages that couldn't be scored
// (empty Scores) are skipped over.
func Changes(pages []Scores, carry int64) []Change {
	smooth := smoothed(pages)
	moods := 0
	for _, p := range pages {
		moods = max(moods, len(p))
	}
	minScore, lead := sureness(moods)
	var out []Change
	current := carry
	if current == 0 {
		// the opening pages decide where the chapter starts
		sum := Scores{}
		n := 0
		for i := 0; i < len(smooth) && n < Hold; i++ {
			if len(smooth[i]) == 0 {
				continue
			}
			for m, v := range smooth[i] {
				sum[m] += v
			}
			n++
		}
		if n > 0 {
			if m, v := best(sum); v/float64(n) >= minScore {
				current = m
				out = append(out, Change{Page: 1, Mood: m, Confidence: round2(v / float64(n))})
			}
		}
	}
	runMood, runStart, runLen, runSum := int64(0), 0, 0, 0.0
	for i, s := range smooth {
		if len(s) == 0 {
			continue
		}
		m, v := best(s)
		if m == current || v < minScore || v-s[current] < lead {
			runMood, runLen = 0, 0
			continue
		}
		if m != runMood {
			runMood, runStart, runLen, runSum = m, i, 0, 0
		}
		runLen++
		runSum += v
		if runLen >= Hold {
			out = append(out, Change{Page: runStart + 1, Mood: m, Confidence: round2(runSum / float64(runLen))})
			current, runMood, runLen = m, 0, 0
		}
	}
	return out
}

// smoothed averages each page's scores with its neighbours'.
func smoothed(pages []Scores) []Scores {
	out := make([]Scores, len(pages))
	for i := range pages {
		if len(pages[i]) == 0 {
			continue
		}
		sum, n := Scores{}, 0
		for j := max(0, i-window); j <= min(len(pages)-1, i+window); j++ {
			if len(pages[j]) == 0 {
				continue
			}
			for m, v := range pages[j] {
				sum[m] += v
			}
			n++
		}
		for m := range sum {
			sum[m] /= float64(n)
		}
		out[i] = sum
	}
	return out
}

// best is the highest-scoring mood (the lowest id on a tie).
func best(s Scores) (int64, float64) {
	var m int64
	v := -1.0
	for id, x := range s {
		if x > v || (x == v && id < m) {
			m, v = id, x
		}
	}
	return m, v
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
