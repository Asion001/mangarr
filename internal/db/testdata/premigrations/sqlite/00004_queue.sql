-- +goose Up
-- Queue management: per-job priority (higher runs first) and a paused
-- status that still counts as active (one active job per chapter).
ALTER TABLE download_jobs ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
DROP INDEX download_jobs_active_chapter;
CREATE UNIQUE INDEX download_jobs_active_chapter ON download_jobs (chapter_id)
    WHERE status IN ('queued', 'paused', 'downloading', 'processing', 'importing');
CREATE INDEX download_jobs_dispatch ON download_jobs (status, priority DESC, id);

-- +goose Down
DROP INDEX download_jobs_dispatch;
DROP INDEX download_jobs_active_chapter;
UPDATE download_jobs SET status = 'queued' WHERE status = 'paused';
CREATE UNIQUE INDEX download_jobs_active_chapter ON download_jobs (chapter_id)
    WHERE status IN ('queued', 'downloading', 'processing', 'importing');
ALTER TABLE download_jobs DROP COLUMN priority;
