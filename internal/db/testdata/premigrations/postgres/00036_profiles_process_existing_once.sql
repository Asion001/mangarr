-- +goose Up
-- Processing chapters downloaded before a profile change is now a one-off
-- answer: drop earlier opt-ins so the backlog sweep stops re-queuing chapters
-- the person removed from the queue. The profile dialog asks again on the
-- next processing change.
UPDATE profiles
SET config = jsonb_set(config, '{processExisting}', 'false')
WHERE config -> 'processExisting' = 'true';

-- +goose Down
SELECT 1;
