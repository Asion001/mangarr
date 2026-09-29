-- +goose Up
-- A preview is a title opened from search without adding it: its chapters
-- stream from the source, and it stays out of the library until added.
ALTER TABLE series ADD COLUMN preview BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE series ADD COLUMN preview_seen_at TIMESTAMPTZ;
CREATE INDEX series_preview ON series (preview);

-- +goose Down
DROP INDEX series_preview;
ALTER TABLE series DROP COLUMN preview_seen_at;
ALTER TABLE series DROP COLUMN preview;
