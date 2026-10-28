// Package readinglists keeps a user's reading lists: one status per title
// (reading, planned, completed, on hold, dropped) and lists of titles, the
// built-in Favorites and ones the user made. Like reading progress they
// belong to the title, so every language edition of it shares them.
package readinglists

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// TitleKey is the SQL expression for a series' title id (series aliased s).
const TitleKey = "COALESCE(s.work_id, -s.id)"

var (
	ErrNotFound = errors.New("list not found")
	ErrBuiltIn  = errors.New("the Favorites list can't be renamed or deleted")
	ErrName     = errors.New("give the list a name")
)

// ValidStatus reports whether s is a status ("" is none).
func ValidStatus(s string) bool { return s == "" || slices.Contains(model.ReadingStatuses, s) }

// TitleLists is where a user put one title.
type TitleLists struct {
	Status string  `json:"status" enum:"reading,planned,completed,onhold,dropped," doc:"Empty when the title has none"`
	Lists  []int64 `json:"lists" doc:"Ids of the lists the title is in"`
}

// Lists returns the user's lists, Favorites first and the rest by name. It
// makes Favorites on first use.
func Lists(ctx context.Context, db bun.IDB, userID int64) ([]model.ReadingList, error) {
	if _, err := Favorites(ctx, db, userID); err != nil {
		return nil, err
	}
	var out []model.ReadingList
	if err := db.NewSelect().Model(&out).Where("user_id = ?", userID).Scan(ctx); err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b model.ReadingList) int {
		if (a.Kind == model.ListFavorites) != (b.Kind == model.ListFavorites) {
			if a.Kind == model.ListFavorites {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out, nil
}

// Favorites returns the user's Favorites list, making it if needed.
func Favorites(ctx context.Context, db bun.IDB, userID int64) (*model.ReadingList, error) {
	var l model.ReadingList
	err := db.NewSelect().Model(&l).Where("user_id = ? AND kind = ?", userID, model.ListFavorites).Order("id").Limit(1).Scan(ctx)
	if err == nil {
		return &l, nil
	}
	l = model.ReadingList{UserID: userID, Name: "Favorites", Kind: model.ListFavorites, CreatedAt: time.Now().UTC()}
	if _, err := db.NewInsert().Model(&l).Exec(ctx); err != nil {
		return nil, err
	}
	return &l, nil
}

// Create makes a list, or returns the user's list of that name.
func Create(ctx context.Context, db bun.IDB, userID int64, name string) (*model.ReadingList, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrName
	}
	lists, err := Lists(ctx, db, userID)
	if err != nil {
		return nil, err
	}
	for i := range lists {
		if strings.EqualFold(lists[i].Name, name) {
			return &lists[i], nil
		}
	}
	l := &model.ReadingList{UserID: userID, Name: name, Kind: model.ListOwn, CreatedAt: time.Now().UTC()}
	if _, err := db.NewInsert().Model(l).Exec(ctx); err != nil {
		return nil, err
	}
	return l, nil
}

// own returns a list the user made.
func own(ctx context.Context, db bun.IDB, userID, listID int64) (*model.ReadingList, error) {
	var l model.ReadingList
	if err := db.NewSelect().Model(&l).Where("id = ? AND user_id = ?", listID, userID).Scan(ctx); err != nil {
		return nil, ErrNotFound
	}
	if l.Kind == model.ListFavorites {
		return nil, ErrBuiltIn
	}
	return &l, nil
}

// Rename renames a list the user made.
func Rename(ctx context.Context, db bun.IDB, userID, listID int64, name string) (*model.ReadingList, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrName
	}
	l, err := own(ctx, db, userID, listID)
	if err != nil {
		return nil, err
	}
	l.Name = name
	_, err = db.NewUpdate().Model(l).Column("name").WherePK().Exec(ctx)
	return l, err
}

// Delete deletes a list the user made (the titles stay in the library).
func Delete(ctx context.Context, db bun.IDB, userID, listID int64) error {
	l, err := own(ctx, db, userID, listID)
	if err != nil {
		return err
	}
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*model.ReadingListTitle)(nil)).Where("list_id = ?", l.ID).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model(l).WherePK().Exec(ctx)
		return err
	})
}

// Get returns where the user put a title.
func Get(ctx context.Context, db bun.IDB, userID, titleID int64) (TitleLists, error) {
	e := TitleLists{Lists: []int64{}}
	var st model.TitleStatus
	if err := db.NewSelect().Model(&st).Where("user_id = ? AND title_id = ?", userID, titleID).Scan(ctx); err == nil {
		e.Status = st.Status
	}
	err := db.NewSelect().TableExpr("reading_list_titles AS t").Column("t.list_id").
		Join("JOIN reading_lists AS l ON l.id = t.list_id").Where("l.user_id = ? AND t.title_id = ?", userID, titleID).
		Order("t.list_id").Scan(ctx, &e.Lists)
	return e, err
}

// Set puts a title where e says: its status (none when empty) and exactly
// the lists in e.Lists, which must be the user's.
func Set(ctx context.Context, db bun.IDB, userID, titleID int64, e TitleLists) error {
	if !ValidStatus(e.Status) {
		return errors.New("unknown status " + strconv.Quote(e.Status))
	}
	lists, err := Lists(ctx, db, userID)
	if err != nil {
		return err
	}
	mine := map[int64]bool{}
	ids := make([]int64, 0, len(lists))
	for _, l := range lists {
		mine[l.ID] = true
		ids = append(ids, l.ID)
	}
	for _, id := range e.Lists {
		if !mine[id] {
			return ErrNotFound
		}
	}
	now := time.Now().UTC()
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if err := setStatus(ctx, tx, userID, titleID, e.Status, now); err != nil {
			return err
		}
		q := tx.NewDelete().Model((*model.ReadingListTitle)(nil)).Where("title_id = ? AND list_id IN (?)", titleID, bun.In(ids))
		if len(e.Lists) > 0 {
			q = q.Where("list_id NOT IN (?)", bun.In(e.Lists))
		}
		if _, err := q.Exec(ctx); err != nil {
			return err
		}
		for _, id := range e.Lists {
			if err := add(ctx, tx, id, titleID, now); err != nil {
				return err
			}
		}
		return nil
	})
}

// Bulk is a change to many titles at once: a status for all of them ("" is
// none), or one list they're added to or taken out of.
type Bulk struct {
	Action string `json:"action" enum:"status,add,remove"`
	Status string `json:"status,omitempty" enum:"reading,planned,completed,onhold,dropped," doc:"For status; empty clears it"`
	ListID int64  `json:"listId,omitempty" doc:"For add and remove: one of your lists, Favorites too"`
}

// ApplyBulk makes the change b to the titles, leaving the rest of where each
// title is as it was.
func ApplyBulk(ctx context.Context, db bun.IDB, userID int64, titleIDs []int64, b Bulk) error {
	switch b.Action {
	case "status":
		if !ValidStatus(b.Status) {
			return errors.New("unknown status " + strconv.Quote(b.Status))
		}
	case "add", "remove":
		n, err := db.NewSelect().Model((*model.ReadingList)(nil)).Where("id = ? AND user_id = ?", b.ListID, userID).Count(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	default:
		return errors.New("unknown action " + strconv.Quote(b.Action))
	}
	if len(titleIDs) == 0 {
		return nil
	}
	now := time.Now().UTC()
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if b.Action == "remove" {
			_, err := tx.NewDelete().Model((*model.ReadingListTitle)(nil)).Where("list_id = ? AND title_id IN (?)", b.ListID, bun.In(titleIDs)).Exec(ctx)
			return err
		}
		for _, id := range titleIDs {
			var err error
			if b.Action == "status" {
				err = setStatus(ctx, tx, userID, id, b.Status, now)
			} else {
				err = add(ctx, tx, b.ListID, id, now)
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func setStatus(ctx context.Context, db bun.IDB, userID, titleID int64, status string, now time.Time) error {
	if status == "" {
		_, err := db.NewDelete().Model((*model.TitleStatus)(nil)).Where("user_id = ? AND title_id = ?", userID, titleID).Exec(ctx)
		return err
	}
	_, err := db.NewInsert().Model(&model.TitleStatus{UserID: userID, TitleID: titleID, Status: status, UpdatedAt: now}).
		On("CONFLICT (user_id, title_id) DO UPDATE").Set("status = EXCLUDED.status").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return err
}

func add(ctx context.Context, db bun.IDB, listID, titleID int64, now time.Time) error {
	_, err := db.NewInsert().Model(&model.ReadingListTitle{ListID: listID, TitleID: titleID, AddedAt: now}).On("CONFLICT DO NOTHING").Exec(ctx)
	return err
}

// Imported is what a file or a site says about a title: its status, whether
// it's a favorite and the other lists it's in, by name.
type Imported struct {
	Status   string
	Favorite bool
	Lists    []string
}

// Import adds what another app or site says to the user's lists: a status
// when the title has none here yet, Favorites, and lists by name (made when
// missing). It reports whether anything changed.
func Import(ctx context.Context, db bun.IDB, userID, titleID int64, in Imported) (bool, error) {
	now := time.Now().UTC()
	changed := false
	if in.Status != "" && ValidStatus(in.Status) {
		r, err := db.NewInsert().Model(&model.TitleStatus{UserID: userID, TitleID: titleID, Status: in.Status, UpdatedAt: now}).
			On("CONFLICT DO NOTHING").Exec(ctx)
		if err != nil {
			return false, err
		}
		n, _ := r.RowsAffected()
		changed = n > 0
	}
	var ids []int64
	if in.Favorite {
		f, err := Favorites(ctx, db, userID)
		if err != nil {
			return changed, err
		}
		ids = append(ids, f.ID)
	}
	for _, name := range in.Lists {
		if strings.TrimSpace(name) == "" {
			continue
		}
		l, err := Create(ctx, db, userID, name)
		if err != nil {
			return changed, err
		}
		ids = append(ids, l.ID)
	}
	for _, id := range ids {
		r, err := db.NewInsert().Model(&model.ReadingListTitle{ListID: id, TitleID: titleID, AddedAt: now}).On("CONFLICT DO NOTHING").Exec(ctx)
		if err != nil {
			return changed, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			changed = true
		}
	}
	return changed, nil
}

// Counts returns how many of the user's titles on this server each status
// and list holds (only ones with titles).
func Counts(ctx context.Context, db bun.IDB, userID int64) (map[string]int, error) {
	out := map[string]int{}
	var statuses []struct {
		Status string `bun:"status"`
		N      int    `bun:"n"`
	}
	if err := db.NewRaw(`SELECT status, COUNT(*) AS n FROM title_statuses WHERE user_id = ? AND title_id IN (SELECT `+TitleKey+` FROM series AS s WHERE s.preview = ?) GROUP BY status`,
		userID, false).Scan(ctx, &statuses); err != nil {
		return nil, err
	}
	for _, s := range statuses {
		out[s.Status] = s.N
	}
	var lists []struct {
		ListID int64 `bun:"list_id"`
		N      int   `bun:"n"`
	}
	if err := db.NewRaw(`SELECT t.list_id, COUNT(*) AS n FROM reading_list_titles AS t JOIN reading_lists AS l ON l.id = t.list_id
WHERE l.user_id = ? AND t.title_id IN (SELECT `+TitleKey+` FROM series AS s WHERE s.preview = ?) GROUP BY t.list_id`, userID, false).Scan(ctx, &lists); err != nil {
		return nil, err
	}
	for _, l := range lists {
		out[strconv.FormatInt(l.ListID, 10)] = l.N
	}
	return out, nil
}

// Titles returns the ids of the titles a status or list (by id) of the user
// holds; ok is false when key is neither.
func Titles(ctx context.Context, db bun.IDB, userID int64, key string) (map[int64]bool, bool, error) {
	var ids []int64
	if slices.Contains(model.ReadingStatuses, key) {
		if err := db.NewSelect().Model((*model.TitleStatus)(nil)).Column("title_id").Where("user_id = ? AND status = ?", userID, key).Scan(ctx, &ids); err != nil {
			return nil, true, err
		}
	} else if id, err := strconv.ParseInt(key, 10, 64); err == nil && id > 0 {
		if err := db.NewSelect().TableExpr("reading_list_titles AS t").Column("t.title_id").
			Join("JOIN reading_lists AS l ON l.id = t.list_id").Where("l.user_id = ? AND l.id = ?", userID, id).Scan(ctx, &ids); err != nil {
			return nil, true, err
		}
	} else {
		return nil, false, nil
	}
	out := make(map[int64]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, true, nil
}

// Retitle carries the statuses and lists of the title a series left to the
// one it joined, keeping what the joined title already had.
func Retitle(ctx context.Context, db bun.IDB, from, to int64) error {
	if from == to {
		return nil
	}
	if _, err := db.NewRaw(`INSERT INTO title_statuses (user_id, title_id, status, updated_at)
SELECT user_id, ?, status, updated_at FROM title_statuses WHERE title_id = ? ON CONFLICT DO NOTHING`, to, from).Exec(ctx); err != nil {
		return err
	}
	_, err := db.NewRaw(`INSERT INTO reading_list_titles (list_id, title_id, added_at)
SELECT list_id, ?, added_at FROM reading_list_titles WHERE title_id = ? ON CONFLICT DO NOTHING`, to, from).Exec(ctx)
	return err
}

// Purge deletes the statuses and list entries of titles that have no series left.
func Purge(ctx context.Context, db bun.IDB) error {
	const gone = ` WHERE title_id NOT IN (SELECT COALESCE(work_id, -id) FROM series)`
	if _, err := db.NewRaw(`DELETE FROM title_statuses` + gone).Exec(ctx); err != nil {
		return err
	}
	_, err := db.NewRaw(`DELETE FROM reading_list_titles` + gone).Exec(ctx)
	return err
}
