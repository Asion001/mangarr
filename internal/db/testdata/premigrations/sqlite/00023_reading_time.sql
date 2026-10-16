-- +goose Up
CREATE TABLE reading_sessions (
    id             TEXT      PRIMARY KEY,
    reader_id      INTEGER   NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    series_id      INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id     INTEGER   NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    active_seconds INTEGER   NOT NULL DEFAULT 0 CHECK (active_seconds >= 0),
    started_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL
);
CREATE INDEX reading_sessions_reader_updated ON reading_sessions (reader_id, updated_at);

-- +goose Down
DROP INDEX reading_sessions_reader_updated;
DROP TABLE reading_sessions;
