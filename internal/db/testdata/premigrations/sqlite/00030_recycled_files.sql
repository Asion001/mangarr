-- +goose Up
CREATE TABLE recycled_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id BIGINT REFERENCES series(id) ON DELETE SET NULL,
    chapter_id BIGINT REFERENCES chapters(id) ON DELETE SET NULL,
    chapter_file_id BIGINT,
    kind TEXT NOT NULL,
    reason TEXT NOT NULL,
    job_id BIGINT,
    job_kind TEXT NOT NULL DEFAULT '',
    profile_name TEXT NOT NULL DEFAULT '',
    process_params TEXT NOT NULL DEFAULT '',
    series_title TEXT NOT NULL DEFAULT '',
    original_relative_path TEXT NOT NULL,
    recycled_path TEXT NOT NULL UNIQUE,
    size BIGINT NOT NULL DEFAULT 0,
    page_count INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    source_name TEXT NOT NULL DEFAULT '',
    scanlator TEXT NOT NULL DEFAULT '',
    file_snapshot TEXT,
    folder_snapshot TEXT,
    recycled_at DATETIME NOT NULL
);
CREATE INDEX recycled_files_series ON recycled_files(series_id, recycled_at);
CREATE INDEX recycled_files_chapter ON recycled_files(chapter_id, recycled_at);
CREATE INDEX recycled_files_retention ON recycled_files(recycled_at);
ALTER TABLE download_jobs ADD COLUMN recycled_file_id BIGINT;
ALTER TABLE download_jobs ADD COLUMN config_override TEXT;
ALTER TABLE download_jobs ADD COLUMN force_download BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE download_jobs ADD COLUMN profile_name TEXT NOT NULL DEFAULT '';
ALTER TABLE download_jobs ADD COLUMN pin_release BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE chapter_files ADD COLUMN source_pages TEXT;

CREATE INDEX download_jobs_recycled ON download_jobs(recycled_file_id);

-- +goose Down
DROP INDEX download_jobs_recycled;
ALTER TABLE download_jobs DROP COLUMN profile_name;
ALTER TABLE chapter_files DROP COLUMN source_pages;
ALTER TABLE download_jobs DROP COLUMN pin_release;
ALTER TABLE download_jobs DROP COLUMN force_download;
ALTER TABLE download_jobs DROP COLUMN config_override;
ALTER TABLE download_jobs DROP COLUMN recycled_file_id;
DROP TABLE recycled_files;
