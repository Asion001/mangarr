-- +goose Up
-- A user's reading lists: one status per title (reading, planned, completed,
-- on hold, dropped) and lists of titles, the built-in Favorites and ones the
-- user made. Both are kept per title (a work, or minus a series without one)
-- like reading progress.
CREATE TABLE title_statuses (
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title_id bigint NOT NULL,
    status text NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    PRIMARY KEY (user_id, title_id)
);

CREATE TABLE reading_lists (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name text NOT NULL,
    kind text NOT NULL DEFAULT '',
    created_at timestamp with time zone NOT NULL
);
CREATE INDEX reading_lists_user ON reading_lists (user_id);

CREATE TABLE reading_list_titles (
    list_id bigint NOT NULL REFERENCES reading_lists (id) ON DELETE CASCADE,
    title_id bigint NOT NULL,
    added_at timestamp with time zone NOT NULL,
    PRIMARY KEY (list_id, title_id)
);

-- +goose Down
DROP TABLE reading_list_titles;
DROP TABLE reading_lists;
DROP TABLE title_statuses;
