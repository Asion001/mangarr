-- +goose Up
-- Requests each catalog answered per hour, for the reliability view.
CREATE TABLE source_stats (
    module_id INTEGER NOT NULL REFERENCES provider_definitions (id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    hour TIMESTAMP NOT NULL,
    requests INTEGER NOT NULL DEFAULT 0,
    failures INTEGER NOT NULL DEFAULT 0,
    throttled INTEGER NOT NULL DEFAULT 0,
    fallbacks INTEGER NOT NULL DEFAULT 0,
    total_ms INTEGER NOT NULL DEFAULT 0,
    lat_0 INTEGER NOT NULL DEFAULT 0,
    lat_1 INTEGER NOT NULL DEFAULT 0,
    lat_2 INTEGER NOT NULL DEFAULT 0,
    lat_3 INTEGER NOT NULL DEFAULT 0,
    lat_4 INTEGER NOT NULL DEFAULT 0,
    lat_5 INTEGER NOT NULL DEFAULT 0,
    lat_6 INTEGER NOT NULL DEFAULT 0,
    lat_7 INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (module_id, source_id, hour)
);
CREATE INDEX source_stats_hour ON source_stats (hour);

-- +goose Down
DROP TABLE source_stats;
