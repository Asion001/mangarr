-- +goose Up
-- Requests each catalog answered per hour, for the reliability view.
CREATE TABLE source_stats (
    module_id bigint NOT NULL REFERENCES provider_definitions (id) ON DELETE CASCADE,
    source_id text NOT NULL,
    hour timestamp with time zone NOT NULL,
    requests integer DEFAULT 0 NOT NULL,
    failures integer DEFAULT 0 NOT NULL,
    throttled integer DEFAULT 0 NOT NULL,
    fallbacks integer DEFAULT 0 NOT NULL,
    total_ms bigint DEFAULT 0 NOT NULL,
    lat_0 integer DEFAULT 0 NOT NULL,
    lat_1 integer DEFAULT 0 NOT NULL,
    lat_2 integer DEFAULT 0 NOT NULL,
    lat_3 integer DEFAULT 0 NOT NULL,
    lat_4 integer DEFAULT 0 NOT NULL,
    lat_5 integer DEFAULT 0 NOT NULL,
    lat_6 integer DEFAULT 0 NOT NULL,
    lat_7 integer DEFAULT 0 NOT NULL,
    PRIMARY KEY (module_id, source_id, hour)
);
CREATE INDEX source_stats_hour ON source_stats (hour);

-- +goose Down
DROP TABLE source_stats;
