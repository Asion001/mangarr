-- +goose Up
-- One piece of work handed to a worker. A download job becomes one download
-- task, and later upscale and encode tasks, so this is a table of its own
-- rather than columns on download_jobs — and the finished rows are what the
-- worker statistics are counted from.
CREATE TABLE worker_tasks (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id       INTEGER   NOT NULL REFERENCES download_jobs (id) ON DELETE CASCADE,
    worker_id    INTEGER   REFERENCES workers (id) ON DELETE SET NULL,
    kind         TEXT      NOT NULL,
    seq          INTEGER   NOT NULL DEFAULT 0,
    state        TEXT      NOT NULL,
    spec         TEXT      NOT NULL DEFAULT '{}',
    not_before   TIMESTAMP NOT NULL,
    lease_until  TIMESTAMP,
    heartbeat_at TIMESTAMP,
    cancel       INTEGER   NOT NULL DEFAULT 0,
    attempt      INTEGER   NOT NULL DEFAULT 0,
    pages_total  INTEGER   NOT NULL DEFAULT 0,
    pages_done   INTEGER   NOT NULL DEFAULT 0,
    bytes_in     INTEGER   NOT NULL DEFAULT 0,
    bytes_out    INTEGER   NOT NULL DEFAULT 0,
    error        TEXT      NOT NULL DEFAULT '',
    created_at   TIMESTAMP NOT NULL,
    started_at   TIMESTAMP,
    finished_at  TIMESTAMP
);
CREATE INDEX worker_tasks_waiting ON worker_tasks (state, kind, not_before);
CREATE INDEX worker_tasks_leases ON worker_tasks (state, lease_until);
CREATE INDEX worker_tasks_job ON worker_tasks (job_id);
CREATE INDEX worker_tasks_worker ON worker_tasks (worker_id, state);

-- +goose Down
DROP TABLE worker_tasks;
