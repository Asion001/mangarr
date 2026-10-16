-- +goose Up
ALTER TABLE workers ADD COLUMN priority INTEGER NOT NULL DEFAULT 100;
ALTER TABLE workers ADD COLUMN concurrent INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE workers DROP COLUMN concurrent;
ALTER TABLE workers DROP COLUMN priority;
