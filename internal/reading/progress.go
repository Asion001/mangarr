package reading

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// ProgressChanged is published (with Event.SeriesID) when reading progress
// changes; the payload is a ProgressPayload.
const ProgressChanged = "reading.progress"

// ProgressPayload lists the chapters whose progress changed.
type ProgressPayload struct {
	ReaderID   int64   `json:"readerId"`
	ChapterIDs []int64 `json:"chapterIds"`
	Deleted    bool    `json:"deleted"` // marked unread
	// Origin is where the change came from (model.EventOrigin*).
	Origin string `json:"origin"`
}

// By is who reported progress.
type By struct {
	Origin string // model.EventOrigin*
	Client string // "KMReader", "Mihon (Komga extension)"…
	Device string // the reading key's comment
}

// Change is one chapter's reported progress.
type Change struct {
	ChapterID int64
	SeriesID  int64
	Completed bool
	Page      int  // 1-based; 0 = not reported
	Unread    bool // explicitly marked unread
}

// Outcome is what Record did with a change (Result is a model.Outcome*).
type Outcome struct {
	Change
	Result string
}

// Record stores progress reported by reading apps; it's their only write
// path. The rules:
//   - a page update never un-finishes a completed chapter (only an explicit
//     unread does);
//   - otherwise the latest report wins;
//   - states get origin "app", so a library server's sync doesn't lower or
//     delete them until it reports the chapter itself;
//   - finishing or unreading a chapter does the same in the title's other
//     language editions (see mirrorEditions).
func (s *Service) Record(ctx context.Context, readerID int64, changes []Change, by By) ([]Outcome, error) {
	if len(changes) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(changes))
	for _, c := range changes {
		ids = append(ids, c.ChapterID)
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
	if err != nil {
		return nil, err
	}
	changed := map[int64]bool{}
	for _, o := range out {
		if o.Result == model.OutcomeApplied || o.Result == model.OutcomeUnread {
			changed[o.SeriesID] = true
		}
	}
	s.logOutcomes(ctx, readerID, out, by, now)
	s.announce(readerID, out, by)
	for sid := range s.mirrorEditions(ctx, readerID, out) {
		changed[sid] = true
	}
	for sid := range changed {
		s.Bus.Changed("series", "updated", sid)
	}
	if len(changed) > 0 {
		s.Bus.Changed("readers", "sync", 0)
		s.Log.Debug("reading progress recorded", "changes", len(changes), "series", len(changed), "client", by.Client, "device", by.Device)
	}
	return out, nil
}

func apply(ctx context.Context, tx bun.Tx, readerID int64, st *model.ChapterReadState, c Change, now time.Time) (string, error) {
	switch {
	case c.Unread:
		if st == nil {
			return model.OutcomeUnchanged, nil
		}
		_, err := tx.NewDelete().Model(st).WherePK().Exec(ctx)
		return model.OutcomeUnread, err
	case st == nil:
		if !c.Completed && c.Page <= 0 {
			return model.OutcomeUnchanged, nil
		}
		st = &model.ChapterReadState{ReaderID: readerID, ChapterID: c.ChapterID, SeriesID: c.SeriesID, Completed: c.Completed,
			Page: max(c.Page, 0), SyncedAt: now, Origin: model.ReadOriginApp}
		if c.Completed {
			st.ReadAt = &now
		}
		_, err := tx.NewInsert().Model(st).Exec(ctx)
		return model.OutcomeApplied, err
	case st.Completed && !c.Completed:
		return model.OutcomeKept, nil // a page update doesn't un-finish a chapter
	case st.Completed == c.Completed && (c.Page <= 0 || c.Page == st.Page):
		return model.OutcomeUnchanged, nil
	}
	if c.Completed && !st.Completed {
		st.ReadAt = &now
	}
	st.Completed = c.Completed
	if c.Page > 0 {
		st.Page = c.Page
	}
	st.SyncedAt, st.Origin = now, model.ReadOriginApp
	_, err := tx.NewUpdate().Model(st).Column("completed", "page", "read_at", "synced_at", "origin").WherePK().Exec(ctx)
	return model.OutcomeApplied, err
}

// chapterState is a chapter with the reader's state (nil: unread).
type chapterState struct {
	ID         int64
	NumberSort float64
	State      *model.ChapterReadState
}

// seriesStates loads a series' chapters in reading order with readerID's states.
func (s *Service) seriesStates(ctx context.Context, readerID, seriesID int64) ([]chapterState, error) {
	if exists, err := s.DB.NewSelect().Model((*model.Series)(nil)).Where("id = ?", seriesID).Exists(ctx); err != nil {
		return nil, err
	} else if !exists || !s.CanSee(ctx, seriesID) {
		return nil, ErrNotFound
	}
	var chs []model.Chapter
	if err := s.DB.NewSelect().Model(&chs).Column("id", "number_sort").Where("series_id = ?", seriesID).
		Order("number_sort", "id").Scan(ctx); err != nil {
		return nil, err
	}
	var states []model.ChapterReadState
	if err := s.DB.NewSelect().Model(&states).Where("reader_id = ? AND series_id = ?", readerID, seriesID).Scan(ctx); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	by := map[int64]*model.ChapterReadState{}
	for i := range states {
		by[states[i].ChapterID] = &states[i]
	}
	out := make([]chapterState, len(chs))
	for i, c := range chs {
		out[i] = chapterState{ID: c.ID, NumberSort: c.NumberSort, State: by[c.ID]}
	}
	return out, nil
}

// MarkSeries marks every chapter of a series read (or unread).
func (s *Service) MarkSeries(ctx context.Context, readerID, seriesID int64, read bool, by By) ([]Outcome, error) {
	list, err := s.seriesStates(ctx, readerID, seriesID)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, c := range list {
		switch {
		case read && (c.State == nil || !c.State.Completed):
			changes = append(changes, Change{ChapterID: c.ID, SeriesID: seriesID, Completed: true})
		case !read && c.State != nil:
			changes = append(changes, Change{ChapterID: c.ID, SeriesID: seriesID, Unread: true})
		}
	}
	return s.Record(ctx, readerID, changes, by)
}

// MarkReadUpTo marks every chapter numbered up to numberSort as read (it
// never lowers progress), as Komga does for Mihon's tracker.
func (s *Service) MarkReadUpTo(ctx context.Context, readerID, seriesID int64, numberSort float64, by By) ([]Outcome, error) {
	list, err := s.seriesStates(ctx, readerID, seriesID)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for _, c := range list {
		if c.NumberSort <= numberSort && (c.State == nil || !c.State.Completed) {
			changes = append(changes, Change{ChapterID: c.ID, SeriesID: seriesID, Completed: true})
		}
	}
	return s.Record(ctx, readerID, changes, by)
}

// MarkBeforeChapter marks every chapter before chapterID read or unread. It
// uses the stable reading order, so duplicate/special chapter numbers behave
// the same way as Previous in the reader.
func (s *Service) MarkBeforeChapter(ctx context.Context, readerID, seriesID, chapterID int64, read bool, by By) ([]Outcome, error) {
	list, err := s.seriesStates(ctx, readerID, seriesID)
	if err != nil {
		return nil, err
	}
	current := -1
	for i, chapter := range list {
		if chapter.ID == chapterID {
			current = i
			break
		}
	}
	if current < 0 {
		return nil, ErrNotFound
	}
	changes := make([]Change, 0, current)
	for _, chapter := range list[:current] {
		switch {
		case read && (chapter.State == nil || !chapter.State.Completed):
			changes = append(changes, Change{ChapterID: chapter.ID, SeriesID: seriesID, Completed: true})
		case !read && chapter.State != nil:
			changes = append(changes, Change{ChapterID: chapter.ID, SeriesID: seriesID, Unread: true})
		}
	}
	return s.Record(ctx, readerID, changes, by)
}

// MarkReadUpToIndex is MarkReadUpTo by 1-based position (the v1 tracker API).
func (s *Service) MarkReadUpToIndex(ctx context.Context, readerID, seriesID int64, index int, by By) ([]Outcome, error) {
	list, err := s.seriesStates(ctx, readerID, seriesID)
	if err != nil {
		return nil, err
	}
	var changes []Change
	for i, c := range list {
		if i < index && (c.State == nil || !c.State.Completed) {
			changes = append(changes, Change{ChapterID: c.ID, SeriesID: seriesID, Completed: true})
		}
	}
	return s.Record(ctx, readerID, changes, by)
}

// SeriesProgress is a series' progress as Mihon's Komga tracker sees it.
type SeriesProgress struct {
	Books, Read, Unread, InProgress int
	// LastReadContinuous is the number of the last chapter of the run of
	// read chapters (Komga's lastReadContinuousNumberSort); Index is its
	// 1-based position (the v1 API). See Progress.
	LastReadContinuous      float64
	LastReadContinuousIndex int
	MaxNumber               float64
}

// Progress computes a series' tracker progress. Komga counts the run of
// read books from the first book; mangarr lists chapters Komga never had
// (older, undownloaded ones), so the run starts at the first chapter with
// any progress instead: reading 30–40 of a series whose 1–29 you never
// touched reports 40, while a skipped 35 still stops the run at 34.
func (s *Service) Progress(ctx context.Context, readerID, seriesID int64) (SeriesProgress, error) {
	list, err := s.seriesStates(ctx, readerID, seriesID)
	if err != nil {
		return SeriesProgress{}, err
	}
	var p SeriesProgress
	p.Books = len(list)
	start := -1
	for i, c := range list {
		p.MaxNumber = max(p.MaxNumber, c.NumberSort)
		switch {
		case c.State == nil:
			p.Unread++
		case c.State.Completed:
			p.Read++
		case c.State.Page > 0:
			p.InProgress++
		default:
			p.Unread++
		}
		if start < 0 && c.State != nil {
			start = i
		}
	}
	for i := max(start, 0); start >= 0 && i < len(list); i++ {
		if list[i].State == nil || !list[i].State.Completed {
			break
		}
		p.LastReadContinuous, p.LastReadContinuousIndex = list[i].NumberSort, i+1
	}
	return p, nil
}

// ChapterPages loads a chapter and its known page count (0 when unknown)
// without the rest of its series, for progress updates.
func (s *Service) ChapterPages(ctx context.Context, chapterID int64) (*model.Chapter, int, error) {
	var ch model.Chapter
	if err := s.DB.NewSelect().Model(&ch).Where("id = ?", chapterID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	if !s.CanSee(ctx, ch.SeriesID) {
		return nil, 0, ErrNotFound
	}
	if ch.FileID != nil {
		var n int
		if err := s.DB.NewSelect().Model((*model.ChapterFile)(nil)).Column("page_count").Where("id = ?", *ch.FileID).Scan(ctx, &n); err == nil {
			return &ch, n, nil
		}
	}
	return &ch, s.CachedPageCount(ch.ID), nil
}
