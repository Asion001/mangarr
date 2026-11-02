package soundtrack

import (
	"context"
	"slices"
	"strings"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// ExportFormat names soundtrack files.
const ExportFormat = "mangarr-soundtrack"

// Export is a soundtrack as a file another install can import: cues by
// chapter number and page, moods by slug or name, scenes with their
// sounds by content hash.
type Export struct {
	Format  string        `json:"format" enum:"mangarr-soundtrack"`
	Version int           `json:"version"`
	Title   string        `json:"title,omitempty"`
	Cues    []ExportedCue `json:"cues"`
}

// ExportedCue is one cue of an exported soundtrack.
type ExportedCue struct {
	Chapter   float64        `json:"chapter"`
	Page      *int           `json:"page,omitempty"`
	PageCount *int           `json:"pageCount,omitempty"`
	Mood      *ExportedMood  `json:"mood,omitempty"`
	Scene     *ExportedScene `json:"scene,omitempty"`
}

// ExportedMood names a mood (a built-in one by slug).
type ExportedMood struct {
	Slug       string `json:"slug,omitempty"`
	Name       string `json:"name"`
	SpotifyURI string `json:"spotifyUri,omitempty"`
}

// ExportedScene is a scene with its sounds by hash.
type ExportedScene struct {
	Name       string          `json:"name"`
	Mood       *ExportedMood   `json:"mood,omitempty"`
	SpotifyURI string          `json:"spotifyUri,omitempty"`
	Shuffle    bool            `json:"shuffle"`
	Ambience   []ExportedSound `json:"ambience"`
	SFX        []ExportedSound `json:"sfx"`
}

// ExportedSound is a pack sound by its content hash.
type ExportedSound struct {
	SHA256 string  `json:"sha256"`
	Title  string  `json:"title,omitempty"`
	Volume float64 `json:"volume,omitempty"`
	Label  string  `json:"label,omitempty"`
}

func exportMood(m *model.AudioMood) *ExportedMood {
	if m == nil {
		return nil
	}
	return &ExportedMood{Slug: m.Slug, Name: m.Name, SpotifyURI: m.SpotifyURI}
}

// MakeExport turns a soundtrack into its file.
func MakeExport(ctx context.Context, db bun.IDB, title string, d *Doc) (*Export, error) {
	out := &Export{Format: ExportFormat, Version: 1, Title: title, Cues: []ExportedCue{}}
	if d == nil {
		return out, nil
	}
	r := &resolver{ctx: ctx, db: db, moods: map[int64]*model.AudioMood{}, scenes: map[int64]*Scene{}}
	for _, c := range d.Cues {
		e := ExportedCue{Chapter: c.ChapterNumber, Page: c.Page, PageCount: c.PageCount}
		m, err := r.mood(c.MoodID)
		if err != nil {
			return nil, err
		}
		e.Mood = exportMood(m)
		if c.SceneID != nil {
			sc, err := GetScene(ctx, db, *c.SceneID)
			if err == nil {
				sm, err := r.mood(sc.MoodID)
				if err != nil {
					return nil, err
				}
				es := &ExportedScene{Name: sc.Name, Mood: exportMood(sm), SpotifyURI: sc.SpotifyURI, Shuffle: sc.Shuffle, Ambience: []ExportedSound{}, SFX: []ExportedSound{}}
				for _, t := range sc.Ambience {
					es.Ambience = append(es.Ambience, ExportedSound{SHA256: t.SHA256, Title: t.Title, Volume: t.Volume})
				}
				for _, t := range sc.SFX {
					es.SFX = append(es.SFX, ExportedSound{SHA256: t.SHA256, Title: t.Title, Label: t.Label})
				}
				e.Scene = es
			}
		}
		out.Cues = append(out.Cues, e)
	}
	return out, nil
}

// ImportResult says what an import could not bring over.
type ImportResult struct {
	Cues          int `json:"cues"`
	ScenesCreated int `json:"scenesCreated"`
	// MissingSounds are sounds whose pack isn't installed here.
	MissingSounds int `json:"missingSounds"`
	// MissingMoods are moods with no match here; their cues play nothing.
	MissingMoods int `json:"missingMoods"`
}

// ReadExport turns an exported soundtrack into a Doc for this install:
// moods by slug then name, scenes by name (made when makeScenes allows,
// from the sounds installed here).
func ReadExport(ctx context.Context, db bun.IDB, e Export, makeScenes bool) (*Doc, *ImportResult, error) {
	if e.Format != ExportFormat {
		return nil, nil, invalid("not a soundtrack file")
	}
	var moods []model.AudioMood
	if err := db.NewSelect().Model(&moods).Scan(ctx); err != nil {
		return nil, nil, err
	}
	res := &ImportResult{}
	findMood := func(m *ExportedMood) *int64 {
		if m == nil {
			return nil
		}
		for _, x := range moods {
			if m.Slug != "" && x.Slug == m.Slug {
				return &x.ID
			}
		}
		for _, x := range moods {
			if strings.EqualFold(x.Name, m.Name) {
				return &x.ID
			}
		}
		res.MissingMoods++
		return nil
	}
	scenes, err := Scenes(ctx, db)
	if err != nil {
		return nil, nil, err
	}
	track := func(sum string) *int64 {
		var t model.AudioTrack
		if err := db.NewSelect().Model(&t).Where("sha256 = ?", strings.ToLower(sum)).Limit(1).Scan(ctx); err != nil {
			res.MissingSounds++
			return nil
		}
		return &t.ID
	}
	d := &Doc{Cues: []Cue{}}
	for _, ec := range e.Cues {
		c := Cue{ChapterNumber: ec.Chapter, Page: ec.Page, PageCount: ec.PageCount, MoodID: findMood(ec.Mood)}
		if ec.Scene != nil {
			i := slices.IndexFunc(scenes, func(s Scene) bool { return strings.EqualFold(s.Name, ec.Scene.Name) })
			if i >= 0 {
				c.SceneID = &scenes[i].ID
			} else if makeScenes {
				in := SceneInput{Name: ec.Scene.Name, MoodID: findMood(ec.Scene.Mood), SpotifyURI: ec.Scene.SpotifyURI, Shuffle: ec.Scene.Shuffle}
				if !ValidURI(in.SpotifyURI) {
					in.SpotifyURI = ""
				}
				for _, a := range ec.Scene.Ambience {
					if id := track(a.SHA256); id != nil && len(in.Ambience) < MaxAmbience {
						in.Ambience = append(in.Ambience, SceneLayer{TrackID: *id, Volume: a.Volume})
					}
				}
				for _, s := range ec.Scene.SFX {
					if id := track(s.SHA256); id != nil && len(in.SFX) < MaxSFX {
						in.SFX = append(in.SFX, ScenePad{TrackID: *id, Label: s.Label})
					}
				}
				sc, err := CreateScene(ctx, db, in)
				if err != nil {
					return nil, nil, err
				}
				scenes = append(scenes, *sc)
				c.SceneID = &sc.ID
				res.ScenesCreated++
			} else if c.MoodID == nil {
				c.MoodID = findMood(ec.Scene.Mood)
			}
		}
		d.Cues = append(d.Cues, c)
	}
	res.Cues = len(d.Cues)
	return d, res, nil
}
