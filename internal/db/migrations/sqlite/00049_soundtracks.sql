-- +goose Up
-- Soundtracks: music from Spotify while reading. Moods are presets (a
-- Spotify playlist, album or track with a colour and an icon); a series'
-- soundtrack is a list of cues naming a mood, shared (user_id NULL) or one
-- reader's own version. Each reader connects their own Spotify account; the
-- refresh token is kept encrypted.
CREATE TABLE audio_moods (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    color TEXT NOT NULL,
    icon TEXT NOT NULL,
    genres TEXT NOT NULL DEFAULT '[]',
    spotify_uri TEXT NOT NULL DEFAULT '',
    spotify_name TEXT NOT NULL DEFAULT '',
    spotify_image_url TEXT NOT NULL DEFAULT '',
    shuffle BOOLEAN NOT NULL DEFAULT TRUE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE spotify_accounts (
    user_id INTEGER PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    spotify_user_id TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    product TEXT NOT NULL DEFAULT '',
    refresh_token_enc TEXT NOT NULL,
    scopes TEXT NOT NULL DEFAULT '',
    connected_at TIMESTAMP NOT NULL
);

CREATE TABLE soundtracks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    user_id INTEGER REFERENCES users (id) ON DELETE CASCADE,
    muted BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX soundtracks_owner ON soundtracks (series_id, COALESCE(user_id, 0));

CREATE TABLE soundtrack_cues (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    soundtrack_id INTEGER NOT NULL REFERENCES soundtracks (id) ON DELETE CASCADE,
    chapter_number REAL NOT NULL DEFAULT 0,
    page INTEGER,
    mood_id INTEGER REFERENCES audio_moods (id) ON DELETE SET NULL,
    scene_id INTEGER
);
CREATE INDEX soundtrack_cues_soundtrack ON soundtrack_cues (soundtrack_id);

-- +goose Down
DROP TABLE soundtrack_cues;
DROP TABLE soundtracks;
DROP TABLE spotify_accounts;
DROP TABLE audio_moods;
