-- +goose Up
-- Built-in moods and pack scenes follow defaults kept in the code: edited
-- lists the fields an admin changed (those keep their value), scene is the
-- pack scene a mood was installed as (its name may change later).
ALTER TABLE audio_moods ADD COLUMN edited TEXT NOT NULL DEFAULT '[]';
ALTER TABLE audio_moods ADD COLUMN scene TEXT NOT NULL DEFAULT '';
UPDATE audio_moods SET scene = name WHERE pack_id IS NOT NULL;

-- +goose Down
ALTER TABLE audio_moods DROP COLUMN scene;
ALTER TABLE audio_moods DROP COLUMN edited;
