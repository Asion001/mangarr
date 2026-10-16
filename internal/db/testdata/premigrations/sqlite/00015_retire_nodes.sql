-- +goose Up
-- Processing nodes are gone: a machine that upscales is a worker now, with
-- a key of its own. Their module instances were created by their
-- heartbeats, so nothing here was configured by hand.
UPDATE profiles
SET config = json_remove(config, '$.upscale.upscalerId')
WHERE json_extract(config, '$.upscale.upscalerId') IN
      (SELECT id FROM provider_definitions WHERE managed_by LIKE 'node:%');
DELETE FROM provider_definitions WHERE managed_by LIKE 'node:%';
DELETE FROM provider_definitions WHERE kind = 'upscale' AND implementation = 'ncnn-worker';

-- +goose Down
SELECT 1;
