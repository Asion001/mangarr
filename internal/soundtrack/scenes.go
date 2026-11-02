package soundtrack

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

var ErrSceneNotFound = errors.New("scene not found")

// Scene limits.
const (
	MaxAmbience = 4
	MaxSFX      = 8
)

// SceneLayer is an ambience loop at a volume.
type SceneLayer struct {
	TrackID int64   `json:"trackId"`
	Volume  float64 `json:"volume" minimum:"0" maximum:"1"`
}

// ScenePad is a sound effect pad.
type ScenePad struct {
	TrackID int64  `json:"trackId"`
	Label   string `json:"label,omitempty" maxLength:"40"`
}

// SceneInput is a scene as admins edit it. Music is the mood's unless
// SpotifyURI overrides it.
type SceneInput struct {
	Name            string       `json:"name" minLength:"1" maxLength:"80"`
	Description     string       `json:"description,omitempty" maxLength:"500"`
	MoodID          *int64       `json:"moodId,omitempty"`
	SpotifyURI      string       `json:"spotifyUri,omitempty"`
	SpotifyName     string       `json:"spotifyName,omitempty"`
	SpotifyImageURL string       `json:"spotifyImageUrl,omitempty"`
	Shuffle         bool         `json:"shuffle" required:"false"`
	Ambience        []SceneLayer `json:"ambience,omitempty" maxItems:"4"`
	SFX             []ScenePad   `json:"sfx,omitempty" maxItems:"8"`
}

// SceneTrack is a scene's layer or pad with what the player needs.
type SceneTrack struct {
	TrackID     int64   `json:"trackId"`
	Title       string  `json:"title"`
	Category    string  `json:"category"`
	Icon        string  `json:"icon"`
	SHA256      string  `json:"sha256"`
	Ext         string  `json:"ext"`
	GainDB      float64 `json:"gainDb"`
	Volume      float64 `json:"volume"`
	Label       string  `json:"label,omitempty"`
	Pack        string  `json:"pack"`
	License     string  `json:"license"`
	Attribution string  `json:"attribution"`
	SourceURL   string  `json:"sourceUrl"`
}

// Scene is a scene with its tracks.
type Scene struct {
	ID              int64        `json:"id"`
	Name            string       `json:"name"`
	Description     string       `json:"description"`
	MoodID          *int64       `json:"moodId,omitempty"`
	SpotifyURI      string       `json:"spotifyUri"`
	SpotifyName     string       `json:"spotifyName"`
	SpotifyImageURL string       `json:"spotifyImageUrl"`
	Shuffle         bool         `json:"shuffle"`
	PackID          *int64       `json:"packId,omitempty" doc:"The pack that brought it"`
	Ambience        []SceneTrack `json:"ambience"`
	SFX             []SceneTrack `json:"sfx"`
	UpdatedAt       time.Time    `json:"updatedAt"`
}

// scenesBy loads scenes (all when ids is nil) with their tracks.
func scenesBy(ctx context.Context, db bun.IDB, ids []int64) ([]Scene, error) {
	var rows []model.AudioScene
	q := db.NewSelect().Model(&rows).OrderExpr("LOWER(name), id")
	if ids != nil {
		if len(ids) == 0 {
			return []Scene{}, nil
		}
		q = q.Where("id IN (?)", bun.In(ids))
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]Scene, 0, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	sceneIDs := make([]int64, len(rows))
	for i, r := range rows {
		sceneIDs[i] = r.ID
	}
	var items []struct {
		model.AudioSceneItem `bun:",extend"`
		Title                string  `bun:"title"`
		Category             string  `bun:"category"`
		Icon                 string  `bun:"icon"`
		SHA256               string  `bun:"sha256"`
		Ext                  string  `bun:"ext"`
		GainDB               float64 `bun:"gain_db"`
		Pack                 string  `bun:"pack"`
		License              string  `bun:"license"`
		Attribution          string  `bun:"attribution"`
		SourceURL            string  `bun:"source_url"`
	}
	err := db.NewSelect().Model(&items).
		ColumnExpr("audio_scene_item.*, t.title, t.category, t.icon, t.sha256, t.ext, t.gain_db, t.license, t.attribution, t.source_url, p.name AS pack").
		Join("JOIN audio_tracks AS t ON t.id = audio_scene_item.track_id").
		Join("JOIN audio_packs AS p ON p.id = t.pack_id").
		Where("audio_scene_item.scene_id IN (?)", bun.In(sceneIDs)).
		OrderExpr("audio_scene_item.position, audio_scene_item.id").Scan(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		s := Scene{ID: r.ID, Name: r.Name, Description: r.Description, MoodID: r.MoodID, SpotifyURI: r.SpotifyURI, SpotifyName: r.SpotifyName,
			SpotifyImageURL: r.SpotifyImageURL, Shuffle: r.Shuffle, PackID: r.PackID, Ambience: []SceneTrack{}, SFX: []SceneTrack{}, UpdatedAt: r.UpdatedAt}
		for _, it := range items {
			if it.SceneID != r.ID {
				continue
			}
			t := SceneTrack{TrackID: it.TrackID, Title: it.Title, Category: it.Category, Icon: it.Icon, SHA256: it.SHA256, Ext: it.Ext, GainDB: it.GainDB,
				Volume: it.Volume, Label: it.Label, Pack: it.Pack, License: it.License, Attribution: it.Attribution, SourceURL: it.SourceURL}
			if it.Role == model.TrackAmbience {
				s.Ambience = append(s.Ambience, t)
			} else {
				s.SFX = append(s.SFX, t)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// Scenes lists the scenes by name.
func Scenes(ctx context.Context, db bun.IDB) ([]Scene, error) { return scenesBy(ctx, db, nil) }

// GetScene returns one scene.
func GetScene(ctx context.Context, db bun.IDB, id int64) (*Scene, error) {
	list, err := scenesBy(ctx, db, []int64{id})
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrSceneNotFound
	}
	return &list[0], nil
}

// saveScene adds a scene (id nil) or replaces one.
func saveScene(ctx context.Context, tx bun.IDB, id, packID *int64, in SceneInput) (int64, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return 0, invalid("give the scene a name")
	}
	in.SpotifyURI = strings.TrimSpace(in.SpotifyURI)
	if !ValidURI(in.SpotifyURI) {
		return 0, invalid("the music must be a Spotify playlist, album or track")
	}
	if in.SpotifyURI == "" {
		in.SpotifyName, in.SpotifyImageURL = "", ""
	}
	if len(in.Ambience) > MaxAmbience || len(in.SFX) > MaxSFX {
		return 0, invalid("a scene has up to %d ambience layers and %d sound effects", MaxAmbience, MaxSFX)
	}
	if in.MoodID != nil {
		if _, err := Mood(ctx, tx, *in.MoodID); err != nil {
			return 0, err
		}
	}
	check := func(trackID int64, kind string) error {
		var t model.AudioTrack
		if err := tx.NewSelect().Model(&t).Where("id = ?", trackID).Scan(ctx); err != nil {
			return invalid("sound %d isn't installed", trackID)
		}
		if t.Kind != kind {
			return invalid("%s is not a %s sound", t.Title, kind)
		}
		return nil
	}
	for _, a := range in.Ambience {
		if err := check(a.TrackID, model.TrackAmbience); err != nil {
			return 0, err
		}
	}
	for _, s := range in.SFX {
		if err := check(s.TrackID, model.TrackSFX); err != nil {
			return 0, err
		}
	}
	now := time.Now().UTC()
	row := model.AudioScene{Name: in.Name, Description: in.Description, MoodID: in.MoodID, SpotifyURI: in.SpotifyURI, SpotifyName: in.SpotifyName,
		SpotifyImageURL: in.SpotifyImageURL, Shuffle: in.Shuffle, PackID: packID, CreatedAt: now, UpdatedAt: now}
	if id == nil {
		if _, err := tx.NewInsert().Model(&row).Exec(ctx); err != nil {
			return 0, err
		}
	} else {
		var old model.AudioScene
		if err := tx.NewSelect().Model(&old).Where("id = ?", *id).Scan(ctx); err != nil {
			return 0, ErrSceneNotFound
		}
		row.ID, row.CreatedAt, row.PackID = old.ID, old.CreatedAt, old.PackID
		if packID != nil {
			row.PackID = packID
		}
		if _, err := tx.NewUpdate().Model(&row).WherePK().Exec(ctx); err != nil {
			return 0, err
		}
		if _, err := tx.NewDelete().Model((*model.AudioSceneItem)(nil)).Where("scene_id = ?", row.ID).Exec(ctx); err != nil {
			return 0, err
		}
	}
	var items []model.AudioSceneItem
	for i, a := range in.Ambience {
		items = append(items, model.AudioSceneItem{SceneID: row.ID, TrackID: a.TrackID, Role: model.TrackAmbience, Volume: max(0, min(1, a.Volume)), Position: i})
	}
	for i, s := range in.SFX {
		items = append(items, model.AudioSceneItem{SceneID: row.ID, TrackID: s.TrackID, Role: model.TrackSFX, Volume: 1, Position: i, Label: strings.TrimSpace(s.Label)})
	}
	if len(items) > 0 {
		if _, err := tx.NewInsert().Model(&items).Exec(ctx); err != nil {
			return 0, err
		}
	}
	return row.ID, nil
}

// CreateScene adds a scene.
func CreateScene(ctx context.Context, db bun.IDB, in SceneInput) (*Scene, error) {
	var id int64
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var err error
		id, err = saveScene(ctx, tx, nil, nil, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return GetScene(ctx, db, id)
}

// UpdateScene replaces a scene.
func UpdateScene(ctx context.Context, db bun.IDB, id int64, in SceneInput) (*Scene, error) {
	err := db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := saveScene(ctx, tx, &id, nil, in)
		return err
	})
	if err != nil {
		return nil, err
	}
	return GetScene(ctx, db, id)
}

// DeleteScene removes a scene; cues that used it keep their mood, if any.
func DeleteScene(ctx context.Context, db bun.IDB, id int64) error {
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*model.AudioScene)(nil)).Where("id = ?", id).Exec(ctx)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrSceneNotFound
		}
		_, err = tx.NewUpdate().Model((*model.SoundtrackCue)(nil)).Set("scene_id = NULL").Where("scene_id = ?", id).Exec(ctx)
		return err
	})
}

// sceneExists reports whether a scene is there.
func sceneExists(ctx context.Context, db bun.IDB, id int64) error {
	n, err := db.NewSelect().Model((*model.AudioScene)(nil)).Where("id = ?", id).Count(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrSceneNotFound
	}
	return nil
}
