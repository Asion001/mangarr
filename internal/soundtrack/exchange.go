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
// chapter number and page, moods by slug or name with their sounds by
// content hash.
type Export struct {
	Format  string        `json:"format" enum:"mangarr-soundtrack"`
	Version int           `json:"version"`
	Title   string        `json:"title,omitempty"`
	Cues    []ExportedCue `json:"cues"`
}

// ExportedCue is one cue of an exported soundtrack.
type ExportedCue struct {
	Chapter   float64       `json:"chapter"`
	Page      *int          `json:"page,omitempty"`
	PageCount *int          `json:"pageCount,omitempty"`
	Mood      *ExportedMood `json:"mood,omitempty"`
	// Scene is how files from before moods and scenes were one named a
	// scene; it is read as a mood.
	Scene *ExportedScene `json:"scene,omitempty"`
}

// ExportedMood names a mood (a built-in one by slug), with what it plays.
type ExportedMood struct {
	Slug       string          `json:"slug,omitempty"`
	Name       string          `json:"name"`
	Color      string          `json:"color,omitempty"`
	Icon       string          `json:"icon,omitempty"`
	SpotifyURI string          `json:"spotifyUri,omitempty"`
	Shuffle    bool            `json:"shuffle,omitempty"`
	Ambience   []ExportedSound `json:"ambience,omitempty"`
	SFX        []ExportedSound `json:"sfx,omitempty"`
}

// ExportedScene is a scene in an older file.
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
	// Plays is an ambience loop's intensity (always, normal or intense).
	Plays string `json:"plays,omitempty"`
}

func exportMood(m *model.AudioMood) *ExportedMood {
	if m == nil {
		return nil
	}
	e := &ExportedMood{Slug: m.Slug, Name: m.Name, Color: m.Color, Icon: m.Icon, SpotifyURI: m.SpotifyURI, Shuffle: m.Shuffle}
	for _, t := range m.Ambience {
		e.Ambience = append(e.Ambience, ExportedSound{SHA256: t.SHA256, Title: t.Title, Volume: t.Volume, Plays: exportedPlays(t.Plays)})
	}
	for _, t := range m.SFX {
		e.SFX = append(e.SFX, ExportedSound{SHA256: t.SHA256, Title: t.Title, Label: t.Label})
	}
	return e
}

// MakeExport turns a soundtrack into its file.
func MakeExport(ctx context.Context, db bun.IDB, title string, d *Doc) (*Export, error) {
	out := &Export{Format: ExportFormat, Version: 2, Title: title, Cues: []ExportedCue{}}
	if d == nil {
		return out, nil
	}
	r := &resolver{ctx: ctx, db: db, moods: map[int64]*model.AudioMood{}}
	for _, c := range d.Cues {
		m, err := r.mood(c.MoodID)
		if err != nil {
			return nil, err
		}
		out.Cues = append(out.Cues, ExportedCue{Chapter: c.ChapterNumber, Page: c.Page, PageCount: c.PageCount, Mood: exportMood(m)})
	}
	return out, nil
}

// ImportResult says what an import could not bring over.
type ImportResult struct {
	Cues         int `json:"cues"`
	MoodsCreated int `json:"moodsCreated"`
	// MissingSounds are sounds whose pack isn't installed here.
	MissingSounds int `json:"missingSounds"`
	// MissingMoods are moods with no match here; their cues play nothing.
	MissingMoods int `json:"missingMoods"`
}

// ReadExport turns an exported soundtrack into a Doc for this install:
// moods by slug then name, made from the sounds installed here when
// makeMoods allows.
func ReadExport(ctx context.Context, db bun.IDB, e Export, makeMoods bool) (*Doc, *ImportResult, error) {
	if e.Format != ExportFormat {
		return nil, nil, invalid("not a soundtrack file")
	}
	var moods []model.AudioMood
	if err := db.NewSelect().Model(&moods).Scan(ctx); err != nil {
		return nil, nil, err
	}
	res := &ImportResult{}
	track := func(sum string) *int64 {
		var t model.AudioTrack
		if err := db.NewSelect().Model(&t).Where("sha256 = ?", strings.ToLower(sum)).Limit(1).Scan(ctx); err != nil {
			res.MissingSounds++
			return nil
		}
		return &t.ID
	}
	lookup := func(m *ExportedMood) *model.AudioMood {
		if m.Slug != "" {
			if i := slices.IndexFunc(moods, func(x model.AudioMood) bool { return x.Slug == m.Slug }); i >= 0 {
				return &moods[i]
			}
		}
		if i := slices.IndexFunc(moods, func(x model.AudioMood) bool { return strings.EqualFold(x.Name, m.Name) }); i >= 0 {
			return &moods[i]
		}
		return nil
	}
	var find func(m *ExportedMood, like *ExportedMood) (*int64, error)
	find = func(m *ExportedMood, like *ExportedMood) (*int64, error) {
		if m == nil {
			return nil, nil
		}
		if x := lookup(m); x != nil {
			return &x.ID, nil
		}
		if !makeMoods || m.Slug != "" {
			res.MissingMoods++
			return nil, nil
		}
		nm := &model.AudioMood{Name: m.Name, Color: m.Color, Icon: m.Icon, Genres: []string{}, Keywords: []string{}, Prompts: []string{},
			SpotifyURI: m.SpotifyURI, Shuffle: m.Shuffle}
		if like != nil {
			if x := lookup(like); x != nil {
				nm.Color, nm.Icon = x.Color, x.Icon
				if nm.SpotifyURI == "" {
					nm.SpotifyURI, nm.SpotifyName, nm.SpotifyImageURL, nm.Shuffle = x.SpotifyURI, x.SpotifyName, x.SpotifyImageURL, x.Shuffle
				}
			}
		}
		if !colorRe.MatchString(nm.Color) {
			nm.Color = "#9aa2b1"
		}
		if !slices.Contains(Icons, nm.Icon) {
			nm.Icon = "music"
		}
		if !ValidURI(nm.SpotifyURI) {
			nm.SpotifyURI = ""
		}
		var sounds Sounds
		for _, a := range m.Ambience {
			if id := track(a.SHA256); id != nil && len(sounds.Ambience) < MaxAmbience {
				sounds.Ambience = append(sounds.Ambience, MoodLayer{TrackID: *id, Volume: a.Volume, Plays: plays(a.Plays)})
			}
		}
		for _, s := range m.SFX {
			if id := track(s.SHA256); id != nil && len(sounds.SFX) < MaxSFX {
				sounds.SFX = append(sounds.SFX, MoodPad{TrackID: *id, Label: s.Label})
			}
		}
		if err := CreateMood(ctx, db, nm, &sounds); err != nil {
			return nil, err
		}
		moods = append(moods, *nm)
		res.MoodsCreated++
		return &nm.ID, nil
	}
	d := &Doc{Cues: []Cue{}}
	for _, ec := range e.Cues {
		c := Cue{ChapterNumber: ec.Chapter, Page: ec.Page, PageCount: ec.PageCount}
		var err error
		if sc := ec.Scene; sc != nil {
			c.MoodID, err = find(&ExportedMood{Name: sc.Name, SpotifyURI: sc.SpotifyURI, Shuffle: sc.Shuffle, Ambience: sc.Ambience, SFX: sc.SFX}, sc.Mood)
			if err == nil && c.MoodID == nil && sc.Mood != nil {
				if x := lookup(sc.Mood); x != nil {
					c.MoodID = &x.ID
				}
			}
		} else {
			c.MoodID, err = find(ec.Mood, nil)
		}
		if err != nil {
			return nil, nil, err
		}
		d.Cues = append(d.Cues, c)
	}
	res.Cues = len(d.Cues)
	return d, res, nil
}

// exportedPlays leaves the default out of a file.
func exportedPlays(v string) string {
	if v == model.PlaysNormal {
		return ""
	}
	return v
}
