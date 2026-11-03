package soundtrack

import (
	"context"
	"strings"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// A mood's sounds.
const (
	MaxAmbience = 4
	MaxSFX      = 8
)

// MoodLayer is an ambience loop at a volume.
type MoodLayer struct {
	TrackID int64   `json:"trackId"`
	Volume  float64 `json:"volume" minimum:"0" maximum:"1"`
}

// MoodPad is a sound effect pad.
type MoodPad struct {
	TrackID int64  `json:"trackId"`
	Label   string `json:"label,omitempty" maxLength:"40"`
}

// Sounds are a mood's ambience loops and effect pads as admins edit them.
type Sounds struct {
	Ambience []MoodLayer `json:"ambience,omitempty" maxItems:"4"`
	SFX      []MoodPad   `json:"sfx,omitempty" maxItems:"8"`
}

// withSounds fills in the moods' ambience and effects.
func withSounds(ctx context.Context, db bun.IDB, moods []model.AudioMood) error {
	if len(moods) == 0 {
		return nil
	}
	ids := make([]int64, len(moods))
	for i := range moods {
		ids[i] = moods[i].ID
		moods[i].Ambience, moods[i].SFX = []model.MoodTrack{}, []model.MoodTrack{}
	}
	var items []struct {
		model.AudioMoodItem `bun:",extend"`
		Title               string  `bun:"title"`
		Category            string  `bun:"category"`
		Icon                string  `bun:"icon"`
		SHA256              string  `bun:"sha256"`
		Ext                 string  `bun:"ext"`
		GainDB              float64 `bun:"gain_db"`
		Pack                string  `bun:"pack"`
		License             string  `bun:"license"`
		Attribution         string  `bun:"attribution"`
		SourceURL           string  `bun:"source_url"`
	}
	err := db.NewSelect().Model(&items).
		ColumnExpr("audio_mood_item.*, t.title, t.category, t.icon, t.sha256, t.ext, t.gain_db, t.license, t.attribution, t.source_url, p.name AS pack").
		Join("JOIN audio_tracks AS t ON t.id = audio_mood_item.track_id").
		Join("JOIN audio_packs AS p ON p.id = t.pack_id").
		Where("audio_mood_item.mood_id IN (?)", bun.In(ids)).
		OrderExpr("audio_mood_item.position, audio_mood_item.id").Scan(ctx)
	if err != nil {
		return err
	}
	byID := make(map[int64]*model.AudioMood, len(moods))
	for i := range moods {
		byID[moods[i].ID] = &moods[i]
	}
	for _, it := range items {
		m := byID[it.MoodID]
		if m == nil {
			continue
		}
		t := model.MoodTrack{TrackID: it.TrackID, Title: it.Title, Category: it.Category, Icon: it.Icon, SHA256: it.SHA256, Ext: it.Ext, GainDB: it.GainDB,
			Volume: it.Volume, Label: it.Label, Pack: it.Pack, License: it.License, Attribution: it.Attribution, SourceURL: it.SourceURL}
		if it.Role == model.TrackAmbience {
			m.Ambience = append(m.Ambience, t)
		} else {
			m.SFX = append(m.SFX, t)
		}
	}
	return nil
}

// checkSounds checks a mood's sounds are installed and of the right kind.
func checkSounds(ctx context.Context, db bun.IDB, in Sounds) error {
	if len(in.Ambience) > MaxAmbience || len(in.SFX) > MaxSFX {
		return invalid("a mood has up to %d ambience loops and %d sound effects", MaxAmbience, MaxSFX)
	}
	check := func(trackID int64, kind string) error {
		var t model.AudioTrack
		if err := db.NewSelect().Model(&t).Where("id = ?", trackID).Scan(ctx); err != nil {
			return invalid("sound %d isn't installed", trackID)
		}
		if t.Kind != kind {
			return invalid("%s is not a %s sound", t.Title, kind)
		}
		return nil
	}
	for _, a := range in.Ambience {
		if err := check(a.TrackID, model.TrackAmbience); err != nil {
			return err
		}
	}
	for _, s := range in.SFX {
		if err := check(s.TrackID, model.TrackSFX); err != nil {
			return err
		}
	}
	return nil
}

// setSounds replaces a mood's sounds.
func setSounds(ctx context.Context, db bun.IDB, moodID int64, in Sounds) error {
	if _, err := db.NewDelete().Model((*model.AudioMoodItem)(nil)).Where("mood_id = ?", moodID).Exec(ctx); err != nil {
		return err
	}
	var items []model.AudioMoodItem
	for i, a := range in.Ambience {
		items = append(items, model.AudioMoodItem{MoodID: moodID, TrackID: a.TrackID, Role: model.TrackAmbience, Volume: max(0, min(1, a.Volume)), Position: i})
	}
	for i, s := range in.SFX {
		items = append(items, model.AudioMoodItem{MoodID: moodID, TrackID: s.TrackID, Role: model.TrackSFX, Volume: 1, Position: i, Label: strings.TrimSpace(s.Label)})
	}
	if len(items) == 0 {
		return nil
	}
	_, err := db.NewInsert().Model(&items).Exec(ctx)
	return err
}

// hasSounds reports whether a mood has any sounds.
func hasSounds(ctx context.Context, db bun.IDB, moodID int64) (bool, error) {
	n, err := db.NewSelect().Model((*model.AudioMoodItem)(nil)).Where("mood_id = ?", moodID).Count(ctx)
	return n > 0, err
}
