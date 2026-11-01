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
	MoodID        *int64  `bun:"mood_id"`
	SceneID       *int64  `bun:"scene_id"`
}
