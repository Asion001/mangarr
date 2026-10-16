-- +goose Up
ALTER TABLE workers ADD COLUMN page_concurrency INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE workers DROP COLUMN page_concurrency;
