-- +goose Up
-- Processing chapters downloaded before a profile change is now a one-off
-- answer: drop earlier opt-ins so the backlog sweep stops re-queuing chapters
-- the person removed from the queue. The profile dialog asks again on the
-- next processing change.
UPDATE profiles
SET config = json_set(config, '$.processExisting', json('false'))
WHERE json_extract(config, '$.processExisting') = 1;

-- +goose Down
SELECT 1;
