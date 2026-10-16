-- +goose Up
-- Workers are machines that do work for this server: they hold a key of
-- their own, dial in, and ask for tasks. The counters are their lifetime
-- totals (worker_tasks keeps the detail).
CREATE TABLE workers (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT      NOT NULL UNIQUE,
    key_hash      TEXT      NOT NULL UNIQUE,
    prefix        TEXT      NOT NULL,
    roles         TEXT      NOT NULL DEFAULT '[]',
    enabled       INTEGER   NOT NULL DEFAULT 1,
    version       TEXT      NOT NULL DEFAULT '',
    platform      TEXT      NOT NULL DEFAULT '',
    info          TEXT      NOT NULL DEFAULT '{}',
    last_ip       TEXT      NOT NULL DEFAULT '',
    created_by    INTEGER   REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMP NOT NULL,
    last_seen_at  TIMESTAMP,
    tasks_done    INTEGER   NOT NULL DEFAULT 0,
    tasks_failed  INTEGER   NOT NULL DEFAULT 0,
    pages_done    INTEGER   NOT NULL DEFAULT 0,
    bytes_in      INTEGER   NOT NULL DEFAULT 0,
    bytes_out     INTEGER   NOT NULL DEFAULT 0,
    busy_seconds  REAL      NOT NULL DEFAULT 0
);

-- +goose Down
DROP TABLE workers;
