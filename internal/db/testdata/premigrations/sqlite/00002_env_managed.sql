-- +goose Up
-- managed_by marks rows defined by environment variables ("env" / "env:<NAME>");
-- they are kept in sync at startup and can't be edited or deleted in the UI.
ALTER TABLE provider_definitions ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';
ALTER TABLE root_folders ADD COLUMN managed_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE provider_definitions DROP COLUMN managed_by;
ALTER TABLE root_folders DROP COLUMN managed_by;
