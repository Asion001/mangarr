-- +goose Up
-- Per-catalog preferences (one catalog = one source of a source module) and
-- throttling state. A catalog without a row is enabled with default priority.
CREATE TABLE catalog_prefs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    module_id        INTEGER   NOT NULL REFERENCES provider_definitions (id) ON DELETE CASCADE,
    source_id        TEXT      NOT NULL,
    enabled          BOOLEAN   NOT NULL DEFAULT TRUE,
    priority         INTEGER   NOT NULL DEFAULT 100,
    throttle         TEXT      NOT NULL DEFAULT '{}',
    cooldown_until   TIMESTAMP,
    cooldown_strikes INTEGER   NOT NULL DEFAULT 0,
    last_throttle    TEXT      NOT NULL DEFAULT '',
    updated_at       TIMESTAMP NOT NULL,
    UNIQUE (module_id, source_id)
);

-- +goose Down
DROP TABLE catalog_prefs;
