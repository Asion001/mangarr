-- +goose Up
-- A request can also ask for a title in the library to be downloaded
-- (kind "monitor"): its chapters are there but not monitored.
ALTER TABLE requests ADD COLUMN kind TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE requests DROP COLUMN kind;
