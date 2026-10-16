-- +goose Up
CREATE INDEX chapters_first_seen ON chapters (first_seen_at, id);
CREATE INDEX series_added ON series (added_at, id);
CREATE INDEX works_created ON works (created_at, id);

-- +goose Down
DROP INDEX works_created;
DROP INDEX series_added;
DROP INDEX chapters_first_seen;
