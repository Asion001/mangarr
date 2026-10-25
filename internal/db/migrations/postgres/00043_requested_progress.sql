-- +goose Up
-- Progress a user imported for titles that weren't on the server: kept with
-- the request that asks for each title and written to the user's reader
-- once the title is added.
CREATE TABLE requested_progress (
    id bigserial PRIMARY KEY,
    user_id bigint NOT NULL,
    reader_id bigint NOT NULL,
    request_id bigint NOT NULL,
    title text NOT NULL,
    created_at timestamp with time zone NOT NULL
);
CREATE INDEX requested_progress_request ON requested_progress (request_id);

-- +goose Down
DROP TABLE requested_progress;
