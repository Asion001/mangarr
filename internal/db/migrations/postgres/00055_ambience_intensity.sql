-- +goose Up
-- When an ambience loop plays as a reader turns the intensity up or down:
-- always (calm too), normal (normal and intense, as before) or intense.
ALTER TABLE audio_mood_items ADD COLUMN plays TEXT NOT NULL DEFAULT 'normal';

-- +goose Down
ALTER TABLE audio_mood_items DROP COLUMN plays;
