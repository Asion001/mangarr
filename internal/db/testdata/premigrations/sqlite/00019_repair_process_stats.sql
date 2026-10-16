-- +goose Up
-- Earlier no-op processing runs counted every inspected page against a
-- microsecond duration. Remove only physically impossible historical rates.
UPDATE chapter_files
SET process_seconds = 0, process_pages = 0
WHERE process_seconds > 0 AND process_pages > process_seconds * 1000;

-- +goose Down
-- Data repair is intentionally irreversible.
