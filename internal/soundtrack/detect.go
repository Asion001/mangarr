package soundtrack

import (
	"context"
	"math"
	"strings"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// DetectorVersion names how automatic chapter cues were found (kept on
// each cue, so a better detector can tell its own from older ones).
const DetectorVersion = "keywords-1"

// ScoreText picks the mood whose keywords a text (a chapter title) has
// most, with a confidence from 0 to 1; nil when none match or two tie.
func ScoreText(moods []model.AudioMood, text string) (*model.AudioMood, float64) {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	if len(words) == 0 {
		return nil, 0
	}
	padded := " " + strings.Join(words, " ") + " "
	var best *model.AudioMood
	bestN, second, total := 0, 0, 0
	for i := range moods {
		n := 0
		for _, k := range moods[i].Keywords {
			if k = strings.Join(wordRe.FindAllString(strings.ToLower(k), -1), " "); k != "" && strings.Contains(padded, " "+k+" ") {
				n++
			}
		}
		total += n
		switch {
		case n > bestN:
			best, second, bestN = &moods[i], bestN, n
		case n > second:
			second = n
		}
	}
	if bestN == 0 || bestN == second {
		return nil, 0
	}
	// sure when the hits agree, more so with more of them
	conf := float64(bestN) / float64(total) * math.Min(1, 0.5+0.25*float64(bestN))
	return best, math.Round(conf*100) / 100
}

// Detection says what a run found.
type Detection struct {
	Chapters int `json:"chapters" doc:"Chapters looked at"`
	Cues     int `json:"cues" doc:"Automatic cues the soundtrack has now"`
}

// DetectChapters finds moods from a series' chapter titles and keeps them
// as automatic cues in its shared soundtrack, replacing the automatic cues
// found before. A cue is placed where the mood changes (a chapter with no
// clue carries the previous mood on); chapters with a cue someone placed
// are left alone.
func DetectChapters(ctx context.Context, db bun.IDB, st *settings.Store, seriesID int64) (*Detection, error) {
	moods, err := Moods(ctx, db, st)
	if err != nil {
		return nil, err
	}
	var chapters []struct {
		Number float64 `bun:"number_sort"`
		Title  string  `bun:"title"`
	}
	if err := db.NewSelect().TableExpr("chapters").ColumnExpr("number_sort, title").Where("series_id = ?", seriesID).OrderExpr("number_sort, id").Scan(ctx, &chapters); err != nil {
		return nil, err
	}
	d, err := Get(ctx, db, seriesID, nil)
	if err != nil {
		return nil, err
	}
	if d == nil {
		d = &Doc{}
	}
	// keep cues people placed and those another detector (pages) found;
	// replace only this detector's own
	taken := map[[2]float64]bool{}
	cues := []Cue{}
	for _, c := range d.Cues {
		if !c.auto() || c.ModelVersion != DetectorVersion {
			cues = append(cues, c)
			taken[c.key()] = true
		}
	}
	out := &Detection{}
	var last *int64
	seen := map[float64]bool{}
	for _, ch := range chapters {
		if ch.Number <= 0 || seen[ch.Number] {
			continue
		}
		seen[ch.Number] = true
		out.Chapters++
		m, conf := ScoreText(moods, ch.Title)
		if m == nil || (last != nil && *last == m.ID) {
			continue
		}
		last = &m.ID
		if taken[[2]float64{ch.Number, 0}] {
			continue
		}
		id, c := m.ID, conf
		cues = append(cues, Cue{ChapterNumber: ch.Number, MoodID: &id, Source: model.CueAuto, Confidence: &c, ModelVersion: DetectorVersion})
		out.Cues++
	}
	if out.Cues == 0 && len(cues) == len(d.Cues) {
		return out, nil // nothing found, nothing to drop
	}
	d.Cues = cues
	return out, Save(ctx, db, seriesID, nil, *d)
}
