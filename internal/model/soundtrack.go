package model

import (
	"time"

	"github.com/uptrace/bun"
)

// AudioMood is what plays: music from Spotify (a playlist, album or track)
// for readers who listen with Spotify, and ambience loops with sound effect
// pads from sound packs for everyone, with a colour and an icon. Slug names
// the built-in moods mangarr seeds ("calm"…); moods admins add, or packs
// bring, have none.
type AudioMood struct {
	bun.BaseModel `bun:"table:audio_moods"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Slug          string `bun:"slug,notnull" json:"slug" doc:"The built-in mood this is (calm, melancholy…); empty for moods admins made"`
	Name          string `bun:"name,notnull" json:"name"`
	Description   string `bun:"description,notnull" json:"description"`
	Color         string `bun:"color,notnull" json:"color" doc:"#rrggbb"`
	Icon          string `bun:"icon,notnull" json:"icon"`
	// Genres are the genres and tags the mood suits (for suggestions).
	Genres []string `bun:"genres,notnull" json:"genres"`
	// Prompts describe how the mood's pages look, for finding it in pages.
	Prompts []string `bun:"prompts,notnull" json:"prompts"`
	// Keywords are words in chapter titles that suggest the mood.
	Keywords        []string `bun:"keywords,notnull" json:"keywords"`
	SpotifyURI      string   `bun:"spotify_uri,notnull" json:"spotifyUri" doc:"spotify:playlist:…, spotify:album:… or spotify:track:…; empty plays nothing"`
	SpotifyName     string   `bun:"spotify_name,notnull" json:"spotifyName"`
	SpotifyImageURL string   `bun:"spotify_image_url,notnull" json:"spotifyImageUrl"`
	Shuffle         bool     `bun:"shuffle,notnull" json:"shuffle"`
	Position        int      `bun:"position,notnull" json:"position"`
	PackID          *int64   `bun:"pack_id" json:"packId,omitempty" doc:"The sound pack that brought it"`
	// Scene is the pack scene it was installed as (its name may change).
	Scene string `bun:"scene,notnull" json:"-"`
	// Edited lists the fields an admin changed from the defaults (name,
	// color, icon, genres, keywords, prompts); the others follow them.
	Edited []string `bun:"edited,notnull" json:"edited,omitempty" doc:"Fields changed from the defaults; the others follow them"`
	// HasDefaults says it can be reset (filled in when listed).
	HasDefaults bool      `bun:"-" json:"hasDefaults,omitempty" doc:"A built-in mood or pack scene, which can go back to its defaults"`
	CreatedAt   time.Time `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt   time.Time `bun:"updated_at,notnull" json:"updatedAt"`
	// Ambience and SFX are its sounds (filled in when listed).
	Ambience []MoodTrack `bun:"-" json:"ambience"`
	SFX      []MoodTrack `bun:"-" json:"sfx"`
}

// MoodTrack is a mood's ambience loop or effect pad with what the player
// needs.
type MoodTrack struct {
	TrackID  int64   `json:"trackId"`
	Title    string  `json:"title"`
	Category string  `json:"category"`
	Icon     string  `json:"icon"`
	SHA256   string  `json:"sha256"`
	Ext      string  `json:"ext"`
	GainDB   float64 `json:"gainDb"`
	Volume   float64 `json:"volume"`
	Label    string  `json:"label,omitempty"`
	// Plays is when an ambience loop plays (always, normal or intense).
	Plays       string `json:"plays,omitempty" enum:"always,normal,intense"`
	Pack        string `json:"pack"`
	License     string `json:"license"`
	Attribution string `json:"attribution"`
	SourceURL   string `json:"sourceUrl"`
}

// SpotifyAccount is a user's connected Spotify account. The refresh token
// is encrypted with a key derived from the instance secret.
type SpotifyAccount struct {
	bun.BaseModel   `bun:"table:spotify_accounts"`
	UserID          int64     `bun:"user_id,pk"`
	SpotifyUserID   string    `bun:"spotify_user_id,notnull"`
	DisplayName     string    `bun:"display_name,notnull"`
	Product         string    `bun:"product,notnull"`
	RefreshTokenEnc string    `bun:"refresh_token_enc,notnull"`
	Scopes          string    `bun:"scopes,notnull"`
	ConnectedAt     time.Time `bun:"connected_at,notnull"`
}

// Soundtrack is a series' soundtrack: the shared one (UserID nil) admins
// set, or one reader's own version of it.
type Soundtrack struct {
	bun.BaseModel `bun:"table:soundtracks"`
	ID            int64     `bun:"id,pk,autoincrement"`
	SeriesID      int64     `bun:"series_id,notnull"`
	UserID        *int64    `bun:"user_id"`
	Muted         bool      `bun:"muted,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
}

// SoundtrackCue starts a mood from a chapter (and page). A cue at chapter
// 0 without a page is the title's default.
type SoundtrackCue struct {
	bun.BaseModel `bun:"table:soundtrack_cues"`
	ID            int64   `bun:"id,pk,autoincrement"`
	SoundtrackID  int64   `bun:"soundtrack_id,notnull"`
	ChapterNumber float64 `bun:"chapter_number,notnull"`
	Page          *int    `bun:"page"`
	// PageCount is the chapter's page count when a page cue was placed.
	PageCount *int   `bun:"page_count"`
	MoodID    *int64 `bun:"mood_id"`
	// Source is manual (placed by someone) or auto (found by mangarr, with a
	// confidence and the detector's version).
	Source       string   `bun:"source,notnull"`
	Confidence   *float64 `bun:"confidence"`
	ModelVersion string   `bun:"model_version,notnull"`
}

// SoundtrackDismissal is a place in a series an admin said a mood found in
// the pages doesn't start (page 0: the chapter's start).
type SoundtrackDismissal struct {
	bun.BaseModel `bun:"table:soundtrack_dismissals"`
	SeriesID      int64     `bun:"series_id,pk"`
	ChapterNumber float64   `bun:"chapter_number,pk"`
	Page          int       `bun:"page,pk"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
}

// Cue sources.
const (
	CueManual = "manual"
	CueAuto   = "auto"
)

// Track kinds.
const (
	TrackAmbience = "ambience"
	TrackSFX      = "sfx"
)

// AudioPack is an installed sound pack.
type AudioPack struct {
	bun.BaseModel `bun:"table:audio_packs"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Slug          string `bun:"slug,notnull" json:"slug"`
	Version       string `bun:"version,notnull" json:"version"`
	Name          string `bun:"name,notnull" json:"name"`
	Description   string `bun:"description,notnull" json:"description"`
	License       string `bun:"license,notnull" json:"license"`
	SourceURL     string `bun:"source_url,notnull" json:"sourceUrl"`
	// Origin is how it came: catalog, upload or folder.
	Origin      string    `bun:"origin,notnull" json:"origin" enum:"catalog,upload,folder"`
	Size        int64     `bun:"size,notnull" json:"size"`
	InstalledAt time.Time `bun:"installed_at,notnull" json:"installedAt"`
}

// AudioTrack is one ambience loop or sound effect from a pack, stored as
// <data>/audio/packs/<pack slug>/<sha256>.<ext>.
type AudioTrack struct {
	bun.BaseModel `bun:"table:audio_tracks"`
	ID            int64    `bun:"id,pk,autoincrement" json:"id"`
	PackID        int64    `bun:"pack_id,notnull" json:"packId"`
	SHA256        string   `bun:"sha256,notnull" json:"sha256"`
	Ext           string   `bun:"ext,notnull" json:"ext"`
	Kind          string   `bun:"kind,notnull" json:"kind" enum:"ambience,sfx"`
	Category      string   `bun:"category,notnull" json:"category"`
	Icon          string   `bun:"icon,notnull" json:"icon"`
	Title         string   `bun:"title,notnull" json:"title"`
	DurationMS    int      `bun:"duration_ms,notnull" json:"durationMs"`
	Size          int64    `bun:"size,notnull" json:"size"`
	GainDB        float64  `bun:"gain_db,notnull" json:"gainDb"`
	Tags          []string `bun:"tags,notnull" json:"tags"`
	License       string   `bun:"license,notnull" json:"license"`
	Attribution   string   `bun:"attribution,notnull" json:"attribution"`
	SourceURL     string   `bun:"source_url,notnull" json:"sourceUrl"`
}

// AudioMoodItem puts a track in a mood: an ambience loop or an SFX pad.
type AudioMoodItem struct {
	bun.BaseModel `bun:"table:audio_mood_items"`
	ID            int64   `bun:"id,pk,autoincrement"`
	MoodID        int64   `bun:"mood_id,notnull"`
	TrackID       int64   `bun:"track_id,notnull"`
	Role          string  `bun:"role,notnull"`
	Volume        float64 `bun:"volume,notnull"`
	Position      int     `bun:"position,notnull"`
	Label         string  `bun:"label,notnull"`
	// Plays is when an ambience loop plays as a reader turns the intensity
	// up or down (Plays*).
	Plays string `bun:"plays,notnull"`
}

// When an ambience loop plays: always (at calm too), from normal (the
// default), or only at intense.
const (
	PlaysAlways  = "always"
	PlaysNormal  = "normal"
	PlaysIntense = "intense"
)

// PageMoods are a chapter file's pages scored against the moods: one
// map of mood id to probability per page, made with a model (Version) and
// a set of prompts (PromptsKey); FileKey is the file's hash.
type PageMoods struct {
	bun.BaseModel `bun:"table:page_moods"`
	FileKey       string               `bun:"file_key,pk"`
	ModelVersion  string               `bun:"model_version,pk"`
	PromptsKey    string               `bun:"prompts_key,notnull"`
	Pages         int                  `bun:"pages,notnull"`
	Scores        []map[string]float64 `bun:"scores,notnull"`
	ScannedAt     time.Time            `bun:"scanned_at,notnull"`
}
