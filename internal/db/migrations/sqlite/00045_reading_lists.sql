-- +goose Up
-- A user's reading lists: one status per title (reading, planned, completed,
-- on hold, dropped) and lists of titles, the built-in Favorites and ones the
-- user made. Both are kept per title (a work, or minus a series without one)
-- like reading progress.
CREATE TABLE title_statuses (
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title_id INTEGER NOT NULL,
    status TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, title_id)
);

CREATE TABLE reading_lists (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX reading_lists_user ON reading_lists (user_id);

CREATE TABLE reading_list_titles (
    list_id INTEGER NOT NULL REFERENCES reading_lists (id) ON DELETE CASCADE,
    title_id INTEGER NOT NULL,
    added_at TIMESTAMP NOT NULL,
    PRIMARY KEY (list_id, title_id)
);

-- +goose Down
DROP TABLE reading_list_titles;
DROP TABLE reading_lists;
DROP TABLE title_statuses;
