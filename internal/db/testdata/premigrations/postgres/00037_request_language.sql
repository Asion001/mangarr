-- +goose Up
-- A request can ask for one language edition, also of a title already in
-- the library (work_id).
ALTER TABLE requests ADD COLUMN language TEXT NOT NULL DEFAULT '';
ALTER TABLE requests ADD COLUMN work_id BIGINT;

-- +goose Down
ALTER TABLE requests DROP COLUMN work_id;
ALTER TABLE requests DROP COLUMN language;
