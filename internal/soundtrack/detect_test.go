package soundtrack

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func TestScoreText(t *testing.T) {
	moods := []model.AudioMood{
		{ID: 1, Name: "Action", Keywords: []string{"battle", "duel", "final showdown"}},
		{ID: 2, Name: "Romance", Keywords: []string{"date", "confession"}},
	}
	for text, want := range map[string]int64{
		"Chapter 12: The Battle at Dawn": 1,
		"The Final Showdown":             1,
		"A Date After the Duel":          0, // a tie says nothing
		"Confession (and a second date)": 2,
		"Chapter 13":                     0,
		"Battlefield":                    0, // whole words only
	} {
		m, conf := ScoreText(moods, text)
		got := int64(0)
		if m != nil {
			got = m.ID
		}
		if got != want || (m != nil && (conf <= 0 || conf > 1)) {
			t.Errorf("ScoreText(%q) = %d (%v), want %d", text, got, conf, want)
		}
	}
	_, one := ScoreText(moods, "Battle")
	_, two := ScoreText(moods, "Battle and duel")
	if two <= one {
		t.Errorf("more agreeing hits should be surer: %v vs %v", one, two)
	}
}

// TestAutoMoods: chapter titles become automatic cues where the mood
// changes; cues people placed stay and win; my cue > admin cue > auto
// page cue > auto chapter cue > title mood.
func TestAutoMoods(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		moods, err := Moods(ctx, d, st)
		if err != nil {
			t.Fatal(err)
		}
		by := map[string]model.AudioMood{}
		for _, m := range moods {
			by[m.Slug] = m
		}
		user, series := seedLibrary(t, d)
		now := time.Now().UTC()
		for n, title := range map[float64]string{1: "A Quiet Morning", 2: "Breakfast at Home", 3: "Chapter 3", 4: "The Battle Begins", 5: "Duel on the Pier", 6: "Farewell", 7: "Chapter 7"} {
			ch := &model.Chapter{SeriesID: series, NumberKey: fmt.Sprint(n), NumberSort: n, Title: title, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
			if _, err := d.NewInsert().Model(ch).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		// the title's mood, and an admin cue at chapter 6
		calm, action, melancholy, horror, tense := by["calm"], by["action"], by["melancholy"], by["horror"], by["tense"]
		if err := Save(ctx, d, series, nil, Doc{Cues: []Cue{{ChapterNumber: 0, MoodID: &horror.ID}, {ChapterNumber: 6, MoodID: &tense.ID}}}); err != nil {
			t.Fatal(err)
		}
		det, err := DetectChapters(ctx, d, st, series)
		if err != nil {
			t.Fatal(err)
		}
		// calm at 1 (2 carries it), action at 4 (5 carries it); 6 is the admin's
		if det.Chapters != 7 || det.Cues != 2 {
			t.Fatalf("detection %+v", det)
		}
		got, _ := Get(ctx, d, series, nil)
		autos := 0
		for _, c := range got.Cues {
			if c.auto() {
				autos++
				if c.Confidence == nil || c.ModelVersion != DetectorVersion {
					t.Fatalf("auto cue %+v", c)
				}
			}
		}
		if autos != 2 || len(got.Cues) != 4 {
			t.Fatalf("cues %+v", got.Cues)
		}
		at := func(ch float64) *Resolved {
			t.Helper()
			r, err := Resolve(ctx, d, st, series, user, nil, ch)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		for ch, want := range map[float64]int64{1: calm.ID, 3: calm.ID, 4: action.ID, 5: action.ID, 6: tense.ID, 7: tense.ID} {
			if r := at(ch); r.Start == nil || r.Start.Mood.ID != want {
				t.Errorf("chapter %g plays %+v, want mood %d", ch, r.Start, want)
			}
		}
		if r := at(4); r.Source != FromAuto || r.Start.Confidence == nil {
			t.Fatalf("an auto cue says so: %+v", r)
		}
		// an admin cue before the auto ones carries over them
		if _, err := SetCue(ctx, d, series, nil, Cue{ChapterNumber: 3, MoodID: &melancholy.ID}); err != nil {
			t.Fatal(err)
		}
		if r := at(4); r.Start.Mood.ID != melancholy.ID {
			t.Fatalf("admin cue over auto: %+v", r.Start)
		}
		if r := at(1); r.Start.Mood.ID != calm.ID {
			t.Fatalf("auto fills in before the first placed cue: %+v", r.Start)
		}
		// my cue wins over everything at its place
		if _, err := SetCue(ctx, d, series, &user, Cue{ChapterNumber: 1, MoodID: &horror.ID}); err != nil {
			t.Fatal(err)
		}
		if r := at(1); r.Start.Mood.ID != horror.ID || r.Source != FromPersonal {
			t.Fatalf("my cue: %+v", r)
		}
		// editing an auto cue makes it someone's; detection keeps it
		if _, err := SetCue(ctx, d, series, nil, Cue{ChapterNumber: 4, MoodID: &tense.ID}); err != nil {
			t.Fatal(err)
		}
		if _, err := DetectChapters(ctx, d, st, series); err != nil {
			t.Fatal(err)
		}
		got, _ = Get(ctx, d, series, nil)
		for _, c := range got.Cues {
			if c.ChapterNumber == 4 && (c.auto() || *c.MoodID != tense.ID) {
				t.Fatalf("detection replaced an edited cue: %+v", c)
			}
		}
		// without any title clues, the title mood plays (and nothing is added)
		if err := Save(ctx, d, series, nil, Doc{Cues: []Cue{{ChapterNumber: 0, MoodID: &horror.ID}}}); err != nil {
			t.Fatal(err)
		}
		if err := Reset(ctx, d, series, user); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.Chapter)(nil)).Set("title = ''").Where("series_id = ?", series).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if det, _ = DetectChapters(ctx, d, st, series); det.Cues != 0 {
			t.Fatalf("found moods in empty titles: %+v", det)
		}
		if r := at(5); r.Start.Mood.ID != horror.ID || r.Source != FromShared {
			t.Fatalf("title mood: %+v", r)
		}
	})
}
