-- +goose Up
-- Reading progress belongs to the account: every table keyed by a reader is
-- keyed by its user instead, and readers go. A reader no account used
-- becomes a disabled account without a password, so its progress stays.
-- (SQLite runs the same steps in Go: see internal/db/progress_per_user.go.)
ALTER TABLE users ADD COLUMN count_for_cleanup boolean DEFAULT false NOT NULL;
UPDATE users SET count_for_cleanup = r.count_for_cleanup FROM readers AS r WHERE r.id = users.reader_id;

INSERT INTO users (username, password_hash, created_at, group_id, reader_id, display_name, disabled, count_for_cleanup)
SELECT CASE WHEN EXISTS (SELECT 1 FROM users AS u WHERE LOWER(u.username) = 'reader-' || r.id) THEN 'former-reader-' || r.id ELSE 'reader-' || r.id END,
       '', r.created_at, (SELECT g.id FROM groups AS g WHERE g.builtin = 'users'), r.id, r.name, TRUE, r.count_for_cleanup
FROM readers AS r WHERE NOT EXISTS (SELECT 1 FROM users AS u WHERE u.reader_id = r.id) ORDER BY r.id;

DROP VIEW chapter_read_states;

ALTER TABLE title_read_states ADD COLUMN user_id bigint;
UPDATE title_read_states SET user_id = u.id FROM users AS u WHERE u.reader_id = title_read_states.reader_id;
DELETE FROM title_read_states WHERE user_id IS NULL;
ALTER TABLE title_read_states ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE title_read_states DROP COLUMN reader_id;
CREATE UNIQUE INDEX title_read_states_identity ON title_read_states USING btree (user_id, title_id, number_key);
ALTER TABLE title_read_states ADD CONSTRAINT title_read_states_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

CREATE VIEW chapter_read_states AS
SELECT t.id, t.user_id, c.id AS chapter_id, c.series_id, t.completed, t.page, t.read_at, t.synced_at, t.origin,
       t.chapter_id AS source_chapter_id
FROM title_read_states AS t
JOIN series AS s ON COALESCE(s.work_id, -s.id) = t.title_id
JOIN chapters AS c ON c.series_id = s.id AND c.number_key = t.number_key;

ALTER TABLE read_events ADD COLUMN user_id bigint;
UPDATE read_events SET user_id = u.id FROM users AS u WHERE u.reader_id = read_events.reader_id;
DELETE FROM read_events WHERE user_id IS NULL;
ALTER TABLE read_events ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE read_events DROP COLUMN reader_id;
CREATE INDEX read_events_user ON read_events USING btree (user_id, at);
ALTER TABLE read_events ADD CONSTRAINT read_events_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE reading_sessions ADD COLUMN user_id bigint;
UPDATE reading_sessions SET user_id = u.id FROM users AS u WHERE u.reader_id = reading_sessions.reader_id;
DELETE FROM reading_sessions WHERE user_id IS NULL;
ALTER TABLE reading_sessions ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE reading_sessions DROP COLUMN reader_id;
CREATE INDEX reading_sessions_user_updated ON reading_sessions USING btree (user_id, updated_at);
ALTER TABLE reading_sessions ADD CONSTRAINT reading_sessions_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE koreader_documents ADD COLUMN user_id bigint;
UPDATE koreader_documents SET user_id = u.id FROM users AS u WHERE u.reader_id = koreader_documents.reader_id;
DELETE FROM koreader_documents WHERE user_id IS NULL;
ALTER TABLE koreader_documents ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE koreader_documents DROP COLUMN reader_id;
ALTER TABLE koreader_documents ADD CONSTRAINT koreader_documents_pkey PRIMARY KEY (user_id, document);
ALTER TABLE koreader_documents ADD CONSTRAINT koreader_documents_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE reader_accounts RENAME TO library_accounts;
ALTER SEQUENCE reader_accounts_id_seq RENAME TO library_accounts_id_seq;
ALTER TABLE library_accounts RENAME CONSTRAINT reader_accounts_pkey TO library_accounts_pkey;
ALTER TABLE library_accounts ADD COLUMN user_id bigint;
UPDATE library_accounts SET user_id = u.id FROM users AS u WHERE u.reader_id = library_accounts.reader_id;
DELETE FROM library_accounts WHERE user_id IS NULL;
ALTER TABLE library_accounts ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE library_accounts DROP COLUMN reader_id;
CREATE UNIQUE INDEX library_accounts_identity ON library_accounts USING btree (user_id, module_id);
ALTER TABLE library_accounts ADD CONSTRAINT library_accounts_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE requested_progress DROP COLUMN reader_id;

-- cleanup's required readers become the users they belonged to; the
-- reader reading apps used is now always the first admin
UPDATE settings SET value = ((value::jsonb - 'readerIds') || jsonb_build_object('userIds', COALESCE((
    SELECT jsonb_agg(u.id ORDER BY u.id) FROM users AS u
    WHERE u.reader_id IN (SELECT r.value::bigint FROM jsonb_array_elements_text(COALESCE(settings.value::jsonb -> 'readerIds', '[]'::jsonb)) AS r)), '[]'::jsonb)))::text
WHERE key = 'cleanup';
UPDATE settings SET value = (value::jsonb - 'readerId')::text WHERE key = 'reading';

ALTER TABLE users DROP COLUMN reader_id;
DROP TABLE readers;
