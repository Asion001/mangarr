-- +goose Up
-- What the duplicate scan found: pairs of titles that look like one work
-- (series_a < series_b, keep_id is the one to keep), and titles without a
-- metadata provider with the match the scan found for them (kind
-- 'metadata', series_b 0, match null when none was found).
CREATE TABLE duplicate_findings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    kind TEXT NOT NULL,
    series_a INTEGER NOT NULL,
    series_b INTEGER NOT NULL DEFAULT 0,
    keep_id INTEGER NOT NULL DEFAULT 0,
    same_language BOOLEAN NOT NULL DEFAULT 0,
    sure BOOLEAN NOT NULL DEFAULT 0,
    reasons TEXT NOT NULL DEFAULT '[]',
    match TEXT,
    state TEXT NOT NULL DEFAULT 'open',
    checked_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX duplicate_findings_key ON duplicate_findings (kind, series_a, series_b);

-- +goose Down
DROP TABLE duplicate_findings;
