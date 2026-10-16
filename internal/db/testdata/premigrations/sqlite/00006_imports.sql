-- +goose Up
-- Imports of other apps' backups (Mihon, Tachiyomi, Suwayomi, Aidoku): the
-- upload and its options, and one row per manga with how it was mapped.
CREATE TABLE imports (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    format     TEXT      NOT NULL,
    file_name  TEXT      NOT NULL,
    status     TEXT      NOT NULL,
    progress   TEXT      NOT NULL DEFAULT '',
    options    TEXT      NOT NULL DEFAULT '{}',
    info       TEXT      NOT NULL DEFAULT '{}',
    error      TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE TABLE import_entries (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    import_id  INTEGER   NOT NULL REFERENCES imports (id) ON DELETE CASCADE,
    position   INTEGER   NOT NULL,
    title      TEXT      NOT NULL,
    state      TEXT      NOT NULL,
    selected   BOOLEAN   NOT NULL DEFAULT TRUE,
    data       TEXT      NOT NULL DEFAULT '{}',
    source     TEXT,
    metadata   TEXT,
    extension  TEXT,
    series_id  INTEGER REFERENCES series (id) ON DELETE SET NULL,
    message    TEXT      NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX import_entries_import ON import_entries (import_id, position);

-- Scanlators excluded for one series (literal names, e.g. from Mihon).
ALTER TABLE series ADD COLUMN blocked_scanlators TEXT NOT NULL DEFAULT '[]';
-- Where a read state came from: '' = a library server, 'backup' = an
-- imported backup (kept until a server reports the chapter).
ALTER TABLE chapter_read_states ADD COLUMN origin TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE chapter_read_states DROP COLUMN origin;
ALTER TABLE series DROP COLUMN blocked_scanlators;
DROP INDEX import_entries_import;
DROP TABLE import_entries;
DROP TABLE imports;
