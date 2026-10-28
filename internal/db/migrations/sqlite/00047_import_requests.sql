-- +goose Up
-- A user's library import goes to the admins as one import (requested_by)
-- with one request standing for it (import_id).
ALTER TABLE imports ADD COLUMN requested_by INTEGER;
ALTER TABLE requests ADD COLUMN import_id INTEGER;
CREATE INDEX requests_import ON requests (import_id);

-- +goose Down
DROP INDEX requests_import;
ALTER TABLE requests DROP COLUMN import_id;
ALTER TABLE imports DROP COLUMN requested_by;
