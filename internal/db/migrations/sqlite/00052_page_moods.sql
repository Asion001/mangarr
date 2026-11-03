-- +goose Up
-- Moods found in pages: each mood's prompts describe how its pages look,
-- page_moods keeps a chapter file's scores (by the file's hash, the model
-- and the prompts they were made with), and a worker task may belong to no
-- download job (scoring a chapter's pages isn't part of one), so job_id
-- may be empty: SQLite needs the table rebuilt for that.
ALTER TABLE audio_moods ADD COLUMN prompts TEXT NOT NULL DEFAULT '[]';
UPDATE audio_moods SET prompts = '["a peaceful manga page of a quiet everyday moment", "a calm manga panel of people relaxing at home", "a gentle slice of life manga scene in the sunshine"]' WHERE slug = 'calm';
UPDATE audio_moods SET prompts = '["a sad manga page with someone crying", "a lonely melancholic manga panel in the rain", "a manga page of a tearful farewell"]' WHERE slug = 'melancholy';
UPDATE audio_moods SET prompts = '["a tense manga page of a dangerous standoff", "a suspenseful manga panel with a worried face and sweat", "a manga page of someone being chased"]' WHERE slug = 'tense';
UPDATE audio_moods SET prompts = '["a manga page of an intense fight with speed lines", "an action manga panel with an explosion", "a manga page of a sword battle"]' WHERE slug = 'action';
UPDATE audio_moods SET prompts = '["a scary horror manga page with a monster in the dark", "a creepy manga panel with a terrified face", "a gory horror manga scene"]' WHERE slug = 'horror';
UPDATE audio_moods SET prompts = '["a romantic manga page of a blushing couple", "a manga panel of a love confession with sparkles", "a manga page of two people about to kiss"]' WHERE slug = 'romance';
UPDATE audio_moods SET prompts = '["a funny manga page with exaggerated comic expressions", "a chibi comedy manga panel", "a silly gag manga scene with people laughing"]' WHERE slug = 'comedy';
UPDATE audio_moods SET prompts = '["a mysterious manga page with dark shadows", "a manga panel of a detective looking for clues", "an eerie mysterious manga scene at night"]' WHERE slug = 'mystery';

CREATE TABLE page_moods (
    file_key TEXT NOT NULL,
    model_version TEXT NOT NULL,
    prompts_key TEXT NOT NULL,
    pages INTEGER NOT NULL DEFAULT 0,
    scores TEXT NOT NULL DEFAULT '[]',
    scanned_at TIMESTAMP NOT NULL,
    PRIMARY KEY (file_key, model_version)
);

CREATE TABLE worker_tasks_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id INTEGER REFERENCES download_jobs (id) ON DELETE CASCADE,
    worker_id INTEGER REFERENCES workers (id) ON DELETE SET NULL,
    kind TEXT NOT NULL,
    seq INTEGER NOT NULL DEFAULT 0,
    state TEXT NOT NULL,
    spec TEXT NOT NULL DEFAULT '{}',
    not_before TIMESTAMP NOT NULL,
    lease_until TIMESTAMP,
    heartbeat_at TIMESTAMP,
    cancel INTEGER NOT NULL DEFAULT 0,
    attempt INTEGER NOT NULL DEFAULT 0,
    pages_total INTEGER NOT NULL DEFAULT 0,
    pages_done INTEGER NOT NULL DEFAULT 0,
    bytes_in INTEGER NOT NULL DEFAULT 0,
    bytes_out INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    started_at TIMESTAMP,
    finished_at TIMESTAMP
);
INSERT INTO worker_tasks_new SELECT id, job_id, worker_id, kind, seq, state, spec, not_before, lease_until, heartbeat_at, cancel, attempt,
    pages_total, pages_done, bytes_in, bytes_out, error, created_at, started_at, finished_at FROM worker_tasks;
DROP TABLE worker_tasks;
ALTER TABLE worker_tasks_new RENAME TO worker_tasks;
CREATE INDEX worker_tasks_waiting ON worker_tasks (state, kind, not_before);
CREATE INDEX worker_tasks_leases ON worker_tasks (state, lease_until);
CREATE INDEX worker_tasks_job ON worker_tasks (job_id);
CREATE INDEX worker_tasks_worker ON worker_tasks (worker_id, state);

-- +goose Down
DELETE FROM worker_tasks WHERE job_id IS NULL;
DROP TABLE page_moods;
ALTER TABLE audio_moods DROP COLUMN prompts;
