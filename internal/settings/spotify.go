package settings

import (
	"context"
	"errors"
	"net/url"
	"strings"
)

// KeySpotify holds the Spotify app soundtracks play music through.
const KeySpotify = "spotify"

// Spotify is the admin's Spotify developer app. Each reader connects their
// own Spotify account to it (Authorization Code with PKCE, so there is no
// client secret).
type Spotify struct {
	ClientID string `json:"clientId"`
}

func (s *Store) Spotify(ctx context.Context) (Spotify, error) {
	var v Spotify
	return v, s.Get(ctx, KeySpotify, &v)
}

// KeySoundtracks remembers what mangarr set up for soundtracks.
const KeySoundtracks = "soundtracks"

// Soundtracks: MoodsSeeded is set once the built-in moods were made, so
// moods admins delete stay deleted.
type Soundtracks struct {
	MoodsSeeded bool `json:"moodsSeeded"`
}

// KeySound holds the sound pack and suggestion settings.
const KeySound = "sound"

// Sound configures sound packs and soundtrack suggestions.
type Sound struct {
	// CatalogURL lists packs to install (a catalog.json); empty: none.
	CatalogURL string `json:"catalogUrl"`
	// QuotaMB warns when installed packs take more than this (0: never).
	QuotaMB int `json:"quotaMb" minimum:"0"`
	// SuggestFromGenres plays the mood matching a title's genres when it
	// has no title mood.
	SuggestFromGenres bool `json:"suggestFromGenres"`
	// AutoMoods finds moods from chapter titles (after imports, and when
	// asked) and places them as automatic cues.
	AutoMoods bool `json:"autoMoods" required:"false"`
}

// DefaultCatalogURL lists the CC0 starter packs (github.com/Asion001/mangarr-sounds).
const DefaultCatalogURL = "https://github.com/Asion001/mangarr-sounds/releases/latest/download/catalog.json"

func DefaultSound() Sound { return Sound{CatalogURL: DefaultCatalogURL, QuotaMB: 2048} }

func (s *Store) Sound(ctx context.Context) (Sound, error) {
	v := DefaultSound()
	return v, s.Get(ctx, KeySound, &v)
}

func (s Sound) Validate() error {
	if s.QuotaMB < 0 {
		return errors.New("the quota can't be negative")
	}
	if c := strings.TrimSpace(s.CatalogURL); c != "" {
		u, err := url.Parse(c)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
			return errors.New("the catalog URL must be an http(s) link")
		}
	}
	return nil
}
