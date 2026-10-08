// Package reading is what reading apps see of the library: every series and
// every chapter (downloaded or not) with one reader's progress, covers,
// pages (from files or streamed from sources) and progress updates. The
// Komga-compatible API (internal/komgaapi) is built on it.
package reading

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/diskcache"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcepriority"
)

// ErrNotFound is returned for unknown series or chapters.
var ErrNotFound = errors.New("not found")

type Service struct {
	DB         *db.DB
	Settings   *settings.Store
	Library    *library.Library
	ImageCache *diskcache.Store
	Mods       *modules.Manager
	HTTP       *http.Client
	Bus        *events.Bus
	Log        *slog.Logger
	// Downloads queues chapters opened before they're downloaded.
	Downloads Grabber
	// Staged finds a page the downloader has already fetched for a chapter
	// being downloaded now ("" when there is none).
	Staged func(ctx context.Context, chapterID int64, n int) string

	streams streams
	bounds  boundsCache
	// readerMu makes the first reader once: a fresh install answering a
	// burst of app requests would otherwise race to create several.
	readerMu sync.Mutex
}

// ReaderID is the reader reading apps act as (the configured one, else the
// first reader; one named "Me" is created when there is none).
func (s *Service) ReaderID(ctx context.Context) (int64, error) {
	rs, err := s.Settings.Reading(ctx)
	if err != nil {
		return 0, err
	}
	if rs.ReaderID > 0 {
		if n, _ := s.DB.NewSelect().Model((*model.Reader)(nil)).Where("id = ?", rs.ReaderID).Count(ctx); n > 0 {
			return rs.ReaderID, nil
		}
	}
	var r model.Reader
	if err := s.DB.NewSelect().Model(&r).Order("id").Limit(1).Scan(ctx); err == nil {
		return r.ID, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// there is none yet: make it once, whoever asks first
	s.readerMu.Lock()
	defer s.readerMu.Unlock()
	if err := s.DB.NewSelect().Model(&r).Order("id").Limit(1).Scan(ctx); err == nil {
		return r.ID, nil
	}
	r = model.Reader{Name: "Me", CreatedAt: time.Now().UTC()}
	if _, err := s.DB.NewInsert().Model(&r).Exec(ctx); err != nil {
		return 0, err
	}
	s.Bus.Changed("readers", "created", r.ID)
	return r.ID, nil
}

// SeriesInfo is a series with the reader's counts.
type SeriesInfo struct {
	Series     model.Series
	Books      int
	Read       int
	InProgress int
	LastRead   *time.Time
	// LastChapterChange is the newest chapter or file change.
	LastChapterChange time.Time
	FirstRelease      *time.Time
	LastRelease       *time.Time
	Dir               string
}

// Unread is the number of chapters not started.
func (si SeriesInfo) Unread() int { return max(si.Books-si.Read-si.InProgress, 0) }

// LastModified is when the series or any chapter last changed.
func (si SeriesInfo) LastModified() time.Time {
	if si.LastChapterChange.After(si.Series.UpdatedAt) {
		return si.LastChapterChange
	}
	return si.Series.UpdatedAt
}

// AllSeries loads every series (id > 0: just that one) with readerID's counts.
func (s *Service) AllSeries(ctx context.Context, readerID, id int64) ([]SeriesInfo, error) {
	var list []model.Series
	q := s.DB.NewSelect().Model(&list).Order("sort_title")
	if id > 0 {
		q = q.Where("id = ?", id)
	} else {
		q = q.Where("preview = ?", false) // previews aren't in the library
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	if sc := scopeOf(ctx); sc != nil {
		kept := list[:0]
		for i := range list {
			if sc.Allows(&list[i]) {
				kept = append(kept, list[i])
			}
		}
		list = kept
	}
	// a device with a language order sees a title once
	var eds *editions
	if id > 0 {
		var err error
		if eds, err = s.titleEditions(ctx, id); err != nil {
			return nil, err
		}
		if eds != nil && len(list) == 1 {
			list = eds.of[eds.shown[id]][:1]
			id = list[0].ID
		}
	} else if eds = editionsFor(ctx, list); eds != nil {
		kept := list[:0]
		for i := range list {
			if eds.shown[list[i].ID] == list[i].ID {
				kept = append(kept, list[i])
			}
		}
		list = kept
	}
	var counts []struct {
		SeriesID int64        `bun:"series_id"`
		Books    int          `bun:"books"`
		Changed  bun.NullTime `bun:"changed"`
		First    bun.NullTime `bun:"first_release"`
		Last     bun.NullTime `bun:"last_release"`
	}
	cq := s.DB.NewSelect().TableExpr("chapters AS c").
		ColumnExpr("c.series_id, COUNT(*) AS books, MAX(c.updated_at) AS changed, MIN(c.release_date) AS first_release, MAX(c.release_date) AS last_release").
		GroupExpr("c.series_id")
	if id > 0 {
		cq = cq.Where("c.series_id = ?", id)
	}
	if err := cq.Scan(ctx, &counts); err != nil {
		return nil, err
	}
	var reads []struct {
		SeriesID   int64        `bun:"series_id"`
		Read       int          `bun:"read_count"`
		InProgress int          `bun:"in_progress"`
		LastRead   bun.NullTime `bun:"last_read"`
	}
	rq := s.DB.NewSelect().TableExpr("chapter_read_states AS rs").
		ColumnExpr("rs.series_id").
		ColumnExpr("SUM(CASE WHEN rs.completed THEN 1 ELSE 0 END) AS read_count").
		ColumnExpr("SUM(CASE WHEN NOT rs.completed AND rs.page > 0 THEN 1 ELSE 0 END) AS in_progress").
		ColumnExpr("MAX(COALESCE(rs.read_at, rs.synced_at)) AS last_read").
		Where("rs.reader_id = ?", readerID).GroupExpr("rs.series_id")
	if id > 0 {
		rq = rq.Where("rs.series_id = ?", id)
	}
	if err := rq.Scan(ctx, &reads); err != nil {
		return nil, err
	}
	type agg struct {
		books, read, prog int
		changed           time.Time
		first, last, lr   *time.Time
	}
	by := map[int64]*agg{}
	get := func(id int64) *agg {
		if by[id] == nil {
			by[id] = &agg{}
		}
		return by[id]
	}
	nt := func(t bun.NullTime) *time.Time {
		if t.IsZero() {
			return nil
		}
		v := t.Time
		return &v
	}
	for _, c := range counts {
		a := get(c.SeriesID)
		a.books, a.changed, a.first, a.last = c.Books, c.Changed.Time, nt(c.First), nt(c.Last)
	}
	for _, r := range reads {
		a := get(r.SeriesID)
		a.read, a.prog, a.lr = r.Read, r.InProgress, nt(r.LastRead)
	}
	roots := map[int64]string{}
	var rfs []model.RootFolder
	_ = s.DB.NewSelect().Model(&rfs).Scan(ctx)
	for _, rf := range rfs {
		roots[rf.ID] = rf.Path
	}
	out := make([]SeriesInfo, 0, len(list))
	for _, ser := range list {
		a := get(ser.ID)
		out = append(out, SeriesInfo{Series: ser, Books: a.books, Read: a.read, InProgress: a.prog, LastRead: a.lr,
			LastChapterChange: a.changed, FirstRelease: a.first, LastRelease: a.last, Dir: filepath.Join(roots[ser.RootFolderID], ser.Path)})
	}
	if eds != nil {
		// titles with several editions count their merged chapters
		var ids []int64
		for i := range out {
			if all := eds.of[out[i].Series.ID]; len(all) > 1 {
				for _, ed := range all {
					ids = append(ids, ed.ID)
				}
			}
		}
		if len(ids) > 0 {
			chapters, err := s.chaptersOf(ctx, ids)
			if err != nil {
				return nil, err
			}
			books, err := s.enrich(ctx, readerID, chapters, ids)
			if err != nil {
				return nil, err
			}
			merged := eds.merge(books)
			for i := range out {
				if len(eds.of[out[i].Series.ID]) > 1 {
					count(&out[i], merged)
				}
			}
		}
	}
	return out, nil
}

// Series loads one series with the reader's counts.
func (s *Service) Series(ctx context.Context, readerID, id int64) (*SeriesInfo, error) {
	list, err := s.AllSeries(ctx, readerID, id)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// BookInfo is a chapter with its file and the reader's state.
type BookInfo struct {
	// Chapter is the chapter; for a device with a language order its
	// SeriesID is the edition the title is shown as (see EditionID).
	Chapter model.Chapter
	// EditionID is the series the chapter really belongs to.
	EditionID int64
	File      *model.ChapterFile
	State     *model.ChapterReadState
	// Index is the chapter's 1-based position in its series (by number).
	Index int
	// Scanlator of the file (or the best known release).
	Scanlator string
	// Path is the CBZ's absolute path ("" when not downloaded).
	Path string
}

// Books loads chapters (seriesID 0: all series), ordered by series and number.
func (s *Service) Books(ctx context.Context, readerID, seriesID int64) ([]BookInfo, error) {
	allowed, err := s.visibleSeries(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	if seriesID > 0 {
		// a device with a language order gets the title's merged chapters
		eds, err := s.titleEditions(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		if eds != nil {
			var ids []int64
			for _, ed := range eds.of[eds.shown[seriesID]] {
				ids = append(ids, ed.ID)
			}
			chapters, err := s.chaptersOf(ctx, ids)
			if err != nil {
				return nil, err
			}
			books, err := s.enrich(ctx, readerID, chapters, ids)
			if err != nil {
				return nil, err
			}
			return eds.merge(books), nil
		}
	}
	var chapters []model.Chapter
	q := s.DB.NewSelect().Model(&chapters).Order("series_id", "number_sort", "id")
	if seriesID > 0 {
		q = q.Where("series_id = ?", seriesID)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	if allowed != nil {
		kept := chapters[:0]
		for _, c := range chapters {
			if allowed[c.SeriesID] {
				kept = append(kept, c)
			}
		}
		chapters = kept
	}
	var only []int64
	if seriesID > 0 {
		only = []int64{seriesID}
	}
	books, err := s.enrich(ctx, readerID, chapters, only)
	if err != nil || seriesID > 0 || len(languagesOf(ctx)) == 0 {
		return books, err
	}
	var list []model.Series
	if err := s.DB.NewSelect().Model(&list).Column("id", "work_id", "language", "preview").Where("preview = ?", false).Scan(ctx); err != nil {
		return nil, err
	}
	if allowed != nil {
		kept := list[:0]
		for i := range list {
			if allowed[list[i].ID] {
				kept = append(kept, list[i])
			}
		}
		list = kept
	}
	return editionsFor(ctx, list).merge(books), nil
}

// Book loads one chapter.
func (s *Service) Book(ctx context.Context, readerID, chapterID int64) (*BookInfo, error) {
	var ch model.Chapter
	if err := s.DB.NewSelect().Model(&ch).Where("id = ?", chapterID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// the index needs the whole series
	all, err := s.Books(ctx, readerID, ch.SeriesID)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Chapter.ID == chapterID {
			return &all[i], nil
		}
	}
	if len(languagesOf(ctx)) > 0 {
		// the device's title shows another language's copy of this chapter;
		// the chapter itself is still readable
		return s.Book(withoutLanguages(ctx), readerID, chapterID)
	}
	return nil, ErrNotFound
}

// FileBook is a downloaded file as a book, found by the file alone (no
// reader, no permissions): for links that carry their own permission.
func (s *Service) FileBook(ctx context.Context, fileID int64) (*BookInfo, error) {
	var f model.ChapterFile
	if err := s.DB.NewSelect().Model(&f).Where("id = ?", fileID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var ch model.Chapter
	if err := s.DB.NewSelect().Model(&ch).Where("id = ?", f.ChapterID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Column("id", "root_folder_id", "path").Where("id = ?", f.SeriesID).Scan(ctx); err != nil {
		return nil, ErrNotFound
	}
	dir, err := s.Library.SeriesDir(ctx, &ser)
	if err != nil || dir == "" {
		return nil, ErrNotFound
	}
	return &BookInfo{Chapter: ch, EditionID: ch.SeriesID, File: &f, Path: filepath.Join(dir, f.RelativePath)}, nil
}

// enrich adds files, states and scanlators to chapters (only: the series
// they are of, nil for any).
func (s *Service) enrich(ctx context.Context, readerID int64, chapters []model.Chapter, only []int64) ([]BookInfo, error) {
	var files []model.ChapterFile
	fq := s.DB.NewSelect().Model(&files)
	var states []model.ChapterReadState
	sq := s.DB.NewSelect().Model(&states).Where("reader_id = ?", readerID)
	var rels []model.ChapterRelease
	relq := s.DB.NewSelect().Model(&rels).Column("chapter_id", "scanlator").Where("chapter_id IS NOT NULL").Where("removed = ?", false)
	if len(only) > 0 {
		fq = fq.Where("series_id IN (?)", bun.In(only))
		sq = sq.Where("series_id IN (?)", bun.In(only))
		relq = relq.Where("series_id IN (?)", bun.In(only))
	}
	if err := fq.Scan(ctx); err != nil {
		return nil, err
	}
	if err := sq.Scan(ctx); err != nil {
		return nil, err
	}
	_ = relq.Scan(ctx)
	fileBy := map[int64]*model.ChapterFile{}
	for i := range files {
		fileBy[files[i].ID] = &files[i]
	}
	stateBy := map[int64]*model.ChapterReadState{}
	for i := range states {
		stateBy[states[i].ChapterID] = &states[i]
	}
	scanBy := map[int64]string{}
	for _, r := range rels {
		if r.ChapterID != nil && r.Scanlator != "" && scanBy[*r.ChapterID] == "" {
			scanBy[*r.ChapterID] = r.Scanlator
		}
	}
	dirs := map[int64]string{}
	out := make([]BookInfo, 0, len(chapters))
	idx := map[int64]int{}
	for _, ch := range chapters {
		idx[ch.SeriesID]++
		b := BookInfo{Chapter: ch, EditionID: ch.SeriesID, State: stateBy[ch.ID], Index: idx[ch.SeriesID], Scanlator: scanBy[ch.ID]}
		if ch.FileID != nil {
			if f := fileBy[*ch.FileID]; f != nil {
				b.File = f
				if f.Scanlator != "" {
					b.Scanlator = f.Scanlator
				}
				dir, ok := dirs[ch.SeriesID]
				if !ok {
					var ser model.Series
					if err := s.DB.NewSelect().Model(&ser).Column("id", "root_folder_id", "path").Where("id = ?", ch.SeriesID).Scan(ctx); err == nil {
						dir, _ = s.Library.SeriesDir(ctx, &ser)
					}
					dirs[ch.SeriesID] = dir
				}
				if dir != "" {
					b.Path = filepath.Join(dir, f.RelativePath)
				}
			}
		}
		out = append(out, b)
	}
	return out, nil
}

// SortSeries orders series by title (the default).
func SortSeries(list []SeriesInfo) {
	sort.SliceStable(list, func(i, j int) bool { return list[i].Series.SortTitle < list[j].Series.SortTitle })
}

// Cover returns a series cover for apps: the library's cover.jpg resized,
// else the metadata cover or the first source's thumbnail (cached).
func (s *Service) Cover(ctx context.Context, ser *model.Series) ([]byte, string, error) {
	if p := s.Library.CoverPath(ctx, ser); p != "" {
		if st, err := os.Stat(p); err == nil {
			key := "file|" + p + "|" + strconv.FormatInt(st.ModTime().UnixNano(), 10) + "|" + strconv.FormatInt(st.Size(), 10)
			data, ct, _, err := s.ImageCache.Get(ctx, "covers", key, 365*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
				f, err := os.Open(p)
				return f, "", err
			})
			if err == nil {
				return data, ct, nil
			}
		}
	}
	key := "series|" + strconv.FormatInt(ser.ID, 10) + "|" + ser.Metadata.CoverURL
	data, ct, _, err := s.ImageCache.Get(ctx, "covers", key, 30*24*time.Hour, func(ctx context.Context) (io.ReadCloser, string, error) {
		if ser.Metadata.CoverURL != "" {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ser.Metadata.CoverURL, nil)
			if resp, err := s.HTTP.Do(req); err == nil {
				if resp.StatusCode == http.StatusOK {
					return resp.Body, resp.Header.Get("Content-Type"), nil
				}
				resp.Body.Close()
			}
		}
		var links []model.SeriesSource
		if err := s.DB.NewSelect().Model(&links).Where("series_id = ? AND enabled = ?", ser.ID, true).Scan(ctx); err != nil {
			return nil, "", err
		}
		if err := sourcepriority.Apply(ctx, s.DB, *ser, links); err != nil {
			return nil, "", err
		}
		if len(links) == 0 {
			return nil, "", ErrNoSource
		}
		ss := links[0]
		th, _, err := modules.GetAs[source.Thumbnails](s.Mods, ss.ModuleID)
		if err != nil {
			return nil, "", err
		}
		return th.Thumbnail(ctx, source.MangaRef{SourceID: ss.SourceID, URL: ss.MangaURL, EngineRef: ss.EngineRef})
	})
	return data, ct, err
}

// scopeOf is the series limit of the request's viewer (nil: no limit, also
// for work mangarr does on its own).
func scopeOf(ctx context.Context) *access.Scope {
	p := access.From(ctx)
	if p == nil || p.Can(access.LibraryEdit) || !p.Scope.Limited() {
		return nil
	}
	return &p.Scope
}

// visibleSeries returns the series the viewer may see (nil: all). For one
// series (id > 0) it's ErrNotFound when hidden.
func (s *Service) visibleSeries(ctx context.Context, id int64) (map[int64]bool, error) {
	sc := scopeOf(ctx)
	if sc == nil {
		// previews aren't listed; with none, everything is visible
		if id > 0 {
			return nil, nil
		}
		if has, err := s.DB.NewSelect().Model((*model.Series)(nil)).Where("preview = ?", true).Exists(ctx); err != nil || !has {
			return nil, err
		}
		sc = &access.Scope{}
	}
	var list []model.Series
	q := s.DB.NewSelect().Model(&list).Column("id", "root_folder_id", "tags", "preview", "metadata")
	if id > 0 {
		q = q.Where("id = ?", id)
	} else {
		q = q.Where("preview = ?", false)
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for i := range list {
		if list[i].Preview || sc.Allows(&list[i]) { // a preview opened by id is readable
			out[list[i].ID] = true
		}
	}
	if id > 0 && !out[id] {
		return nil, ErrNotFound
	}
	return out, nil
}

// CanSee reports whether the viewer may see a series.
func (s *Service) CanSee(ctx context.Context, seriesID int64) bool {
	_, err := s.visibleSeries(ctx, seriesID)
	return err == nil
}
