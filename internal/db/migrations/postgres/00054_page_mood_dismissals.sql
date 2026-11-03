-- +goose Up
-- Places an admin said a mood found in the pages doesn't start ("Not
-- here"): finding moods again leaves them be. Page 0 is the chapter's start.
CREATE TABLE soundtrack_dismissals (
    series_id bigint NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_number double precision NOT NULL,
    page integer NOT NULL DEFAULT 0,
    created_at timestamp with time zone NOT NULL,
    PRIMARY KEY (series_id, chapter_number, page)
);

-- +goose Down
DROP TABLE soundtrack_dismissals;
