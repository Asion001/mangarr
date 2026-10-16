-- +goose Up
ALTER TABLE workers ADD COLUMN upscale_model TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE workers DROP COLUMN upscale_model;
