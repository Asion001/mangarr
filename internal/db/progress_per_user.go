package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/pressly/goose/v3"
)

// sqliteGoMigrations are the SQLite migrations that can't be plain SQL.
func sqliteGoMigrations() []*goose.Migration {
	return []*goose.Migration{
		goose.NewGoMigration(44, &goose.GoFunc{RunDB: progressPerUser}, nil),
	}
}

// progressPerUser is migrations/postgres/00044_progress_per_user.sql for
// SQLite: progress is keyed by user instead of reader, and readers go. The
// tables are rebuilt (SQLite can't drop a column with a foreign key), and
// rebuilding users means turning foreign keys off, or dropping it would
// cascade into every table that refers to it. That needs one connection
// outside a transaction.
func progressPerUser(ctx context.Context, sqldb *sql.DB) error {
	conn, err := sqldb.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON") //nolint:errcheck
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, q := range progressPerUserSQL {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%w\n%s", err, q)
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	broken := rows.Next()
	if err := rows.Close(); err != nil {
		return err
	}
	if broken {
		return fmt.Errorf("moving progress to accounts left broken foreign keys")
	}
	return tx.Commit()
}

// byUser rebuilds a table keyed by reader_id with user_id (last, as
// PostgreSQL's ALTER TABLE puts it) instead.
func byUser(table, newTable, columns, definition string, indexes ...string) []string {
	out := []string{
		"CREATE TABLE " + newTable + "_new (" + definition + ")",
		"INSERT INTO " + newTable + "_new (" + columns + ", user_id) SELECT " + prefixed("t.", columns) + ", u.id FROM " + table +
			" AS t JOIN users AS u ON u.reader_id = t.reader_id",
		"DROP TABLE " + table,
		"ALTER TABLE " + newTable + "_new RENAME TO " + newTable,
	}
	return append(out, indexes...)
}

func prefixed(prefix, columns string) string {
	cols := strings.Split(columns, ", ")
	for i, c := range cols {
		cols[i] = prefix + c
	}
	return strings.Join(cols, ", ")
}

var progressPerUserSQL = func() []string {
	q := []string{
		`ALTER TABLE users ADD COLUMN count_for_cleanup BOOLEAN NOT NULL DEFAULT FALSE`,
		`UPDATE users SET count_for_cleanup = (SELECT r.count_for_cleanup FROM readers AS r WHERE r.id = users.reader_id) WHERE reader_id IS NOT NULL`,
		`INSERT INTO users (username, password_hash, created_at, group_id, reader_id, display_name, disabled, count_for_cleanup)
SELECT CASE WHEN EXISTS (SELECT 1 FROM users AS u WHERE LOWER(u.username) = 'reader-' || r.id) THEN 'former-reader-' || r.id ELSE 'reader-' || r.id END,
       '', r.created_at, (SELECT g.id FROM groups AS g WHERE g.builtin = 'users'), r.id, r.name, TRUE, r.count_for_cleanup
FROM readers AS r WHERE NOT EXISTS (SELECT 1 FROM users AS u WHERE u.reader_id = r.id) ORDER BY r.id`,
		`DROP VIEW chapter_read_states`,
	}
	q = append(q, byUser("title_read_states", "title_read_states", "id, title_id, number_key, chapter_id, completed, page, read_at, synced_at, origin", `
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title_id INTEGER NOT NULL,
    number_key TEXT NOT NULL,
    chapter_id INTEGER NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    page INTEGER NOT NULL DEFAULT 0,
    read_at TIMESTAMP,
    synced_at TIMESTAMP NOT NULL,
    origin TEXT NOT NULL DEFAULT '',
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE`,
		`CREATE UNIQUE INDEX title_read_states_identity ON title_read_states (user_id, title_id, number_key)`,
		`CREATE INDEX title_read_states_title ON title_read_states (title_id, number_key)`)...)
	q = append(q, `CREATE VIEW chapter_read_states AS
SELECT t.id, t.user_id, c.id AS chapter_id, c.series_id, t.completed, t.page, t.read_at, t.synced_at, t.origin,
       t.chapter_id AS source_chapter_id
FROM title_read_states AS t
JOIN series AS s ON COALESCE(s.work_id, -s.id) = t.title_id
JOIN chapters AS c ON c.series_id = s.id AND c.number_key = t.number_key`)
	q = append(q, byUser("read_events", "read_events", "id, series_id, chapter_id, completed, page, origin, client, device, outcome, at, chapters", `
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id INTEGER NOT NULL,
    chapter_id INTEGER NOT NULL,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    page INTEGER NOT NULL DEFAULT 0,
    origin TEXT NOT NULL,
    client TEXT NOT NULL DEFAULT '',
    device TEXT NOT NULL DEFAULT '',
    outcome TEXT NOT NULL DEFAULT '',
    at TIMESTAMP NOT NULL,
    chapters INTEGER NOT NULL DEFAULT 1,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE`,
		`CREATE INDEX read_events_user ON read_events (user_id, at)`)...)
	q = append(q, byUser("reading_sessions", "reading_sessions", "id, series_id, chapter_id, active_seconds, started_at, updated_at", `
    id TEXT PRIMARY KEY,
    series_id INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    active_seconds INTEGER NOT NULL DEFAULT 0 CHECK (active_seconds >= 0),
    started_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE`,
		`CREATE INDEX reading_sessions_user_updated ON reading_sessions (user_id, updated_at)`)...)
	q = append(q, byUser("koreader_documents", "koreader_documents", "document, chapter_id", `
    document TEXT NOT NULL,
    chapter_id INTEGER NOT NULL REFERENCES chapters (id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, document)`,
		`CREATE INDEX koreader_documents_chapter ON koreader_documents (chapter_id)`)...)
	q = append(q, byUser("reader_accounts", "library_accounts", "id, module_id, credentials, external_user, last_sync_at, last_error, created_at", `
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    module_id INTEGER NOT NULL,
    credentials TEXT NOT NULL DEFAULT '{}',
    external_user TEXT NOT NULL DEFAULT '',
    last_sync_at TIMESTAMP,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    user_id INTEGER NOT NULL REFERENCES users (id) ON DELETE CASCADE`,
		`CREATE UNIQUE INDEX library_accounts_identity ON library_accounts (user_id, module_id)`)...)
	return append(q,
		`ALTER TABLE requested_progress DROP COLUMN reader_id`,
		`UPDATE settings SET value = json_set(json_remove(value, '$.readerIds'), '$.userIds', json((
    SELECT json_group_array(u.id) FROM (SELECT u.id FROM users AS u
    WHERE u.reader_id IN (SELECT j.value FROM json_each(settings.value, '$.readerIds') AS j) ORDER BY u.id) AS u)))
WHERE key = 'cleanup'`,
		`UPDATE settings SET value = json_remove(value, '$.readerId') WHERE key = 'reading'`,
		`CREATE TABLE users_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    group_id INTEGER REFERENCES groups (id),
    display_name TEXT NOT NULL DEFAULT '',
    disabled BOOLEAN NOT NULL DEFAULT FALSE,
    last_login_at TIMESTAMP,
    oidc_subject TEXT NOT NULL DEFAULT '',
    created_by INTEGER,
    count_for_cleanup BOOLEAN NOT NULL DEFAULT FALSE
)`,
		`INSERT INTO users_new (id, username, password_hash, created_at, group_id, display_name, disabled, last_login_at, oidc_subject, created_by, count_for_cleanup)
SELECT id, username, password_hash, created_at, group_id, display_name, disabled, last_login_at, oidc_subject, created_by, count_for_cleanup FROM users`,
		`DROP TABLE users`,
		`ALTER TABLE users_new RENAME TO users`,
		`DROP TABLE readers`,
	)
}()
