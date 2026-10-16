-- +goose Up
ALTER TABLE download_jobs ADD COLUMN rank BIGINT NOT NULL DEFAULT 0;
WITH ordered AS (
    SELECT id, ROW_NUMBER() OVER (ORDER BY priority DESC, id) * 1048576 AS new_rank
    FROM download_jobs
)
UPDATE download_jobs SET rank = (SELECT new_rank FROM ordered WHERE ordered.id = download_jobs.id);
CREATE TABLE download_queue_order (id INTEGER PRIMARY KEY CHECK (id = 1), revision BIGINT NOT NULL DEFAULT 0);
INSERT INTO download_queue_order (id) VALUES (1);
DROP INDEX download_jobs_dispatch;
CREATE INDEX download_jobs_dispatch ON download_jobs (status, rank, id);
CREATE INDEX download_jobs_rank ON download_jobs (rank, id);

CREATE INDEX download_jobs_enqueue_priority ON download_jobs (priority, rank) WHERE status IN ('queued', 'paused');
CREATE INDEX download_jobs_list ON download_jobs (
    (CASE status WHEN 'importing' THEN 0 WHEN 'processing' THEN 1 WHEN 'downloading' THEN 2
    WHEN 'queued' THEN 3 WHEN 'paused' THEN 3 WHEN 'failed' THEN 4 ELSE 5 END), rank, id
);

-- +goose Down
DROP INDEX download_jobs_list;
DROP INDEX download_jobs_enqueue_priority;
DROP INDEX download_jobs_rank;
DROP INDEX download_jobs_dispatch;
CREATE INDEX download_jobs_dispatch ON download_jobs (status, priority DESC, id);
DROP TABLE download_queue_order;
ALTER TABLE download_jobs DROP COLUMN rank;
