-- +goose Up
-- Worker choice is installation-wide now. Profiles only describe the desired
-- image output, and the server picks the first available engine by priority.
UPDATE profiles
SET config = json_remove(config, '$.upscale.upscalerId')
WHERE json_type(config, '$.upscale.upscalerId') IS NOT NULL;

-- +goose Down
SELECT 1;
