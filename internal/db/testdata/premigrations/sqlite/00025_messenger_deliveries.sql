-- +goose Up
-- Users link identities on admin-owned Telegram/Discord bots. No user-owned
-- token or callback URL is stored here.
CREATE TABLE messenger_links (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id         INTEGER   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind            TEXT      NOT NULL,
    external_id     TEXT      NOT NULL,
    display_name    TEXT      NOT NULL DEFAULT '',
    mode            TEXT      NOT NULL DEFAULT 'instant',
    events          TEXT      NOT NULL DEFAULT '[]',
    status          TEXT      NOT NULL DEFAULT 'active',
    last_error      TEXT      NOT NULL DEFAULT '',
    last_attempt_at TIMESTAMP,
    created_at      TIMESTAMP NOT NULL,
    updated_at      TIMESTAMP NOT NULL,
    UNIQUE (user_id, kind),
    UNIQUE (kind, external_id)
);

CREATE TABLE messenger_link_tokens (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    token_hash TEXT      NOT NULL UNIQUE,
    user_id    INTEGER   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind       TEXT      NOT NULL,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX messenger_link_tokens_expiry ON messenger_link_tokens (expires_at);

-- The inbox is the source of truth. A dedupe key makes replaying an event
-- idempotent; one dispatch row per linked channel tracks independent retries.
CREATE TABLE notification_deliveries (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER   NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    dedupe_key  TEXT      NOT NULL,
    event_type  TEXT      NOT NULL,
    series_id   INTEGER REFERENCES series (id) ON DELETE SET NULL,
    payload     TEXT      NOT NULL DEFAULT '{}',
    created_at  TIMESTAMP NOT NULL,
    UNIQUE (user_id, dedupe_key)
);
CREATE INDEX notification_deliveries_user_created ON notification_deliveries (user_id, created_at DESC);

CREATE TABLE notification_dispatches (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    delivery_id   INTEGER   NOT NULL REFERENCES notification_deliveries (id) ON DELETE CASCADE,
    link_id       INTEGER   NOT NULL REFERENCES messenger_links (id) ON DELETE CASCADE,
    available_at  TIMESTAMP NOT NULL,
    sent_at       TIMESTAMP,
    attempts      INTEGER   NOT NULL DEFAULT 0,
    last_error    TEXT      NOT NULL DEFAULT '',
    UNIQUE (delivery_id, link_id)
);
CREATE INDEX notification_dispatches_pending ON notification_dispatches (sent_at, available_at);

-- +goose Down
DROP INDEX notification_dispatches_pending;
DROP TABLE notification_dispatches;
DROP INDEX notification_deliveries_user_created;
DROP TABLE notification_deliveries;
DROP INDEX messenger_link_tokens_expiry;
DROP TABLE messenger_link_tokens;
DROP TABLE messenger_links;
