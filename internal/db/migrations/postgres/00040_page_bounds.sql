-- +goose Up
-- Each downloaded page's size and the box inside its borders, measured once
-- (after import, or the first time a reader asks) for cropping. sha256 is the
-- file's: a rewritten file is measured again.
CREATE TABLE page_bounds (
    file_id BIGINT NOT NULL REFERENCES chapter_files (id) ON DELETE CASCADE,
    page    INTEGER NOT NULL,
    sha256  TEXT    NOT NULL DEFAULT '',
    width   INTEGER NOT NULL,
    height  INTEGER NOT NULL,
    x       INTEGER NOT NULL,
    y       INTEGER NOT NULL,
    w       INTEGER NOT NULL,
    h       INTEGER NOT NULL,
    PRIMARY KEY (file_id, page)
);

-- +goose Down
DROP TABLE page_bounds;
