package settings

import "context"

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
