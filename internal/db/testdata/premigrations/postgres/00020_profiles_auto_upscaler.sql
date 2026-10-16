-- +goose Up
-- Worker choice is installation-wide now. Profiles only describe the desired
-- image output, and the server picks the first available engine by priority.
UPDATE profiles
SET config = config #- '{upscale,upscalerId}'
WHERE config #> '{upscale,upscalerId}' IS NOT NULL;

-- +goose Down
SELECT 1;
