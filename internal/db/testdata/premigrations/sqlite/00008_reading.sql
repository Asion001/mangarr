-- +goose Up
-- Keys reading apps use with the Komga-compatible API (one per device).
CREATE TABLE reading_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    key_hash     TEXT      NOT NULL UNIQUE,
    prefix       TEXT      NOT NULL,
    comment      TEXT      NOT NULL DEFAULT '',
    last_client  TEXT      NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL,
    last_used_at TIMESTAMP
);
-- Progress reports from every origin (apps, library servers, imports), for
-- sync health per device. Old rows are purged by housekeeping.
CREATE TABLE read_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    reader_id  INTEGER   NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    series_id  INTEGER   NOT NULL,
    chapter_id INTEGER   NOT NULL,
    completed  BOOLEAN   NOT NULL DEFAULT FALSE,
    page       INTEGER   NOT NULL DEFAULT 0,
    origin     TEXT      NOT NULL,
    client     TEXT      NOT NULL DEFAULT '',
    device     TEXT      NOT NULL DEFAULT '',
    outcome    TEXT      NOT NULL DEFAULT '',
    at         TIMESTAMP NOT NULL
);
CREATE INDEX read_events_reader ON read_events (reader_id, at);

-- +goose Down
DROP INDEX read_events_reader;
DROP TABLE read_events;
DROP TABLE reading_keys;
