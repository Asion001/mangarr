-- +goose Up
-- Progress a user imported for titles that weren't on the server: kept with
-- the request that asks for each title and written to the user's reader
-- once the title is added.
CREATE TABLE requested_progress (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL,
    reader_id INTEGER NOT NULL,
    request_id INTEGER NOT NULL,
    title TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX requested_progress_request ON requested_progress (request_id);

-- +goose Down
DROP TABLE requested_progress;
