-- +goose Up
-- Sound packs: ambience loops and sound effects installed from a catalog,
-- an uploaded zip or a folder on the server. Files live in the data
-- folder named by their content hash; scenes layer them over a mood's
-- music. Page cues remember the chapter's page count so a changed chapter
-- gets flagged.
CREATE TABLE audio_packs (
    id bigserial PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    version text NOT NULL DEFAULT '',
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    license text NOT NULL DEFAULT '',
    source_url text NOT NULL DEFAULT '',
    origin text NOT NULL DEFAULT '',
    size bigint NOT NULL DEFAULT 0,
    installed_at timestamp with time zone NOT NULL
);

CREATE TABLE audio_tracks (
    id bigserial PRIMARY KEY,
    pack_id bigint NOT NULL REFERENCES audio_packs (id) ON DELETE CASCADE,
    sha256 text NOT NULL,
    ext text NOT NULL,
    kind text NOT NULL,
    category text NOT NULL DEFAULT '',
    icon text NOT NULL DEFAULT '',
    title text NOT NULL,
    duration_ms integer NOT NULL DEFAULT 0,
    size bigint NOT NULL DEFAULT 0,
    gain_db double precision NOT NULL DEFAULT 0,
    tags jsonb NOT NULL DEFAULT '[]'::jsonb,
    license text NOT NULL DEFAULT '',
    attribution text NOT NULL DEFAULT '',
    source_url text NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX audio_tracks_file ON audio_tracks (pack_id, sha256);
CREATE INDEX audio_tracks_sha256 ON audio_tracks (sha256);

CREATE TABLE audio_scenes (
    id bigserial PRIMARY KEY,
    name text NOT NULL,
    description text NOT NULL DEFAULT '',
    mood_id bigint REFERENCES audio_moods (id) ON DELETE SET NULL,
    spotify_uri text NOT NULL DEFAULT '',
    spotify_name text NOT NULL DEFAULT '',
    spotify_image_url text NOT NULL DEFAULT '',
    shuffle boolean NOT NULL DEFAULT true,
    pack_id bigint REFERENCES audio_packs (id) ON DELETE SET NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE TABLE audio_scene_items (
    id bigserial PRIMARY KEY,
    scene_id bigint NOT NULL REFERENCES audio_scenes (id) ON DELETE CASCADE,
    track_id bigint NOT NULL REFERENCES audio_tracks (id) ON DELETE CASCADE,
    role text NOT NULL,
    volume double precision NOT NULL DEFAULT 1,
    position integer NOT NULL DEFAULT 0,
    label text NOT NULL DEFAULT ''
);
CREATE INDEX audio_scene_items_scene ON audio_scene_items (scene_id);

ALTER TABLE soundtrack_cues ADD COLUMN page_count integer;

-- +goose Down
ALTER TABLE soundtrack_cues DROP COLUMN page_count;
DROP TABLE audio_scene_items;
DROP TABLE audio_scenes;
DROP TABLE audio_tracks;
DROP TABLE audio_packs;
