-- +goose Up
-- Moods and scenes become one: a mood has its Spotify music and its own
-- ambience loops and sound effect pads. Every scene becomes a mood (keeping
-- its music, or its mood's, and its sounds) and the cues that started it
-- start that mood. A built-in mood without sounds takes those of the first
-- pack scene made for it.
ALTER TABLE audio_moods ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE audio_moods ADD COLUMN pack_id INTEGER REFERENCES audio_packs (id) ON DELETE SET NULL;
ALTER TABLE audio_moods ADD COLUMN scene_id INTEGER;

CREATE TABLE audio_mood_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    mood_id INTEGER NOT NULL REFERENCES audio_moods (id) ON DELETE CASCADE,
    track_id INTEGER NOT NULL REFERENCES audio_tracks (id) ON DELETE CASCADE,
    role TEXT NOT NULL,
    volume REAL NOT NULL DEFAULT 1,
    position INTEGER NOT NULL DEFAULT 0,
    label TEXT NOT NULL DEFAULT ''
);
CREATE INDEX audio_mood_items_mood ON audio_mood_items (mood_id);

INSERT INTO audio_moods (slug, name, color, icon, genres, prompts, keywords, spotify_uri, spotify_name, spotify_image_url, shuffle, position,
    created_at, updated_at, description, pack_id, scene_id)
SELECT '', s.name, COALESCE(m.color, '#9aa2b1'), COALESCE(m.icon, 'music'), '[]', '[]', '[]',
    CASE WHEN s.spotify_uri <> '' THEN s.spotify_uri ELSE COALESCE(m.spotify_uri, '') END,
    CASE WHEN s.spotify_uri <> '' THEN s.spotify_name ELSE COALESCE(m.spotify_name, '') END,
    CASE WHEN s.spotify_uri <> '' THEN s.spotify_image_url ELSE COALESCE(m.spotify_image_url, '') END,
    CASE WHEN s.spotify_uri <> '' THEN s.shuffle ELSE COALESCE(m.shuffle, s.shuffle) END,
    (SELECT COALESCE(MAX(position), 0) FROM audio_moods) + s.id,
    s.created_at, s.updated_at, s.description, s.pack_id, s.id
FROM audio_scenes AS s LEFT JOIN audio_moods AS m ON m.id = s.mood_id;

INSERT INTO audio_mood_items (mood_id, track_id, role, volume, position, label)
SELECT am.id, i.track_id, i.role, i.volume, i.position, i.label
FROM audio_scene_items AS i JOIN audio_moods AS am ON am.scene_id = i.scene_id;

INSERT INTO audio_mood_items (mood_id, track_id, role, volume, position, label)
SELECT b.id, i.track_id, i.role, i.volume, i.position, i.label
FROM audio_moods AS b JOIN audio_scene_items AS i ON i.scene_id = (SELECT MIN(s.id) FROM audio_scenes AS s WHERE s.mood_id = b.id AND s.pack_id IS NOT NULL)
WHERE b.slug <> '' AND b.scene_id IS NULL AND NOT EXISTS (SELECT 1 FROM audio_mood_items AS x WHERE x.mood_id = b.id);

UPDATE soundtrack_cues SET mood_id = (SELECT am.id FROM audio_moods AS am WHERE am.scene_id = soundtrack_cues.scene_id)
WHERE scene_id IS NOT NULL;

ALTER TABLE soundtrack_cues DROP COLUMN scene_id;
ALTER TABLE audio_moods DROP COLUMN scene_id;
DROP TABLE audio_scene_items;
DROP TABLE audio_scenes;

-- +goose Down
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
ALTER TABLE soundtrack_cues ADD COLUMN scene_id INTEGER;
DROP TABLE audio_mood_items;
ALTER TABLE audio_moods DROP COLUMN description;
