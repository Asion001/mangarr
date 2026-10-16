-- +goose Up
-- Background processing (upscaling, re-encoding) state per chapter file.
-- process_params is the hash of the profile's processing settings the file
-- was last processed with ('' = never, 'force' = process again).
ALTER TABLE chapter_files ADD COLUMN process_params TEXT NOT NULL DEFAULT '';
ALTER TABLE chapter_files ADD COLUMN process_state TEXT NOT NULL DEFAULT '';
ALTER TABLE chapter_files ADD COLUMN process_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chapter_files ADD COLUMN process_retry_at TIMESTAMP;
ALTER TABLE chapter_files ADD COLUMN process_error TEXT NOT NULL DEFAULT '';
ALTER TABLE chapter_files ADD COLUMN processed_at TIMESTAMP;
-- size of the file as downloaded, before any processing (0 = unknown)
ALTER TABLE chapter_files ADD COLUMN size_original INTEGER NOT NULL DEFAULT 0;
UPDATE chapter_files SET size_original = CASE WHEN size_before > 0 THEN size_before ELSE size END;
CREATE INDEX chapter_files_process ON chapter_files (process_params, process_retry_at);

-- +goose Down
DROP INDEX chapter_files_process;
ALTER TABLE chapter_files DROP COLUMN size_original;
ALTER TABLE chapter_files DROP COLUMN processed_at;
ALTER TABLE chapter_files DROP COLUMN process_error;
ALTER TABLE chapter_files DROP COLUMN process_retry_at;
ALTER TABLE chapter_files DROP COLUMN process_attempts;
ALTER TABLE chapter_files DROP COLUMN process_state;
ALTER TABLE chapter_files DROP COLUMN process_params;
