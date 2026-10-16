-- +goose Up
CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT      NOT NULL UNIQUE,
    password_hash TEXT      NOT NULL,
    created_at    TIMESTAMP NOT NULL
);

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT      NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE tags (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    label TEXT NOT NULL UNIQUE
);

CREATE TABLE root_folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT      NOT NULL UNIQUE,
    language   TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);

CREATE TABLE profiles (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT      NOT NULL UNIQUE,
    is_default BOOLEAN   NOT NULL DEFAULT FALSE,
    config     TEXT      NOT NULL DEFAULT '{}',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE provider_definitions (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    kind           TEXT      NOT NULL,
    implementation TEXT      NOT NULL,
    name           TEXT      NOT NULL,
    enabled        BOOLEAN   NOT NULL DEFAULT TRUE,
    priority       INTEGER   NOT NULL DEFAULT 25,
    tags           TEXT      NOT NULL DEFAULT '[]',
    events         TEXT      NOT NULL DEFAULT '[]',
    settings       TEXT      NOT NULL DEFAULT '{}',
    created_at     TIMESTAMP NOT NULL,
    updated_at     TIMESTAMP NOT NULL
);
CREATE INDEX provider_definitions_kind ON provider_definitions (kind);

CREATE TABLE series (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    title                 TEXT      NOT NULL,
    sort_title            TEXT      NOT NULL,
    status                TEXT      NOT NULL DEFAULT 'unknown',
    monitored             BOOLEAN   NOT NULL DEFAULT TRUE,
    monitor_new           TEXT      NOT NULL DEFAULT 'all',
    root_folder_id        INTEGER   NOT NULL REFERENCES root_folders (id),
    path                  TEXT      NOT NULL,
    profile_id            INTEGER   NOT NULL REFERENCES profiles (id),
    language              TEXT      NOT NULL DEFAULT '',
    reading_direction     TEXT      NOT NULL DEFAULT 'rtl',
    tags                  TEXT      NOT NULL DEFAULT '[]',
    metadata              TEXT      NOT NULL DEFAULT '{}',
    add_options           TEXT      NOT NULL DEFAULT '{}',
    added_at              TIMESTAMP NOT NULL,
    updated_at            TIMESTAMP NOT NULL,
    last_metadata_refresh TIMESTAMP
);
CREATE UNIQUE INDEX series_root_path ON series (root_folder_id, path);

CREATE TABLE series_sources (
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id              INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    module_id              INTEGER   NOT NULL,
    source_id              TEXT      NOT NULL,
    source_name            TEXT      NOT NULL DEFAULT '',
    lang                   TEXT      NOT NULL DEFAULT '',
    manga_url              TEXT      NOT NULL,
    title                  TEXT      NOT NULL DEFAULT '',
    web_url                TEXT      NOT NULL DEFAULT '',
    engine_ref             TEXT      NOT NULL DEFAULT '',
    priority               INTEGER   NOT NULL DEFAULT 0,
    enabled                BOOLEAN   NOT NULL DEFAULT TRUE,
    check_interval_minutes INTEGER   NOT NULL DEFAULT 0,
    last_checked_at        TIMESTAMP,
    last_success_at        TIMESTAMP,
    next_check_at          TIMESTAMP NOT NULL,
    consecutive_failures   INTEGER   NOT NULL DEFAULT 0,
    backoff_until          TIMESTAMP,
    last_error             TEXT      NOT NULL DEFAULT '',
    created_at             TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX series_sources_identity ON series_sources (series_id, source_id, manga_url);
CREATE INDEX series_sources_next_check ON series_sources (next_check_at);

CREATE TABLE chapters (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id     INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    number_key    TEXT      NOT NULL,
    number_sort   REAL      NOT NULL,
    volume        TEXT      NOT NULL DEFAULT '',
    title         TEXT      NOT NULL DEFAULT '',
    monitored     BOOLEAN   NOT NULL DEFAULT TRUE,
    state         TEXT      NOT NULL DEFAULT 'missing',
    file_id       INTEGER,
    cleaned_at    TIMESTAMP,
    release_date  TIMESTAMP,
    first_seen_at TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX chapters_series_number ON chapters (series_id, number_key);
CREATE INDEX chapters_state ON chapters (state);

CREATE TABLE chapter_releases (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id        INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id       INTEGER REFERENCES chapters (id) ON DELETE SET NULL,
    series_source_id INTEGER   NOT NULL REFERENCES series_sources (id) ON DELETE CASCADE,
    chapter_url      TEXT      NOT NULL,
    web_url          TEXT      NOT NULL DEFAULT '',
    engine_ref       TEXT      NOT NULL DEFAULT '',
    name             TEXT      NOT NULL DEFAULT '',
    scanlator        TEXT      NOT NULL DEFAULT '',
    raw_number       REAL      NOT NULL DEFAULT -1,
    upload_date      TIMESTAMP,
    removed          BOOLEAN   NOT NULL DEFAULT FALSE,
    created_at       TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX chapter_releases_identity ON chapter_releases (series_source_id, chapter_url);
CREATE INDEX chapter_releases_chapter ON chapter_releases (chapter_id);

CREATE TABLE chapter_files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    chapter_id    INTEGER   NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    series_id     INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    relative_path TEXT      NOT NULL,
    size          INTEGER   NOT NULL DEFAULT 0,
    page_count    INTEGER   NOT NULL DEFAULT 0,
    avg_width     INTEGER   NOT NULL DEFAULT 0,
    format        TEXT      NOT NULL DEFAULT '',
    release_id    INTEGER,
    scanlator     TEXT      NOT NULL DEFAULT '',
    source_name   TEXT      NOT NULL DEFAULT '',
    sha256        TEXT      NOT NULL DEFAULT '',
    upscaled      BOOLEAN   NOT NULL DEFAULT FALSE,
    upscale_model TEXT      NOT NULL DEFAULT '',
    size_before   INTEGER   NOT NULL DEFAULT 0,
    imported_at   TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX chapter_files_chapter ON chapter_files (chapter_id);

CREATE TABLE download_jobs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    kind        TEXT      NOT NULL DEFAULT 'download',
    series_id   INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id  INTEGER   NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    release_id  INTEGER,
    status      TEXT      NOT NULL,
    progress    INTEGER   NOT NULL DEFAULT 0,
    pages_done  INTEGER   NOT NULL DEFAULT 0,
    pages_total INTEGER   NOT NULL DEFAULT 0,
    attempt     INTEGER   NOT NULL DEFAULT 0,
    is_upgrade  BOOLEAN   NOT NULL DEFAULT FALSE,
    error       TEXT      NOT NULL DEFAULT '',
    not_before  TIMESTAMP NOT NULL,
    created_at  TIMESTAMP NOT NULL,
    updated_at  TIMESTAMP NOT NULL,
    started_at  TIMESTAMP
);
CREATE INDEX download_jobs_status ON download_jobs (status);
-- at most one active job per chapter (enqueue races resolve to the existing job)
CREATE UNIQUE INDEX download_jobs_active_chapter ON download_jobs (chapter_id)
    WHERE status IN ('queued', 'downloading', 'processing', 'importing');

CREATE TABLE history (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id    INTEGER   NOT NULL,
    chapter_id   INTEGER,
    event_type   TEXT      NOT NULL,
    source_title TEXT      NOT NULL DEFAULT '',
    data         TEXT      NOT NULL DEFAULT '{}',
    created_at   TIMESTAMP NOT NULL
);
CREATE INDEX history_series ON history (series_id);
CREATE INDEX history_created ON history (created_at);

CREATE TABLE blocklist (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id        INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id       INTEGER,
    series_source_id INTEGER   NOT NULL,
    chapter_url      TEXT      NOT NULL,
    scanlator        TEXT      NOT NULL DEFAULT '',
    reason           TEXT      NOT NULL DEFAULT '',
    created_at       TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX blocklist_identity ON blocklist (series_source_id, chapter_url);

CREATE TABLE commands (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    name        TEXT      NOT NULL,
    body        TEXT      NOT NULL DEFAULT '{}',
    status      TEXT      NOT NULL,
    "trigger"   TEXT      NOT NULL DEFAULT 'manual',
    message     TEXT      NOT NULL DEFAULT '',
    queued_at   TIMESTAMP NOT NULL,
    started_at  TIMESTAMP,
    ended_at    TIMESTAMP,
    duration_ms INTEGER   NOT NULL DEFAULT 0,
    error       TEXT      NOT NULL DEFAULT ''
);
CREATE INDEX commands_status ON commands (status);

CREATE TABLE scheduled_tasks (
    name             TEXT PRIMARY KEY,
    interval_minutes INTEGER NOT NULL,
    last_execution   TIMESTAMP,
    last_start       TIMESTAMP
);

CREATE TABLE readers (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    name              TEXT      NOT NULL UNIQUE,
    count_for_cleanup BOOLEAN   NOT NULL DEFAULT TRUE,
    created_at        TIMESTAMP NOT NULL
);

CREATE TABLE reader_accounts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    reader_id     INTEGER   NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    module_id     INTEGER   NOT NULL,
    credentials   TEXT      NOT NULL DEFAULT '{}',
    external_user TEXT      NOT NULL DEFAULT '',
    last_sync_at  TIMESTAMP,
    last_error    TEXT      NOT NULL DEFAULT '',
    created_at    TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX reader_accounts_identity ON reader_accounts (reader_id, module_id);

CREATE TABLE chapter_read_states (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    reader_id  INTEGER   NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    chapter_id INTEGER   NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    series_id  INTEGER   NOT NULL,
    completed  BOOLEAN   NOT NULL DEFAULT FALSE,
    page       INTEGER   NOT NULL DEFAULT 0,
    read_at    TIMESTAMP,
    synced_at  TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX chapter_read_states_identity ON chapter_read_states (reader_id, chapter_id);
CREATE INDEX chapter_read_states_series ON chapter_read_states (series_id);

-- +goose Down
DROP TABLE chapter_read_states;
DROP TABLE reader_accounts;
DROP TABLE readers;
DROP TABLE scheduled_tasks;
DROP TABLE commands;
DROP TABLE blocklist;
DROP TABLE history;
DROP TABLE download_jobs;
DROP TABLE chapter_files;
DROP TABLE chapter_releases;
DROP TABLE chapters;
DROP TABLE series_sources;
DROP TABLE series;
DROP TABLE provider_definitions;
DROP TABLE profiles;
DROP TABLE root_folders;
DROP TABLE tags;
DROP TABLE settings;
DROP TABLE users;
