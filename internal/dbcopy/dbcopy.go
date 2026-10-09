// Package dbcopy copies mangarr's data between databases (SQLite and
// PostgreSQL, either way): moving to Postgres, moving back, and backups of
// a Postgres install as a portable SQLite file.
package dbcopy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/db"
)

// Tables are mangarr's tables in an order that satisfies foreign keys
// (TestTablesCoverSchema keeps it complete).
var Tables = []string{
	"groups", "readers", "users", "user_ui_preferences", "source_priority_lists", "sessions", "invites",
	"messenger_links", "messenger_link_tokens",
	"settings", "tags", "root_folders", "profiles", "provider_definitions", "catalog_prefs",
	"works", "series", "series_sources", "chapters", "chapter_releases", "chapter_files", "page_bounds",
	"notification_deliveries", "notification_dispatches",
	"recycled_files", "download_queue_order", "download_jobs", "history", "blocklist", "commands", "scheduled_tasks",
	"reader_accounts", "title_read_states", "koreader_documents",
	"imports", "import_entries", "reading_keys", "read_events", "reading_sessions", "reader_prefs",
	"requests", "request_users", "follows",
	"workers", "worker_tasks",
}

// ErrNotEmpty is returned when the target already has data and overwrite
// wasn't asked for.
var ErrNotEmpty = errors.New("the target database already has mangarr data")

// batch is how many rows go into one INSERT.
const batch = 500

// Progress reports rows copied so far for a table.
type Progress func(table string, done, total int)

// Result counts copied rows per table.
type Result struct {
	Rows     map[string]int `json:"rows"`
	Total    int            `json:"total"`
	Duration time.Duration  `json:"duration"`
}

// Copy migrates dst to the current schema and copies every table from src.
// dst must be empty unless overwrite is set, which empties it first. Both
// databases must be on the same schema version (src is migrated by the app).
func Copy(ctx context.Context, src, dst *db.DB, overwrite bool, progress Progress) (*Result, error) {
	start := time.Now()
	if err := dst.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("prepare target: %w", err)
	}
	n, err := Rows(ctx, dst)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		if !overwrite {
			return nil, fmt.Errorf("%w (%d rows)", ErrNotEmpty, n)
		}
		if err := empty(ctx, dst); err != nil {
			return nil, fmt.Errorf("empty target: %w", err)
		}
	}
	// Migration 27 seeds this singleton even in an otherwise empty database.
	if _, err := dst.ExecContext(ctx, "DELETE FROM download_queue_order"); err != nil {
		return nil, err
	}
	// read Postgres in one snapshot, so rows added meanwhile can't break
	// foreign keys in the copy
	var from reader = src
	if src.Kind == db.Postgres {
		tx, err := src.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Rollback() }()
		from = tx
	}
	res := &Result{Rows: map[string]int{}}
	for _, t := range Tables {
		rows, err := copyTable(ctx, from, dst, t, progress)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", t, err)
		}
		res.Rows[t], res.Total = rows, res.Total+rows
	}
	if dst.Kind == db.Postgres {
		if err := resetSequences(ctx, dst); err != nil {
			return nil, err
		}
	}
	for _, t := range Tables {
		var got int
		if err := dst.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(t)).Scan(&got); err != nil {
			return nil, err
		}
		if got != res.Rows[t] {
			return nil, fmt.Errorf("%s: copied %d rows but the target has %d", t, res.Rows[t], got)
		}
	}
	res.Duration = time.Since(start)
	return res, nil
}

// Rows counts user data, excluding the migration-seeded queue singleton
// (0 = an empty mangarr database).
func Rows(ctx context.Context, d *db.DB) (int, error) {
	counts, err := CountRows(ctx, d)
	if err != nil {
		return 0, err
	}
	return counts.Total - counts.Rows["download_queue_order"], nil
}

// CountRows counts every required mangarr table and returns its row summary.
func CountRows(ctx context.Context, d *db.DB) (*Result, error) {
	res := &Result{Rows: map[string]int{}}
	total := 0
	for _, t := range Tables {
		var n int
		if err := d.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(t)).Scan(&n); err != nil {
			return nil, err
		}
		res.Rows[t] = n
		total += n
	}
	res.Total = total
	return res, nil
}

func empty(ctx context.Context, d *db.DB) error {
	if d.Kind == db.Postgres {
		quoted := make([]string, len(Tables))
		for i, t := range Tables {
			quoted[i] = quote(t)
		}
		_, err := d.ExecContext(ctx, "TRUNCATE "+strings.Join(quoted, ", ")+" RESTART IDENTITY CASCADE")
		return err
	}
	for i := len(Tables) - 1; i >= 0; i-- {
		if _, err := d.ExecContext(ctx, "DELETE FROM "+quote(Tables[i])); err != nil {
			return err
		}
	}
	_, _ = d.ExecContext(ctx, "DELETE FROM sqlite_sequence")
	return nil
}

func quote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }

// kind is how a target column wants its values.
type kind int

const (
	kindOther kind = iota
	kindBool
	kindTime
	kindJSON
	kindText
)

// columns returns a table's columns in the target with their kinds.
func columns(ctx context.Context, d *db.DB, table string) (map[string]kind, error) {
	out := map[string]kind{}
	var rows *sql.Rows
	var err error
	if d.Kind == db.Postgres {
		rows, err = d.QueryContext(ctx, `SELECT column_name, data_type FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ?`, table)
	} else {
		rows, err = d.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return nil, err
		}
		typ = strings.ToLower(typ)
		switch {
		case strings.Contains(typ, "bool"):
			out[name] = kindBool
		case strings.Contains(typ, "timestamp"), strings.Contains(typ, "date"):
			out[name] = kindTime
		case strings.Contains(typ, "json"):
			out[name] = kindJSON
		case strings.Contains(typ, "text"), strings.Contains(typ, "char"):
			out[name] = kindText
		default:
			out[name] = kindOther
		}
	}
	return out, rows.Err()
}

// reader reads the source (the database, or a snapshot transaction).
type reader interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func copyTable(ctx context.Context, src reader, dst *db.DB, table string, progress Progress) (int, error) {
	want, err := columns(ctx, dst, table)
	if err != nil {
		return 0, err
	}
	var total int
	if err := src.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(table)).Scan(&total); err != nil {
		return 0, err
	}
	if progress != nil {
		progress(table, 0, total)
	}
	if total == 0 {
		return 0, nil
	}
	rows, err := src.QueryContext(ctx, "SELECT * FROM "+quote(table))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}
	for _, c := range cols {
		if _, ok := want[c]; !ok {
			return 0, fmt.Errorf("column %s is missing in the target (different mangarr versions?)", c)
		}
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = quote(c)
	}
	head := "INSERT INTO " + quote(table) + " (" + strings.Join(quoted, ", ") + ") VALUES "
	rowTmpl := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")"

	tx, err := dst.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var pending [][]any
	done := 0
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		tmpls := make([]string, len(pending))
		args := make([]any, 0, len(pending)*len(cols))
		for i, r := range pending {
			tmpls[i] = rowTmpl
			args = append(args, r...)
		}
		// bun formats the values for the target's dialect (times, bools)
		if _, err := tx.NewRaw(head+strings.Join(tmpls, ", "), args...).Exec(ctx); err != nil {
			return err
		}
		done += len(pending)
		pending = pending[:0]
		if progress != nil {
			progress(table, done, total)
		}
		return nil
	}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return done, err
		}
		for i, c := range cols {
			v, err := convert(vals[i], want[c])
			if err != nil {
				return done, fmt.Errorf("column %s: %w", c, err)
			}
			vals[i] = v
		}
		pending = append(pending, vals)
		if len(pending) >= batch {
			if err := flush(); err != nil {
				return done, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return done, err
	}
	if err := flush(); err != nil {
		return done, err
	}
	return done, tx.Commit()
}

// timeLayouts are the forms timestamps take as text (bun's SQLite format first).
var timeLayouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02",
}

// convert turns a value read from the source into what the target column
// takes: SQLite keeps booleans as integers and times and JSON as text.
func convert(v any, k kind) (any, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok && k != kindOther {
		v = string(b)
	}
	switch k {
	case kindBool:
		switch x := v.(type) {
		case bool:
			return x, nil
		case int64:
			return x != 0, nil
		case float64:
			return x != 0, nil
		case string:
			return x == "1" || strings.EqualFold(x, "true") || strings.EqualFold(x, "t"), nil
		}
	case kindTime:
		switch x := v.(type) {
		case time.Time:
			return x.UTC(), nil
		case string:
			for _, l := range timeLayouts {
				if t, err := time.Parse(l, x); err == nil {
					return t.UTC(), nil
				}
			}
			return nil, fmt.Errorf("unreadable time %q", x)
		case int64:
			return time.Unix(x, 0).UTC(), nil
		}
	case kindJSON, kindText:
		switch x := v.(type) {
		case string:
			return x, nil
		case bool, int64, float64:
			return fmt.Sprint(x), nil
		case time.Time:
			return x.UTC().Format(time.RFC3339Nano), nil
		}
	default:
		if b, ok := v.(bool); ok { // a bool into an integer column
			if b {
				return int64(1), nil
			}
			return int64(0), nil
		}
		return v, nil
	}
	return v, nil
}

// resetSequences moves Postgres identity sequences past the copied ids.
func resetSequences(ctx context.Context, d *db.DB) error {
	for _, t := range Tables {
		var has bool
		if err := d.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
			WHERE table_schema = current_schema() AND table_name = ? AND column_name = 'id' AND data_type IN ('bigint', 'integer'))`, t).Scan(&has); err != nil {
			return err
		}
		if !has {
			continue
		}
		q := fmt.Sprintf(`SELECT setval(pg_get_serial_sequence('%s', 'id'), COALESCE((SELECT MAX(id) FROM %s), 0) + 1, false)
			WHERE pg_get_serial_sequence('%s', 'id') IS NOT NULL`, t, quote(t), t)
		if _, err := d.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("%s sequence: %w", t, err)
		}
	}
	return nil
}
