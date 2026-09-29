package reading

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// Language editions of one title (series sharing a work) share read state
// by chapter number: a chapter finished in English is read in Russian too,
// so switching language continues where you left off. Page progress stays
// with its edition, since translations don't share page numbers, and
// chapters without a number (sort < 0) aren't matched.

// editionsBy announces mirrored changes. They come from mangarr, not from
// the server that reported the original, so that server hears about them too.
var editionsBy = By{Origin: model.EventOriginApp, Client: "Language editions"}

// editionChapter is a chapter with its title (work).
type editionChapter struct {
	ID         int64   `bun:"id"`
	SeriesID   int64   `bun:"series_id"`
	WorkID     int64   `bun:"work_id"`
	NumberSort float64 `bun:"number_sort"`
}

// mirrorEditions carries the chapters out finished or unread over to the
// same-numbered chapters of their titles' other editions, and returns the
// series it changed. Mirrored changes are announced, so apps and library
// servers hear about them, but not logged as read events: no device
// reported them. Failures are logged; the reported change already stands.
func (s *Service) mirrorEditions(ctx context.Context, readerID int64, out []Outcome) map[int64]bool {
	var reported []Change
	for _, o := range out {
		if o.Result == model.OutcomeApplied && o.Completed || o.Result == model.OutcomeUnread {
			reported = append(reported, o.Change)
		}
	}
	mirrors, err := s.editionMirrors(ctx, reported)
	if err == nil {
		out, err = s.applyMirrors(ctx, readerID, mirrors)
	}
	if err != nil {
		s.Log.Warn("reading progress not mirrored to other editions", "reader", readerID, "error", err)
		return nil
	}
	s.announce(readerID, out, editionsBy)
	changed := map[int64]bool{}
	for _, o := range out {
		if o.Result == model.OutcomeApplied || o.Result == model.OutcomeUnread {
			changed[o.SeriesID] = true
		}
	}
	return changed
}

// editionMirrors are the changes that repeat finishing (or unreading) the
// chapters in changes in the other editions of their titles. A chapter
// already in changes is left to its own change.
func (s *Service) editionMirrors(ctx context.Context, changes []Change) ([]Change, error) {
	var ids []int64
	have := make(map[int64]bool, len(changes))
	for _, c := range changes {
		have[c.ChapterID] = true
		if c.Completed || c.Unread {
			ids = append(ids, c.ChapterID)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var src []editionChapter
	for start := 0; start < len(ids); start += 500 {
		var part []editionChapter
		if err := s.DB.NewSelect().TableExpr("chapters AS c").Join("JOIN series AS s ON s.id = c.series_id").
			ColumnExpr("c.id, c.series_id, s.work_id, c.number_sort").
			Where("c.id IN (?)", bun.In(ids[start:min(start+500, len(ids))])).
			Where("s.work_id IS NOT NULL AND c.number_sort >= 0").Scan(ctx, &part); err != nil {
			return nil, err
		}
		src = append(src, part...)
	}
	if len(src) == 0 {
		return nil, nil
	}
	numbered := make(map[int64]editionChapter, len(src))
	works := map[int64]bool{}
	for _, c := range src {
		numbered[c.ID] = c
		works[c.WorkID] = true
	}
	type edition struct {
		work   int64
		number float64
	}
	// the latest change for a title's chapter number wins, as in Record
	want := map[edition]Change{}
	from := map[edition]int64{} // the edition the change came from
	for _, c := range changes {
		if n, ok := numbered[c.ChapterID]; ok && (c.Completed || c.Unread) {
			k := edition{n.WorkID, n.NumberSort}
			want[k], from[k] = c, n.SeriesID
		}
	}
	var targets []editionChapter
	if err := s.DB.NewSelect().TableExpr("chapters AS c").Join("JOIN series AS s ON s.id = c.series_id").
		ColumnExpr("c.id, c.series_id, s.work_id, c.number_sort").
		Where("s.work_id IN (?) AND c.number_sort >= 0", bun.In(keys(works))).
		Order("c.series_id", "c.number_sort", "c.id").Scan(ctx, &targets); err != nil {
		return nil, err
	}
	var out []Change
	for _, t := range targets {
		k := edition{t.WorkID, t.NumberSort}
		c, ok := want[k]
		if !ok || have[t.ID] || t.SeriesID == from[k] {
			continue
		}
		have[t.ID] = true
		out = append(out, Change{ChapterID: t.ID, SeriesID: t.SeriesID, Completed: !c.Unread, Unread: c.Unread})
	}
	return out, nil
}

// applyMirrors applies mirrored changes with Record's rules.
func (s *Service) applyMirrors(ctx context.Context, readerID int64, changes []Change) ([]Outcome, error) {
	if len(changes) == 0 {
		return nil, nil
	}
	ids := make([]int64, len(changes))
	for i, c := range changes {
		ids[i] = c.ChapterID
	}
	cur := map[int64]*model.ChapterReadState{}
	for start := 0; start < len(ids); start += 500 {
		var states []model.ChapterReadState
		if err := s.DB.NewSelect().Model(&states).Where("reader_id = ?", readerID).
			Where("chapter_id IN (?)", bun.In(ids[start:min(start+500, len(ids))])).Scan(ctx); err != nil {
			return nil, err
		}
		for i := range states {
			cur[states[i].ChapterID] = &states[i]
		}
	}
	now := time.Now().UTC()
	out := make([]Outcome, 0, len(changes))
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for _, c := range changes {
			res, err := apply(ctx, tx, readerID, cur[c.ChapterID], c, now)
			if err != nil {
				return err
			}
			out = append(out, Outcome{Change: c, Result: res})
		}
		return nil
	})
	return out, err
}

// CatchUpEditions marks read, in every edition of a title, the chapters a
// reader finished in another edition before the editions were grouped, or
// before the chapter reached that edition. workID 0 catches up every title.
// It never unreads anything; unreads are mirrored as they happen.
func (s *Service) CatchUpEditions(ctx context.Context, workID int64) (int, error) {
	var rows []struct {
		ReaderID  int64 `bun:"reader_id"`
		ChapterID int64 `bun:"chapter_id"`
		SeriesID  int64 `bun:"series_id"`
	}
	q := s.DB.NewSelect().TableExpr("chapter_read_states AS rs").
		Join("JOIN chapters AS c1 ON c1.id = rs.chapter_id").
		Join("JOIN series AS s1 ON s1.id = c1.series_id").
		Join("JOIN series AS s2 ON s2.work_id = s1.work_id AND s2.id <> s1.id").
		Join("JOIN chapters AS c2 ON c2.series_id = s2.id AND c2.number_sort = c1.number_sort").
		Join("LEFT JOIN chapter_read_states AS x ON x.reader_id = rs.reader_id AND x.chapter_id = c2.id").
		ColumnExpr("DISTINCT rs.reader_id, c2.id AS chapter_id, c2.series_id").
		Where("rs.completed AND c1.number_sort >= 0 AND (x.id IS NULL OR NOT x.completed)").
		OrderExpr("rs.reader_id, c2.series_id, c2.id")
	if workID > 0 {
		q = q.Where("s1.work_id = ?", workID)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return 0, err
	}
	byReader := map[int64][]Change{}
	var readers []int64
	for _, r := range rows {
		if _, ok := byReader[r.ReaderID]; !ok {
			readers = append(readers, r.ReaderID)
		}
		byReader[r.ReaderID] = append(byReader[r.ReaderID], Change{ChapterID: r.ChapterID, SeriesID: r.SeriesID, Completed: true})
	}
	n := 0
	changed := map[int64]bool{}
	for _, rid := range readers {
		out, err := s.applyMirrors(ctx, rid, byReader[rid])
		if err != nil {
			return n, err
		}
		s.announce(rid, out, editionsBy)
		for _, o := range out {
			if o.Result == model.OutcomeApplied {
				n++
				changed[o.SeriesID] = true
			}
		}
	}
	for sid := range changed {
		s.Bus.Changed("series", "updated", sid)
	}
	if n > 0 {
		s.Bus.Changed("readers", "sync", 0)
		s.Log.Info("read chapters carried over to other language editions", "chapters", n, "series", len(changed))
	}
	return n, nil
}
