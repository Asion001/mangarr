-- +goose Up
-- How long the last processing run of a file took and how many pages it
-- covered (for speed, ETA and history).
ALTER TABLE chapter_files ADD COLUMN process_seconds DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE chapter_files ADD COLUMN process_pages INTEGER NOT NULL DEFAULT 0;
CREATE INDEX chapter_files_processed_at ON chapter_files (processed_at);

-- +goose Down
DROP INDEX chapter_files_processed_at;
ALTER TABLE chapter_files DROP COLUMN process_pages;
ALTER TABLE chapter_files DROP COLUMN process_seconds;
