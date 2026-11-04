package soundtrack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// Defaults are what a built-in mood, or a scene a sound pack brought,
// starts with. They live here, not in the database, so a new version can
// change them: a field an admin hasn't edited follows its default, one
// they edited keeps their value until they reset the mood.
type Defaults struct {
	Name     string   `json:"name,omitempty"`
	Color    string   `json:"color,omitempty"`
	Icon     string   `json:"icon,omitempty"`
	Genres   []string `json:"genres,omitempty"`
	Keywords []string `json:"keywords,omitempty"`
	Prompts  []string `json:"prompts,omitempty"`
}

// The fields defaults cover, as Edited names them.
const (
	FieldName     = "name"
	FieldColor    = "color"
	FieldIcon     = "icon"
	FieldGenres   = "genres"
	FieldKeywords = "keywords"
	FieldPrompts  = "prompts"
)

// sceneDefaults are the words and page descriptions of the scenes the
// official sound packs bring, by scene name (lower case). The pack sets
// the rest (name, colour, icon, sounds).
var sceneDefaults = map[string]Defaults{
	"busy street": {Keywords: []string{"street", "city", "town", "downtown", "market", "crowd", "shopping"},
		Prompts: []string{"a manga page of a crowded city street", "a manga panel of people walking past shops in town"}},
	"café": {Keywords: []string{"café", "cafe", "coffee", "tea house", "bakery", "restaurant"},
		Prompts: []string{"a manga page inside a cozy café", "a manga panel of two people talking over coffee at a table"}},
	"school day": {Keywords: []string{"school", "class", "classroom", "exam", "test", "homework", "teacher", "student", "entrance"},
		Prompts: []string{"a manga page in a school classroom", "a manga panel of students in uniforms in a school hallway"}},
	"station": {Keywords: []string{"station", "train", "platform", "departure", "journey", "subway"},
		Prompts: []string{"a manga page at a train station platform", "a manga panel of a train arriving at a station"}},
	"battlefield": {Keywords: []string{"war", "army", "battlefield", "siege", "invasion", "soldiers", "frontline"},
		Prompts: []string{"a manga page of a large battle between armies", "a manga panel of soldiers fighting on a battlefield with smoke"}},
	"duel": {Keywords: []string{"duel", "one on one", "rival", "showdown", "swordsman", "versus", "vs"},
		Prompts: []string{"a manga page of two fighters facing each other in a duel", "a manga panel of crossed swords between two rivals"}},
	"seaside": {Keywords: []string{"sea", "seaside", "beach", "ocean", "coast", "shore", "harbor", "port"},
		Prompts: []string{"a manga page at the seaside with waves", "a manga panel of a beach and the ocean"}},
	"forest": {Keywords: []string{"forest", "woods", "trees", "grove", "hike"},
		Prompts: []string{"a manga page in a sunny forest with trees", "a manga panel of people walking through the woods"}},
	"night forest": {Keywords: []string{"night forest", "dark forest", "dark woods"},
		Prompts: []string{"a manga page of a dark forest at night", "a manga panel of shadowy trees under the moon"}},
	"campfire": {Keywords: []string{"campfire", "camp", "camping", "bonfire", "tent"},
		Prompts: []string{"a manga page of people sitting around a campfire at night", "a manga panel of a crackling bonfire"}},
	"quiet room": {Keywords: []string{"room", "bedroom", "apartment", "study", "library", "alone"},
		Prompts: []string{"a manga page of a quiet room indoors", "a manga panel of someone alone in a bedroom"}},
	"haunted house": {Keywords: []string{"haunted", "mansion", "abandoned", "ghost house", "spirits"},
		Prompts: []string{"a manga page of a dark haunted house", "a manga panel of a creepy abandoned mansion corridor"}},
	"rainy night": {Keywords: []string{"rain", "rainy", "downpour", "umbrella", "wet"},
		Prompts: []string{"a manga page of a rainy night street", "a manga panel with heavy rain falling at night"}},
	"storm": {Keywords: []string{"storm", "thunder", "lightning", "typhoon", "hurricane", "tempest"},
		Prompts: []string{"a manga page of a thunderstorm with lightning", "a manga panel of a violent storm with wind and rain"}},
	"snowfall": {Keywords: []string{"snow", "winter", "snowy", "blizzard", "frozen", "christmas"},
		Prompts: []string{"a manga page of falling snow", "a manga panel of a snowy winter landscape"}},
}

// DefaultsOf is what a mood follows unless edited: a built-in mood's (by
// slug) or a pack scene's (by the scene name it was installed as); nil for
// moods admins made.
func DefaultsOf(m *model.AudioMood) *Defaults {
	if m.Slug != "" {
		for _, b := range builtins {
			if b.slug == m.Slug {
				return &Defaults{Name: b.name, Color: b.color, Icon: b.icon, Genres: b.genres, Keywords: b.keywords, Prompts: b.prompts}
			}
		}
		return nil
	}
	if m.PackID != nil && m.Scene != "" {
		if d, ok := sceneDefaults[strings.ToLower(m.Scene)]; ok {
			return &d
		}
	}
	return nil
}

// defaultsKey changes whenever any default does.
func defaultsKey() string {
	h := sha256.New()
	fmt.Fprintf(h, "%v\x00%v", builtins, sceneDefaults)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// fields lists the fields d sets, with how to read and write them on a mood.
func (d *Defaults) fields() []struct {
	name string
	same func(*model.AudioMood) bool
	set  func(*model.AudioMood)
} {
	type f = struct {
		name string
		same func(*model.AudioMood) bool
		set  func(*model.AudioMood)
	}
	var out []f
	if d.Name != "" {
		out = append(out, f{FieldName, func(m *model.AudioMood) bool { return m.Name == d.Name }, func(m *model.AudioMood) { m.Name = d.Name }})
	}
	if d.Color != "" {
		out = append(out, f{FieldColor, func(m *model.AudioMood) bool { return strings.EqualFold(m.Color, d.Color) }, func(m *model.AudioMood) { m.Color = d.Color }})
	}
	if d.Icon != "" {
		out = append(out, f{FieldIcon, func(m *model.AudioMood) bool { return m.Icon == d.Icon }, func(m *model.AudioMood) { m.Icon = d.Icon }})
	}
	list := func(name string, def []string, get func(*model.AudioMood) *[]string) {
		if def == nil {
			return
		}
		out = append(out, f{name, func(m *model.AudioMood) bool { return slices.Equal(*get(m), def) },
			func(m *model.AudioMood) { *get(m) = slices.Clone(def) }})
	}
	list(FieldGenres, d.Genres, func(m *model.AudioMood) *[]string { return &m.Genres })
	list(FieldKeywords, d.Keywords, func(m *model.AudioMood) *[]string { return &m.Keywords })
	list(FieldPrompts, d.Prompts, func(m *model.AudioMood) *[]string { return &m.Prompts })
	return out
}

// edited lists the fields of m that differ from d.
func (d *Defaults) edited(m *model.AudioMood) []string {
	out := []string{}
	if d == nil {
		return out
	}
	for _, f := range d.fields() {
		if !f.same(m) {
			out = append(out, f.name)
		}
	}
	return out
}

// follow sets the fields of m that aren't edited to their defaults, and
// reports whether any changed.
func (d *Defaults) follow(m *model.AudioMood) bool {
	if d == nil {
		return false
	}
	changed := false
	for _, f := range d.fields() {
		if !slices.Contains(m.Edited, f.name) && !f.same(m) {
			f.set(m)
			changed = true
		}
	}
	return changed
}

// syncDefaults brings every mood's unedited fields to their defaults.
// first is the first run with defaults: what differs then was edited
// before edits were kept apart, so it's marked edited instead.
func syncDefaults(ctx context.Context, db bun.IDB, first bool) error {
	var moods []model.AudioMood
	if err := db.NewSelect().Model(&moods).Scan(ctx); err != nil {
		return err
	}
	now := time.Now().UTC()
	for i := range moods {
		m := &moods[i]
		d := DefaultsOf(m)
		if d == nil {
			continue
		}
		if first && m.Slug != "" {
			m.Edited = d.edited(m)
		}
		if !d.follow(m) && !first {
			continue
		}
		m.UpdatedAt = now
		if _, err := db.NewUpdate().Model(m).Column("name", "color", "icon", "genres", "keywords", "prompts", "edited", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

// ResetMood puts a built-in mood or pack scene back to its defaults.
func ResetMood(ctx context.Context, db bun.IDB, id int64) (*model.AudioMood, error) {
	m, err := Mood(ctx, db, id)
	if err != nil {
		return nil, err
	}
	d := DefaultsOf(m)
	if d == nil {
		return nil, invalid("this mood has no defaults to go back to")
	}
	m.Edited = []string{}
	d.follow(m)
	m.UpdatedAt = time.Now().UTC()
	if _, err := db.NewUpdate().Model(m).Column("name", "color", "icon", "genres", "keywords", "prompts", "edited", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, err
	}
	return Mood(ctx, db, id)
}
