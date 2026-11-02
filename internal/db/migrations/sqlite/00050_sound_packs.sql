-- +goose Up
-- Sound packs: ambience loops and sound effects installed from a catalog,
-- an uploaded zip or a folder on the server. Files live in the data
-- folder named by their content hash; scenes layer them over a mood's
-- music. Page cues remember the chapter's page count so a changed chapter
-- gets flagged.
CREATE TABLE audio_packs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    slug TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    license TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    origin TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    installed_at TIMESTAMP NOT NULL
);

CREATE TABLE audio_tracks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    pack_id INTEGER NOT NULL REFERENCES audio_packs (id) ON DELETE CASCADE,
    sha256 TEXT NOT NULL,
    ext TEXT NOT NULL,
    kind TEXT NOT NULL,
    category TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    size INTEGER NOT NULL DEFAULT 0,
    gain_db REAL NOT NULL DEFAULT 0,
    tags TEXT NOT NULL DEFAULT '[]',
    license TEXT NOT NULL DEFAULT '',
    attribution TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX audio_tracks_file ON audio_tracks (pack_id, sha256);
CREATE INDEX audio_tracks_sha256 ON audio_tracks (sha256);

CREATE TABLE audio_scenes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    mood_id INTEGER REFERENCES audio_moods (id) ON DELETE SET NULL,
    spotify_uri TEXT NOT NULL DEFAULT '',
    spotify_name TEXT NOT NULL DEFAULT '',
    spotify_image_url TEXT NOT NULL DEFAULT '',
    shuffle BOOLEAN NOT NULL DEFAULT TRUE,
    pack_id INTEGER REFERENCES audio_packs (id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE audio_scene_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scene_id INTEGER NOT NULL REFERENCES audio_scenes (id) ON DELETE CASCADE,
    track_id INTEGER NOT NULL REFERENCES audio_tracks (id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    volume REAL NOT NULL DEFAULT 1,
    position INTEGER NOT NULL DEFAULT 0,
    label TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audio_scene_items_scene ON audio_scene_items (scene_id);

ALTER TABLE soundtrack_cues ADD COLUMN page_count INTEGER;

-- +goose Down
ALTER TABLE soundtrack_cues DROP COLUMN page_count;
DROP TABLE audio_scene_items;
DROP TABLE audio_scenes;
DROP TABLE audio_tracks;
DROP TABLE audio_packs;
