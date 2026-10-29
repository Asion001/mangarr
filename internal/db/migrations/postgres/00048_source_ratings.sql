-- +goose Up
-- A followed source's community score for the title, when the site shows one.
ALTER TABLE series_sources ADD COLUMN rating jsonb;

-- +goose Down
ALTER TABLE series_sources DROP COLUMN rating;
