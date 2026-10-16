-- +goose Up
-- Groups can limit titles by content rating and hide genres or tags.
ALTER TABLE groups ADD COLUMN max_rating TEXT NOT NULL DEFAULT '';
ALTER TABLE groups ADD COLUMN blocked_genres JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE groups DROP COLUMN blocked_genres;
ALTER TABLE groups DROP COLUMN max_rating;
