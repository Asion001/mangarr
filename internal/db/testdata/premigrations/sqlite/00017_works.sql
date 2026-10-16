-- +goose Up
-- A work is the canonical title; series remain language-specific editions
-- with their own files, sources, profile, progress and queue state.
CREATE TABLE works (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT      NOT NULL,
    sort_title TEXT      NOT NULL,
    metadata   TEXT      NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
ALTER TABLE series ADD COLUMN work_id INTEGER REFERENCES works (id);
INSERT INTO works (id, title, sort_title, metadata, created_at, updated_at)
SELECT id, title, sort_title, metadata, added_at, updated_at FROM series;
UPDATE series SET work_id = id WHERE work_id IS NULL;
CREATE INDEX series_work ON series (work_id);

-- +goose Down
DROP INDEX series_work;
ALTER TABLE series DROP COLUMN work_id;
DROP TABLE works;
