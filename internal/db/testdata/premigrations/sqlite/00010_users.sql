-- +goose Up
-- Groups give users permissions and limit the series they see. The built-in
-- Admins and Users groups are created by the app on start.
CREATE TABLE groups (
    id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    name                  TEXT      NOT NULL UNIQUE,
    builtin               TEXT      NOT NULL DEFAULT '',
    permissions           TEXT      NOT NULL DEFAULT '[]',
    include_tags          TEXT      NOT NULL DEFAULT '[]',
    exclude_tags          TEXT      NOT NULL DEFAULT '[]',
    root_folders          TEXT      NOT NULL DEFAULT '[]',
    auto_approve_requests BOOLEAN   NOT NULL DEFAULT FALSE,
    created_at            TIMESTAMP NOT NULL
);
ALTER TABLE users ADD COLUMN group_id INTEGER REFERENCES groups (id);
ALTER TABLE users ADD COLUMN reader_id INTEGER REFERENCES readers (id) ON DELETE SET NULL;
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN disabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN last_login_at TIMESTAMP;
ALTER TABLE users ADD COLUMN oidc_subject TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN created_by INTEGER;
-- Web sessions (the cookie holds the id).
CREATE TABLE sessions (
    id           TEXT PRIMARY KEY,
    user_id      INTEGER   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   TIMESTAMP NOT NULL,
    last_seen_at TIMESTAMP NOT NULL,
    expires_at   TIMESTAMP NOT NULL,
    ip           TEXT      NOT NULL DEFAULT '',
    user_agent   TEXT      NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user ON sessions (user_id);
-- One-time links that let a friend create their account.
CREATE TABLE invites (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT      NOT NULL UNIQUE,
    group_id   INTEGER   NOT NULL REFERENCES groups (id) ON DELETE CASCADE,
    note       TEXT      NOT NULL DEFAULT '',
    max_uses   INTEGER   NOT NULL DEFAULT 1,
    uses       INTEGER   NOT NULL DEFAULT 0,
    expires_at TIMESTAMP,
    created_by INTEGER,
    created_at TIMESTAMP NOT NULL
);
ALTER TABLE reading_keys ADD COLUMN user_id INTEGER REFERENCES users (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE reading_keys DROP COLUMN user_id;
DROP TABLE invites;
DROP INDEX sessions_user;
DROP TABLE sessions;
ALTER TABLE users DROP COLUMN created_by;
ALTER TABLE users DROP COLUMN oidc_subject;
ALTER TABLE users DROP COLUMN last_login_at;
ALTER TABLE users DROP COLUMN disabled;
ALTER TABLE users DROP COLUMN display_name;
ALTER TABLE users DROP COLUMN reader_id;
ALTER TABLE users DROP COLUMN group_id;
DROP TABLE groups;
