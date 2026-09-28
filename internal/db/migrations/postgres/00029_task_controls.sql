-- +goose Up
ALTER TABLE scheduled_tasks ADD COLUMN paused BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE scheduled_tasks ADD COLUMN schedule_kind TEXT;
ALTER TABLE scheduled_tasks ADD COLUMN custom_interval_minutes INTEGER;
ALTER TABLE scheduled_tasks ADD COLUMN times_of_day TEXT;
ALTER TABLE scheduled_tasks ADD COLUMN weekdays TEXT;
ALTER TABLE scheduled_tasks ADD COLUMN updated_at TIMESTAMPTZ;
UPDATE scheduled_tasks SET updated_at = CURRENT_TIMESTAMP;
-- Preserve the reader-sync interval previously stored as the code default.
UPDATE scheduled_tasks SET schedule_kind = 'interval', custom_interval_minutes = interval_minutes
WHERE name = 'SyncReadProgress' AND interval_minutes <> 30;
CREATE INDEX commands_name_id ON commands (name, id DESC);

-- +goose Down
DROP INDEX commands_name_id;
ALTER TABLE scheduled_tasks DROP COLUMN updated_at;
ALTER TABLE scheduled_tasks DROP COLUMN weekdays;
ALTER TABLE scheduled_tasks DROP COLUMN times_of_day;
ALTER TABLE scheduled_tasks DROP COLUMN custom_interval_minutes;
ALTER TABLE scheduled_tasks DROP COLUMN schedule_kind;
ALTER TABLE scheduled_tasks DROP COLUMN paused;
