// Package readstate writes reading progress. Progress belongs to a title,
// so a state written for a chapter is the state of that chapter number in
// every language edition of the title (model.ChapterReadState reads it back
// per chapter).
package readstate

import (
	"context"
	"database/sql"
	"errors"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// TitleKey is the SQL expression for a series' title id (series aliased s).
const TitleKey = "COALESCE(s.work_id, -s.id)"

// ErrNoChapter is returned when the state's chapter doesn't exist.
var ErrNoChapter = errors.New("chapter not found")

// Save stores st as the progress of its chapter's number in the title,
// replacing what the title had for it, and sets st.ID.
func Save(ctx context.Context, db bun.IDB, st *model.ChapterReadState) error {
	err := db.NewRaw(`INSERT INTO title_read_states (reader_id, title_id, number_key, chapter_id, completed, page, read_at, synced_at, origin)
SELECT ?, `+TitleKey+`, c.number_key, c.id, ?, ?, ?, ?, ?
FROM chapters AS c JOIN series AS s ON s.id = c.series_id WHERE c.id = ?
ON CONFLICT (reader_id, title_id, number_key) DO UPDATE SET chapter_id = EXCLUDED.chapter_id, completed = EXCLUDED.completed,
  page = EXCLUDED.page, read_at = EXCLUDED.read_at, synced_at = EXCLUDED.synced_at, origin = EXCLUDED.origin
RETURNING id`, st.ReaderID, st.Completed, st.Page, st.ReadAt, st.SyncedAt, st.Origin, st.ChapterID).Scan(ctx, &st.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNoChapter
	}
	if err == nil {
		st.SourceChapterID = st.ChapterID
	}
	return err
}

// Delete removes states (marks their chapters unread in every edition).
func Delete(ctx context.Context, db bun.IDB, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := db.NewDelete().Model((*model.TitleReadState)(nil)).Where("id IN (?)", bun.In(ids)).Exec(ctx)
	return err
}

// SetPage changes only a state's page.
func SetPage(ctx context.Context, db bun.IDB, id int64, page int) error {
	_, err := db.NewUpdate().Model((*model.TitleReadState)(nil)).Set("page = ?", page).Where("id = ?", id).Exec(ctx)
	return err
}

// Retitle carries the progress of a series' chapters from the title it
// left to the one it joined, keeping the furthest where both have some.
func Retitle(ctx context.Context, db bun.IDB, seriesID, from, to int64) error {
	if from == to {
		return nil
	}
	_, err := db.NewRaw(`INSERT INTO title_read_states (reader_id, title_id, number_key, chapter_id, completed, page, read_at, synced_at, origin)
SELECT t.reader_id, ?, t.number_key, t.chapter_id, t.completed, t.page, t.read_at, t.synced_at, t.origin
FROM title_read_states AS t
WHERE t.title_id = ? AND t.number_key IN (SELECT number_key FROM chapters WHERE series_id = ?)
ON CONFLICT (reader_id, title_id, number_key) DO UPDATE SET chapter_id = EXCLUDED.chapter_id, completed = EXCLUDED.completed,
  page = EXCLUDED.page, read_at = EXCLUDED.read_at, synced_at = EXCLUDED.synced_at, origin = EXCLUDED.origin
WHERE EXCLUDED.completed AND NOT title_read_states.completed`, to, from, seriesID).Exec(ctx)
	return err
}

// Purge deletes the progress of titles that have no series left.
func Purge(ctx context.Context, db bun.IDB) error {
	_, err := db.NewRaw(`DELETE FROM title_read_states WHERE title_id NOT IN (SELECT COALESCE(work_id, -id) FROM series)`).Exec(ctx)
	return err
}

// TitleID is a series' title id: its work, or minus its own id without one.
func TitleID(ser *model.Series) int64 {
	if ser.WorkID > 0 {
		return ser.WorkID
	}
	return -ser.ID
}
