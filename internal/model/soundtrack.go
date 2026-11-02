package model

import (
	"time"

	"github.com/uptrace/bun"
)

// AudioMood is a soundtrack preset: music from Spotify (a playlist, album or
// track) with a colour and an icon. Slug names the built-in moods mangarr
// seeds ("calm"…); moods admins add have none.
type AudioMood struct {
	bun.BaseModel `bun:"table:audio_moods"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Slug          string `bun:"slug,notnull" json:"slug" doc:"The built-in mood this is (calm, melancholy…); empty for moods admins made"`
	Name          string `bun:"name,notnull" json:"name"`
	Color         string `bun:"color,notnull" json:"color" doc:"#rrggbb"`
	Icon          string `bun:"icon,notnull" json:"icon"`
	// Genres are the genres and tags the mood suits (for suggestions).
	Genres          []string  `bun:"genres,notnull" json:"genres"`
	SpotifyURI      string    `bun:"spotify_uri,notnull" json:"spotifyUri" doc:"spotify:playlist:…, spotify:album:… or spotify:track:…; empty plays nothing"`
	SpotifyName     string    `bun:"spotify_name,notnull" json:"spotifyName"`
	SpotifyImageURL string    `bun:"spotify_image_url,notnull" json:"spotifyImageUrl"`
	Shuffle         bool      `bun:"shuffle,notnull" json:"shuffle"`
	Position        int       `bun:"position,notnull" json:"position"`
	CreatedAt       time.Time `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt       time.Time `bun:"updated_at,notnull" json:"updatedAt"`
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
	SceneID   *int64 `bun:"scene_id"`
}

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

// AudioScene is a mood's music (or its own) with ambience layers and
// sound effect pads.
type AudioScene struct {
	bun.BaseModel   `bun:"table:audio_scenes"`
	ID              int64     `bun:"id,pk,autoincrement"`
	Name            string    `bun:"name,notnull"`
	Description     string    `bun:"description,notnull"`
	MoodID          *int64    `bun:"mood_id"`
	SpotifyURI      string    `bun:"spotify_uri,notnull"`
	SpotifyName     string    `bun:"spotify_name,notnull"`
	SpotifyImageURL string    `bun:"spotify_image_url,notnull"`
	Shuffle         bool      `bun:"shuffle,notnull"`
	PackID          *int64    `bun:"pack_id"`
	CreatedAt       time.Time `bun:"created_at,notnull"`
	UpdatedAt       time.Time `bun:"updated_at,notnull"`
}

// AudioSceneItem puts a track in a scene: an ambience layer or an SFX pad.
type AudioSceneItem struct {
	bun.BaseModel `bun:"table:audio_scene_items"`
	ID            int64   `bun:"id,pk,autoincrement"`
	SceneID       int64   `bun:"scene_id,notnull"`
	TrackID       int64   `bun:"track_id,notnull"`
	Role          string  `bun:"role,notnull"`
	Volume        float64 `bun:"volume,notnull"`
	Position      int     `bun:"position,notnull"`
	Label         string  `bun:"label,notnull"`
}
