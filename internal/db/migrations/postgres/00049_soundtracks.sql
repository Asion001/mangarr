-- +goose Up
-- Soundtracks: music from Spotify while reading. Moods are presets (a
-- Spotify playlist, album or track with a colour and an icon); a series'
-- soundtrack is a list of cues naming a mood, shared (user_id NULL) or one
-- reader's own version. Each reader connects their own Spotify account; the
-- refresh token is kept encrypted.
CREATE TABLE audio_moods (
    id bigserial PRIMARY KEY,
    slug text NOT NULL DEFAULT '',
    name text NOT NULL,
    color text NOT NULL,
    icon text NOT NULL,
    genres jsonb NOT NULL DEFAULT '[]'::jsonb,
    spotify_uri text NOT NULL DEFAULT '',
    spotify_name text NOT NULL DEFAULT '',
    spotify_image_url text NOT NULL DEFAULT '',
    shuffle boolean NOT NULL DEFAULT true,
    position integer NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE TABLE spotify_accounts (
    user_id bigint PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    spotify_user_id text NOT NULL,
    display_name text NOT NULL DEFAULT '',
    product text NOT NULL DEFAULT '',
    refresh_token_enc text NOT NULL,
    scopes text NOT NULL DEFAULT '',
    connected_at timestamp with time zone NOT NULL
);

CREATE TABLE soundtracks (
    id bigserial PRIMARY KEY,
    series_id bigint NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    user_id bigint REFERENCES users (id) ON DELETE CASCADE,
    muted boolean NOT NULL DEFAULT false,
    updated_at timestamp with time zone NOT NULL
);
CREATE UNIQUE INDEX soundtracks_owner ON soundtracks (series_id, COALESCE(user_id, 0));

CREATE TABLE soundtrack_cues (
    id bigserial PRIMARY KEY,
    soundtrack_id bigint NOT NULL REFERENCES soundtracks (id) ON DELETE CASCADE,
    chapter_number double precision NOT NULL DEFAULT 0,
    page integer,
    mood_id bigint REFERENCES audio_moods (id) ON DELETE SET NULL,
    scene_id bigint
);
CREATE INDEX soundtrack_cues_soundtrack ON soundtrack_cues (soundtrack_id);

-- +goose Down
DROP TABLE soundtrack_cues;
DROP TABLE soundtracks;
DROP TABLE spotify_accounts;
DROP TABLE audio_moods;
