// Package readsync pulls per-reader progress from library modules (Komga,
// Kavita) and stores it as chapter read states.
package readsync

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/readstate"
)

type Syncer struct {
	db   *db.DB
	mods *modules.Manager
	bus  *events.Bus
	log  *slog.Logger

	mu sync.Mutex
	// protected series keep their read states while progress is restored
	// (right after a move the server reports them as unread)
	protected map[int64]time.Time

	// OnChange, when set, hears what each sync or pushed event changed.
	OnChange func(ctx context.Context, acc *model.ReaderAccount, changes []Change)
}

// Change is one read state a server's report changed (or that mangarr kept
// against a lower report).
type Change struct {
	SeriesID  int64
	ChapterID int64
	Completed bool
	Page      int
	// Outcome is model.OutcomeApplied, OutcomeUnread or OutcomeKept.
	Outcome string
}

func (s *Syncer) report(ctx context.Context, acc *model.ReaderAccount, changes []Change) {
	if s.OnChange != nil && len(changes) > 0 {
		s.OnChange(ctx, acc, changes)
	}
}

func New(d *db.DB, mods *modules.Manager, bus *events.Bus, log *slog.Logger) *Syncer {
	return &Syncer{db: d, mods: mods, bus: bus, log: log, protected: map[int64]time.Time{}}
}

// Protect keeps syncs from lowering or clearing read states of a series for d.
func (s *Syncer) Protect(seriesID int64, d time.Duration) {
	s.mu.Lock()
	s.protected[seriesID] = time.Now().Add(d)
	s.mu.Unlock()
}

// Unprotect ends Protect.
func (s *Syncer) Unprotect(seriesID int64) {
	s.mu.Lock()
	delete(s.protected, seriesID)
	s.mu.Unlock()
}

func (s *Syncer) isProtected(seriesID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	until, ok := s.protected[seriesID]
	return ok && time.Now().Before(until)
}

type Result struct {
	Accounts int `json:"accounts"`
	Updated  int `json:"updated"`
	Failed   int `json:"failed"`
}

type fileRef struct {
	chapterID int64
	seriesID  int64
}

// index maps absolute file paths to chapters.
func (s *Syncer) index(ctx context.Context) (map[string]fileRef, []string, error) {
	var rows []struct {
		ChapterID    int64  `bun:"chapter_id"`
		SeriesID     int64  `bun:"series_id"`
		RelativePath string `bun:"relative_path"`
		SeriesPath   string `bun:"series_path"`
		RootPath     string `bun:"root_path"`
	}
	err := s.db.NewSelect().TableExpr("chapter_files AS f").
		ColumnExpr("f.chapter_id, f.series_id, f.relative_path, s.path AS series_path, r.path AS root_path").
		Join("JOIN series AS s ON s.id = f.series_id").
		Join("JOIN root_folders AS r ON r.id = s.root_folder_id").Scan(ctx, &rows)
	if err != nil {
		return nil, nil, err
	}
	idx := make(map[string]fileRef, len(rows))
	for _, r := range rows {
		idx[filepath.Clean(filepath.Join(r.RootPath, r.SeriesPath, r.RelativePath))] = fileRef{r.ChapterID, r.SeriesID}
	}
	var roots []string
	if err := s.db.NewSelect().Model((*model.RootFolder)(nil)).Column("path").Scan(ctx, &roots); err != nil {
		return nil, nil, err
	}
	return idx, roots, nil
}

// Sync refreshes read states for every reader account.
func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	var res Result
	idx, roots, err := s.index(ctx)
	if err != nil {
		return res, err
	}
	var accounts []model.ReaderAccount
	if err := s.db.NewSelect().Model(&accounts).Scan(ctx); err != nil {
		return res, err
	}
	var errs []error
	for i := range accounts {
		acc := &accounts[i]
		res.Accounts++
		n, err := s.syncAccount(ctx, acc, idx, roots)
		now := time.Now().UTC()
		acc.LastSyncAt = &now
		acc.LastError = ""
		if err != nil {
			res.Failed++
			acc.LastError = err.Error()
			errs = append(errs, err)
			s.log.Warn("read progress sync failed", "reader", acc.ReaderID, "module", acc.ModuleID, "err", err)
		}
		res.Updated += n
		_, _ = s.db.NewUpdate().Model(acc).Column("last_sync_at", "last_error").WherePK().Exec(ctx)
	}
	s.bus.Changed("readers", "sync", 0)
	if res.Accounts > 0 && res.Failed == res.Accounts {
		return res, errors.Join(errs...)
	}
	return res, nil
}

func (s *Syncer) syncAccount(ctx context.Context, acc *model.ReaderAccount, idx map[string]fileRef, roots []string) (int, error) {
	pr, _, err := modules.GetAs[library.ProgressReader](s.mods, acc.ModuleID)
	if err != nil {
		return 0, err
	}
	sctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	progress, err := pr.ReadProgress(sctx, library.Account{Credentials: acc.Credentials}, roots)
	if err != nil {
		return 0, err
	}
	var existing []model.ChapterReadState
	if err := s.db.NewSelect().Model(&existing).Where("reader_id = ?", acc.ReaderID).Scan(ctx); err != nil {
		return 0, err
	}
	byChapter := map[int64]*model.ChapterReadState{}
	for i := range existing {
		byChapter[existing[i].ChapterID] = &existing[i]
	}
	withFile := make(map[int64]bool, len(idx))
	for _, r := range idx {
		withFile[r.chapterID] = true
	}
	now := time.Now().UTC()
	seen := map[int64]bool{}
	updated := 0
	var changes []Change
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		changes = changes[:0]
		for _, bp := range progress {
			ref, ok := idx[filepath.Clean(bp.LocalPath)]
			if !ok {
				continue
			}
			seen[ref.chapterID] = true
			outcome, err := s.apply(ctx, tx, acc.ReaderID, byChapter[ref.chapterID], ref, bp, now)
			if err != nil {
				return err
			}
			if outcome != "" {
				changes = append(changes, Change{SeriesID: ref.seriesID, ChapterID: ref.chapterID, Completed: bp.Completed, Page: bp.Page, Outcome: outcome})
			}
			if outcome == model.OutcomeApplied {
				updated++
			}
		}
		// Chapters that still have files but are no longer reported were
		// marked unread on the server. A state reported on another language
		// edition's chapter is that chapter's to clear: this one may just
		// not have been written to the server yet.
		for chID, st := range byChapter {
			if seen[chID] || st.SourceChapterID != chID {
				continue
			}
			if !withFile[chID] {
				continue // cleaned/deleted files are not reported; keep their state
			}
			if s.isProtected(st.SeriesID) || st.Origin != "" {
				continue // imported states wait until they're written to the server
			}
			if err := readstate.Delete(ctx, tx, st.ID); err != nil {
				return err
			}
			changes = append(changes, Change{SeriesID: st.SeriesID, ChapterID: chID, Outcome: model.OutcomeUnread})
			updated++
		}
		return nil
	})
	if err == nil {
		s.report(ctx, acc, changes)
	}
	return updated, err
}

// apply stores one reported book's progress (st is the current state of
// the book's chapter, nil when none). Restored or imported progress is never lowered, and a server
// report takes over imported (backup) states. It returns
// model.OutcomeApplied, model.OutcomeKept (a lower report was ignored) or
// "" (nothing new).
func (s *Syncer) apply(ctx context.Context, idb bun.IDB, readerID int64, st *model.ChapterReadState, ref fileRef, bp library.BookProgress, now time.Time) (string, error) {
	readAt := bp.ReadAt
	if st == nil {
		if !bp.Completed && bp.Page <= 0 {
			return "", nil
		}
		if readAt == nil && bp.Completed {
			readAt = &now
		}
		st = &model.ChapterReadState{ReaderID: readerID, ChapterID: ref.chapterID, SeriesID: ref.seriesID,
			Completed: bp.Completed, Page: bp.Page, ReadAt: readAt, SyncedAt: now}
		return model.OutcomeApplied, readstate.Save(ctx, idb, st)
	}
	if st.SourceChapterID != ref.chapterID && !(bp.Completed && !st.Completed) {
		// the state came from another language edition of the title: this
		// edition's book can finish the chapter, but its lower (or merely
		// different) position says nothing about the title's
		return "", nil
	}
	if st.Origin == "" && st.Completed == bp.Completed && st.Page == bp.Page && (readAt == nil || (st.ReadAt != nil && st.ReadAt.Equal(*readAt))) {
		return "", nil
	}
	if (s.isProtected(ref.seriesID) || st.Origin != "") && (st.Completed && !bp.Completed || st.Page > bp.Page) {
		return model.OutcomeKept, nil // don't lower progress that is being restored, was imported or came from an app
	}
	sameValues := st.Completed == bp.Completed && st.Page == bp.Page
	if readAt == nil && bp.Completed && !st.Completed {
		readAt = &now
	}
	if readAt == nil {
		readAt = st.ReadAt
	}
	// the server knows this chapter now; it owns the state from here on
	st.Completed, st.Page, st.ReadAt, st.SyncedAt, st.Origin = bp.Completed, bp.Page, readAt, now, ""
	err := readstate.Save(ctx, idb, st)
	if sameValues {
		return "", err // only the owner changed
	}
	return model.OutcomeApplied, err
}

// ApplyEvent stores one change pushed by a library server. It returns the
// series that changed (0 when nothing did).
func (s *Syncer) ApplyEvent(ctx context.Context, acc *model.ReaderAccount, ev library.ProgressEvent) (int64, error) {
	if ev.Book == nil {
		return 0, nil
	}
	idx, _, err := s.index(ctx)
	if err != nil {
		return 0, err
	}
	ref, ok := idx[filepath.Clean(ev.Book.LocalPath)]
	if !ok {
		return 0, nil // not one of our files
	}
	var cur model.ChapterReadState
	found := true
	if err := s.db.NewSelect().Model(&cur).Where("reader_id = ? AND chapter_id = ?", acc.ReaderID, ref.chapterID).Scan(ctx); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, err
		}
		found = false
	}
	outcome := ""
	if ev.Deleted {
		// marked unread on the server
		switch {
		case !found:
		case cur.Origin == "" && !s.isProtected(ref.seriesID):
			if err := readstate.Delete(ctx, s.db, cur.ID); err != nil {
				return 0, err
			}
			outcome = model.OutcomeUnread
		default:
			outcome = model.OutcomeKept
		}
	} else {
		var st *model.ChapterReadState
		if found {
			st = &cur
		}
		if outcome, err = s.apply(ctx, s.db, acc.ReaderID, st, ref, *ev.Book, time.Now().UTC()); err != nil {
			return 0, err
		}
	}
	if outcome != "" {
		c := Change{SeriesID: ref.seriesID, ChapterID: ref.chapterID, Outcome: outcome}
		if ev.Book != nil && !ev.Deleted {
			c.Completed, c.Page = ev.Book.Completed, ev.Book.Page
		}
		s.report(ctx, acc, []Change{c})
	}
	if outcome != model.OutcomeApplied && outcome != model.OutcomeUnread {
		return 0, nil
	}
	s.bus.Changed("readers", "sync", 0)
	s.bus.Changed("series", "updated", ref.seriesID)
	return ref.seriesID, nil
}

// SyncAccount refreshes one reader account.
func (s *Syncer) SyncAccount(ctx context.Context, acc *model.ReaderAccount) (int, error) {
	idx, roots, err := s.index(ctx)
	if err != nil {
		return 0, err
	}
	n, err := s.syncAccount(ctx, acc, idx, roots)
	now := time.Now().UTC()
	acc.LastSyncAt, acc.LastError = &now, ""
	if err != nil {
		acc.LastError = err.Error()
	}
	_, _ = s.db.NewUpdate().Model(acc).Column("last_sync_at", "last_error").WherePK().Exec(ctx)
	if n > 0 {
		s.bus.Changed("readers", "sync", 0)
	}
	return n, err
}

// TestAccount validates credentials for a module and returns the server username.
func (s *Syncer) TestAccount(ctx context.Context, moduleID int64, creds map[string]string) (string, error) {
	pr, def, err := modules.GetAs[library.ProgressReader](s.mods, moduleID)
	if err != nil {
		return "", err
	}
	user, err := pr.TestAccount(ctx, library.Account{Credentials: creds})
	if err != nil {
		return "", fmt.Errorf("%s: %w", def.Name, err)
	}
	return user, nil
}

// RestoreResult summarizes a progress restore.
type RestoreResult struct {
	Written int `json:"written"`
	// Missing counts files the servers don't know yet (not scanned).
	Missing int `json:"missing"`
}

// RestoreSeries writes the read states stored in mangarr back to library
// servers that know less (e.g. after the series moved to another library).
// Progress on the server is never lowered.
func (s *Syncer) RestoreSeries(ctx context.Context, seriesID int64) (RestoreResult, error) {
	return s.PushSeries(ctx, seriesID, nil)
}

// PushSeries is RestoreSeries that also clears the progress of chapters a
// reading app marked unread (unread), where mangarr still has them unread.
func (s *Syncer) PushSeries(ctx context.Context, seriesID int64, unread []int64) (RestoreResult, error) {
	var res RestoreResult
	var accounts []model.ReaderAccount
	if err := s.db.NewSelect().Model(&accounts).Scan(ctx); err != nil || len(accounts) == 0 {
		return res, err
	}
	idx, _, err := s.index(ctx)
	if err != nil {
		return res, err
	}
	pathOf := map[int64]string{}
	var seriesDir string
	for p, ref := range idx {
		if ref.seriesID == seriesID {
			pathOf[ref.chapterID] = p
			seriesDir = filepath.Dir(p)
		}
	}
	if seriesDir == "" {
		return res, nil
	}
	var errs []error
	for _, acc := range accounts {
		pw, _, err := modules.GetAs[library.ProgressWriter](s.mods, acc.ModuleID)
		if err != nil {
			continue // server can't be written to
		}
		var states []model.ChapterReadState
		if err := s.db.NewSelect().Model(&states).Where("reader_id = ? AND series_id = ?", acc.ReaderID, seriesID).Scan(ctx); err != nil {
			return res, err
		}
		remote := map[string]library.BookProgress{}
		if pr, _, err := modules.GetAs[library.ProgressReader](s.mods, acc.ModuleID); err == nil {
			rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			list, err := pr.ReadProgress(rctx, library.Account{Credentials: acc.Credentials}, []string{seriesDir})
			cancel()
			if err != nil {
				errs = append(errs, err)
				continue
			}
			for _, bp := range list {
				remote[filepath.Clean(bp.LocalPath)] = bp
			}
		}
		var items []library.BookProgress
		hasState := map[int64]bool{}
		for _, st := range states {
			hasState[st.ChapterID] = true
			p, ok := pathOf[st.ChapterID]
			if !ok || (!st.Completed && st.Page == 0) {
				continue
			}
			if !st.Completed && st.SourceChapterID != st.ChapterID {
				continue // a page of another language edition's file
			}
			r, has := remote[p]
			if has && (r.Completed || (!st.Completed && r.Page >= st.Page)) {
				continue // the server already knows as much
			}
			items = append(items, library.BookProgress{LocalPath: p, Completed: st.Completed, Page: st.Page})
		}
		for _, chID := range unread {
			p, ok := pathOf[chID]
			if !ok || hasState[chID] {
				continue // not on the server, or read again since
			}
			if r, has := remote[p]; has && (r.Completed || r.Page > 0) {
				items = append(items, library.BookProgress{LocalPath: p, Unread: true})
			}
		}
		if len(items) == 0 {
			continue
		}
		wctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		n, missing, err := pw.WriteProgress(wctx, library.Account{Credentials: acc.Credentials}, items)
		cancel()
		res.Written += n
		res.Missing += len(missing)
		if err != nil {
			errs = append(errs, err)
		}
	}
	return res, errors.Join(errs...)
}
