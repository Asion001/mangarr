-- +goose Up
-- A reading app's device can have a language order: a title with several
-- language editions is then shown to it once, chapters in the first
-- language that has them. Empty lists every edition on its own.
ALTER TABLE reading_keys ADD COLUMN languages JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE reading_keys DROP COLUMN languages;
