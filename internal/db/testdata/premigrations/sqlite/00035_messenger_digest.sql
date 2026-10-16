-- +goose Up
-- The daily digest's watermark: chapters announced up to here were sent.
ALTER TABLE messenger_links ADD COLUMN digest_through TIMESTAMP;

-- +goose Down
ALTER TABLE messenger_links DROP COLUMN digest_through;
