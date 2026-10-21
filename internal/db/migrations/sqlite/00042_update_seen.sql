-- +goose Up
-- How far each person has seen the Updates feed: everything at or before
-- the cursor (time, kind rank, id, the feed's own order) is seen, plus the
-- single items marked above it. user_id 0 is a signed-out install (no
-- login, or the API key).
CREATE TABLE update_seen (
    user_id INTEGER PRIMARY KEY,
    seen_at TIMESTAMP NOT NULL,
    seen_rank INTEGER NOT NULL,
    seen_id INTEGER NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE TABLE update_seen_items (
    user_id INTEGER NOT NULL,
    kind TEXT NOT NULL,
    item_id INTEGER NOT NULL,
    at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, kind, item_id)
);

-- +goose Down
DROP TABLE update_seen_items;
DROP TABLE update_seen;
