-- +goose Up
CREATE TABLE source_priority_lists (scope TEXT PRIMARY KEY, sources JSONB NOT NULL DEFAULT '[]');
ALTER TABLE series ADD COLUMN source_priority_mode TEXT NOT NULL DEFAULT 'custom' CHECK (source_priority_mode IN ('inherit','custom'));

-- +goose Down
ALTER TABLE series DROP COLUMN source_priority_mode;
DROP TABLE source_priority_lists;
