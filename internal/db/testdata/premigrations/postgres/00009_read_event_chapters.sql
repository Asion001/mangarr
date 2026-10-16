-- +goose Up
-- One event can stand for several chapters (a series marked read, a sync):
-- chapter_id is then the highest-numbered one.
ALTER TABLE read_events ADD COLUMN chapters INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE read_events DROP COLUMN chapters;
