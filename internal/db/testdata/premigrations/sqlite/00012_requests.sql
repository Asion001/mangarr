-- +goose Up
-- Series requests: users ask, managers add them.
CREATE TABLE requests (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    title        TEXT      NOT NULL,
    metadata     TEXT      NOT NULL DEFAULT '{}',
    status       TEXT      NOT NULL,
    reason       TEXT      NOT NULL DEFAULT '',
    series_id    INTEGER   REFERENCES series (id) ON DELETE SET NULL,
    handled_by   INTEGER   REFERENCES users (id) ON DELETE SET NULL,
    created_at   TIMESTAMP NOT NULL,
    updated_at   TIMESTAMP NOT NULL,
    handled_at   TIMESTAMP,
    available_at TIMESTAMP
);
CREATE INDEX requests_status ON requests (status);
-- Who asked (the first requester and anyone who asked for the same series).
CREATE TABLE request_users (
    request_id INTEGER   NOT NULL REFERENCES requests (id) ON DELETE CASCADE,
    user_id    INTEGER   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    note       TEXT      NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (request_id, user_id)
);
-- Series a user follows (their notifications for new chapters).
CREATE TABLE follows (
    user_id    INTEGER   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    series_id  INTEGER   NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, series_id)
);
CREATE INDEX follows_series ON follows (series_id);
-- A user's own notification targets.
ALTER TABLE provider_definitions ADD COLUMN user_id INTEGER REFERENCES users (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE provider_definitions DROP COLUMN user_id;
DROP TABLE follows;
DROP TABLE request_users;
DROP TABLE requests;
