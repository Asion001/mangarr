-- +goose Up
-- KOReader sends the MD5 of its password and a partial MD5 of each file.
ALTER TABLE reading_keys ADD COLUMN koreader_hash TEXT NOT NULL DEFAULT '';

CREATE TABLE koreader_documents (
    reader_id  INTEGER NOT NULL REFERENCES readers (id) ON DELETE CASCADE,
    document   TEXT    NOT NULL,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    PRIMARY KEY (reader_id, document)
);
CREATE INDEX koreader_documents_chapter ON koreader_documents (chapter_id);

-- +goose Down
DROP INDEX koreader_documents_chapter;
DROP TABLE koreader_documents;
ALTER TABLE reading_keys DROP COLUMN koreader_hash;
