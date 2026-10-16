-- +goose Up
-- Web reader settings: a user's defaults (series_id 0) and per-series ones.
CREATE TABLE reader_prefs (
    user_id    BIGINT      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    series_id  BIGINT      NOT NULL DEFAULT 0,
    data       JSONB       NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (user_id, series_id)
);

-- +goose Down
DROP TABLE reader_prefs;
