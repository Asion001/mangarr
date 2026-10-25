package api

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/sourcepriority"
)

type SeriesStats struct {
	ChapterCount   int   `json:"chapterCount"`
	MonitoredCount int   `json:"monitoredCount"`
	FileCount      int   `json:"fileCount"`
	MissingCount   int   `json:"missingCount"`
	CleanedCount   int   `json:"cleanedCount"`
	SizeOnDisk     int64 `json:"sizeOnDisk"`
	// SpaceSaved is how much smaller processing (re-encoding) made the files.
	SpaceSaved  int64   `json:"spaceSaved"`
	LastChapter float64 `json:"lastChapter"`
	// Read progress over readers who count for cleanup (all readers when
	// none do): chapters finished, chapters started, last read.
	ReadCount       int        `json:"readCount"`
	InProgressCount int        `json:"inProgressCount"`
	LastReadAt      *time.Time `json:"lastReadAt,omitempty"`
}

// ReadingInfo is a series' reading progress (series detail).
type ReadingInfo struct {
	// NextUnread is the first chapter after the last one read.
	NextUnread *NextChapter     `json:"nextUnread,omitempty"`
	Readers    []ReaderProgress `json:"readers"`
	// WebURL opens the series on a library server (e.g. Komga).
	WebURL  string `json:"webUrl,omitempty"`
	WebName string `json:"webName,omitempty"`
}

type NextChapter struct {
	ChapterID int64  `json:"chapterId"`
	Number    string `json:"number"`
	Title     string `json:"title,omitempty"`
	Available bool   `json:"available"` // has a file
}

type ReaderProgress struct {
	ReaderID   int64      `json:"readerId"`
	Reader     string     `json:"reader"`
	Read       int        `json:"read"`
	InProgress int        `json:"inProgress"`
	LastReadAt *time.Time `json:"lastReadAt,omitempty"`
}

type SeriesResource struct {
	model.Series
	Adaptations []AdaptationResource `json:"adaptations" nullable:"false"`
	Stats       SeriesStats          `json:"stats"`
	Sources     []model.SeriesSource `json:"sources,omitempty"`
	CoverURL    string               `json:"coverUrl"`
	FullPath    string               `json:"fullPath,omitempty"`
	Reading     *ReadingInfo         `json:"reading,omitempty"`
	// Following: you follow it (new chapters on your notification targets).
	Following bool `json:"following"`
	// WorkTitle is the canonical list title; Editions are independently
	// managed language variants of that work.
	WorkTitle string           `json:"workTitle,omitempty"`
	Editions  []EditionSummary `json:"editions,omitempty"`
}

type EditionSummary struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Language string `json:"language,omitempty"`
	CoverURL string `json:"coverUrl"`
}

type statsRow struct {
	SeriesID       int64   `bun:"series_id"`
	ChapterCount   int     `bun:"chapter_count"`
	MonitoredCount int     `bun:"monitored_count"`
	FileCount      int     `bun:"file_count"`
	MissingCount   int     `bun:"missing_count"`
	CleanedCount   int     `bun:"cleaned_count"`
	LastChapter    float64 `bun:"last_chapter"`
}

func (s *Server) seriesStats(ctx context.Context, seriesID int64) (map[int64]SeriesStats, error) {
	var rows []statsRow
	q := s.app.DB.NewSelect().TableExpr("chapters AS c").
		ColumnExpr("c.series_id").
		ColumnExpr("COUNT(*) AS chapter_count").
		ColumnExpr("SUM(CASE WHEN c.monitored THEN 1 ELSE 0 END) AS monitored_count").
		ColumnExpr("SUM(CASE WHEN c.file_id IS NOT NULL THEN 1 ELSE 0 END) AS file_count").
		ColumnExpr("SUM(CASE WHEN c.monitored AND c.file_id IS NULL AND c.state <> 'cleaned' THEN 1 ELSE 0 END) AS missing_count").
		ColumnExpr("SUM(CASE WHEN c.state = 'cleaned' THEN 1 ELSE 0 END) AS cleaned_count").
		ColumnExpr("MAX(c.number_sort) AS last_chapter").
		GroupExpr("c.series_id")
	if seriesID > 0 {
		q = q.Where("c.series_id = ?", seriesID)
	}
	if err := q.Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := map[int64]SeriesStats{}
	for _, r := range rows {
		out[r.SeriesID] = SeriesStats{ChapterCount: r.ChapterCount, MonitoredCount: r.MonitoredCount, FileCount: r.FileCount,
			MissingCount: r.MissingCount, CleanedCount: r.CleanedCount, LastChapter: r.LastChapter}
	}
	var sizes []struct {
		SeriesID int64 `bun:"series_id"`
		Size     int64 `bun:"size"`
		Saved    int64 `bun:"saved"`
	}
	sq := s.app.DB.NewSelect().TableExpr("chapter_files").
		ColumnExpr("series_id, SUM(size) AS size, SUM(CASE WHEN size_original > size THEN size_original - size ELSE 0 END) AS saved").GroupExpr("series_id")
	if seriesID > 0 {
		sq = sq.Where("series_id = ?", seriesID)
	}
	if err := sq.Scan(ctx, &sizes); err != nil {
		return nil, err
	}
	for _, sz := range sizes {
		st := out[sz.SeriesID]
		st.SizeOnDisk, st.SpaceSaved = sz.Size, sz.Saved
		out[sz.SeriesID] = st
	}
	readers := s.statsReaders(ctx)
	if len(readers) == 0 {
		return out, nil
	}
	var reads []struct {
		SeriesID   int64        `bun:"series_id"`
		Read       int          `bun:"read_count"`
		InProgress int          `bun:"in_progress"`
		LastRead   bun.NullTime `bun:"last_read"`
	}
	rq := s.app.DB.NewSelect().TableExpr("chapter_read_states AS rs").
		ColumnExpr("rs.series_id").
		ColumnExpr("COUNT(DISTINCT CASE WHEN rs.completed THEN rs.chapter_id END) AS read_count").
		ColumnExpr("COUNT(DISTINCT CASE WHEN NOT rs.completed AND rs.page > 0 THEN rs.chapter_id END) AS in_progress").
		ColumnExpr("MAX(rs.read_at) AS last_read").
		Where("rs.reader_id IN (?)", bun.In(readers)).GroupExpr("rs.series_id")
	if seriesID > 0 {
		rq = rq.Where("rs.series_id = ?", seriesID)
	}
	if err := rq.Scan(ctx, &reads); err != nil {
		return nil, err
	}
	for _, r := range reads {
		st := out[r.SeriesID]
		st.ReadCount, st.InProgressCount = r.Read, r.InProgress
		if !r.LastRead.IsZero() {
			t := r.LastRead.Time
			st.LastReadAt = &t
		}
		out[r.SeriesID] = st
	}
	return out, nil
}

// statsReaders are the readers whose progress the series pages show: a
// signed-in user's own, else (the API key) the ones counting for cleanup.
func (s *Server) statsReaders(ctx context.Context) []int64 {
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser && p.ReaderID > 0 {
		return []int64{p.ReaderID}
	}
	return s.countedReaders(ctx)
}

// ownReaderOnly is set when the caller only sees their own progress (not
// an administrator).
func ownReaderOnly(ctx context.Context) (int64, bool) {
	p := access.From(ctx)
	if p == nil || p.IsAdmin() {
		return 0, false
	}
	return p.ReaderID, true
}

// chapterReaderOnly is the reader whose progress the chapter list shows. A
// signed-in person, administrators included, sees only their own: the list
// marks chapters read and picks where to continue from it, so another
// reader's page there reads as yours. Everyone's totals stay in the reading
// panel.
func chapterReaderOnly(ctx context.Context) (int64, bool) {
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser {
		return p.ReaderID, true
	}
	return ownReaderOnly(ctx)
}

// countedReaders are the readers whose progress counts (cleanup readers,
// or everyone when no reader counts for cleanup).
func (s *Server) countedReaders(ctx context.Context) []int64 {
	var readers []model.Reader
	_ = s.app.DB.NewSelect().Model(&readers).Scan(ctx)
	var counted, all []int64
	for _, r := range readers {
		all = append(all, r.ID)
		if r.CountForCleanup {
			counted = append(counted, r.ID)
		}
	}
	if len(counted) > 0 {
		return counted
	}
	return all
}

// readingInfo describes who read what of a series, and what's next.
func (s *Server) readingInfo(ctx context.Context, ser *model.Series, fullPath string) *ReadingInfo {
	info := &ReadingInfo{Readers: []ReaderProgress{}}
	var rows []struct {
		ReaderID   int64        `bun:"reader_id"`
		Name       string       `bun:"name"`
		Read       int          `bun:"read_count"`
		InProgress int          `bun:"in_progress"`
		LastRead   bun.NullTime `bun:"last_read"`
	}
	_ = s.app.DB.NewSelect().TableExpr("chapter_read_states AS rs").Join("JOIN readers AS r ON r.id = rs.reader_id").
		ColumnExpr("rs.reader_id, r.name").
		ColumnExpr("SUM(CASE WHEN rs.completed THEN 1 ELSE 0 END) AS read_count").
		ColumnExpr("SUM(CASE WHEN NOT rs.completed AND rs.page > 0 THEN 1 ELSE 0 END) AS in_progress").
		ColumnExpr("MAX(rs.read_at) AS last_read").
		Where("rs.series_id = ?", ser.ID).GroupExpr("rs.reader_id, r.name").OrderExpr("r.name").Scan(ctx, &rows)
	own, onlyOwn := ownReaderOnly(ctx)
	labels := s.progressReaderLabels(ctx)
	for _, r := range rows {
		if onlyOwn && r.ReaderID != own {
			continue // others' progress is theirs
		}
		rp := ReaderProgress{ReaderID: r.ReaderID, Reader: progressReaderLabel(ctx, labels, r.ReaderID, r.Name), Read: r.Read, InProgress: r.InProgress}
		if !r.LastRead.IsZero() {
			t := r.LastRead.Time
			rp.LastReadAt = &t
		}
		info.Readers = append(info.Readers, rp)
	}
	if readers := s.statsReaders(ctx); len(readers) > 0 {
		// the first chapter after the highest one read
		var maxRead float64
		err := s.app.DB.NewSelect().TableExpr("chapter_read_states AS rs").Join("JOIN chapters AS c ON c.id = rs.chapter_id").
			ColumnExpr("COALESCE(MAX(c.number_sort), -1)").Where("rs.series_id = ? AND rs.completed AND rs.reader_id IN (?)", ser.ID, bun.In(readers)).
			Scan(ctx, &maxRead)
		if err == nil && maxRead >= 0 {
			var next model.Chapter
			if err := s.app.DB.NewSelect().Model(&next).Where("series_id = ? AND number_sort > ?", ser.ID, maxRead).
				Order("number_sort").Limit(1).Scan(ctx); err == nil {
				info.NextUnread = &NextChapter{ChapterID: next.ID, Number: next.NumberKey, Title: next.Title, Available: next.FileID != nil}
			}
		}
	}
	if fullPath != "" {
		for _, m := range modules.ActiveAs[library.WebLinker](s.app.Modules, modules.KindLibrary) {
			wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			u, err := m.Instance.SeriesURL(wctx, fullPath)
			cancel()
			if err == nil && u != "" {
				info.WebURL, info.WebName = u, m.Def.Name
				break
			}
		}
	}
	return info
}

// progressReaderLabels decouples a person's visible identity from the name of
// the storage bucket their progress was first imported into.
func (s *Server) progressReaderLabels(ctx context.Context) map[int64]string {
	var users []model.User
	_ = s.app.DB.NewSelect().Model(&users).Column("reader_id", "username", "display_name").Where("reader_id IS NOT NULL").Scan(ctx)
	out := make(map[int64]string, len(users))
	for _, u := range users {
		name := strings.TrimSpace(u.DisplayName)
		if name == "" {
			name = u.Username
		}
		out[u.ReaderID] = name
	}
	return out
}

func progressReaderLabel(ctx context.Context, labels map[int64]string, readerID int64, fallback string) string {
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser && p.ReaderID == readerID {
		return "You"
	}
	if name := labels[readerID]; name != "" {
		return name
	}
	return fallback
}

// sourceCounts fills each link's chapter count and how many chapter files
// came from it.
func (s *Server) sourceCounts(ctx context.Context, links []model.SeriesSource) {
	if len(links) == 0 {
		return
	}
	ids := make([]int64, len(links))
	for i, l := range links {
		ids[i] = l.ID
	}
	type row struct {
		SeriesSourceID int64 `bun:"series_source_id"`
		N              int   `bun:"n"`
	}
	var chapters, files []row
	_ = s.app.DB.NewSelect().Model((*model.ChapterRelease)(nil)).ColumnExpr("series_source_id, COUNT(DISTINCT chapter_id) AS n").
		Where("series_source_id IN (?)", bun.In(ids)).Where("chapter_id IS NOT NULL").Where("removed = ?", false).Group("series_source_id").Scan(ctx, &chapters)
	_ = s.app.DB.NewSelect().TableExpr("chapter_files AS f").Join("JOIN chapter_releases AS r ON r.id = f.release_id").
		ColumnExpr("r.series_source_id, COUNT(*) AS n").Where("r.series_source_id IN (?)", bun.In(ids)).Group("r.series_source_id").Scan(ctx, &files)
	byID := func(rows []row) map[int64]int {
		m := map[int64]int{}
		for _, r := range rows {
			m[r.SeriesSourceID] = r.N
		}
		return m
	}
	c, f := byID(chapters), byID(files)
	for i := range links {
		nc, nf := c[links[i].ID], f[links[i].ID]
		links[i].Chapters, links[i].Files = &nc, &nf
	}
}

func (s *Server) seriesResource(ctx context.Context, ser model.Series, stats map[int64]SeriesStats, detail bool) SeriesResource {
	r := SeriesResource{Series: ser, Stats: stats[ser.ID],
		CoverURL: seriesCoverURL(ser)}
	r.Adaptations = make([]AdaptationResource, len(ser.Metadata.Adaptations))
	for i, a := range ser.Metadata.Adaptations {
		r.Adaptations[i] = AdaptationResource{Adaptation: a, WatchLinks: []WatchLink{}}
	}
	if detail {
		_ = s.app.DB.NewSelect().Model(&r.Sources).Where("series_id = ?", ser.ID).Order("priority", "id").Scan(ctx)
		if ranks, err := sourcepriority.Ranks(ctx, s.app.DB, ser, r.Sources); err == nil {
			for i := range r.Sources {
				rank := ranks[r.Sources[i].ID]
				r.Sources[i].EffectivePriority = &rank
			}
		}
		s.sourceCounts(ctx, r.Sources)
		r.FullPath, _ = s.app.Library.SeriesDir(ctx, &ser)
		r.Reading = s.readingInfo(ctx, &ser, r.FullPath)
		s.adaptationWatchLinks(ctx, r.Adaptations)
	}
	return r
}

// seriesCoverURL is a series' cover, relative to the URL base, with the
// series' last change as a cache-buster.
func seriesCoverURL(ser model.Series) string {
	return "api/v1/series/" + strconv.FormatInt(ser.ID, 10) + "/cover?v=" + strconv.FormatInt(ser.UpdatedAt.Unix(), 10)
}

func editionSummary(ser model.Series) EditionSummary {
	return EditionSummary{ID: ser.ID, Title: ser.Title, Language: ser.Language,
		CoverURL: seriesCoverURL(ser)}
}

func addStats(a, b SeriesStats) SeriesStats {
	a.ChapterCount += b.ChapterCount
	a.MonitoredCount += b.MonitoredCount
	a.FileCount += b.FileCount
	a.MissingCount += b.MissingCount
	a.CleanedCount += b.CleanedCount
	a.SizeOnDisk += b.SizeOnDisk
	a.SpaceSaved += b.SpaceSaved
	a.ReadCount += b.ReadCount
	a.InProgressCount += b.InProgressCount
	if b.LastChapter > a.LastChapter {
		a.LastChapter = b.LastChapter
	}
	if b.LastReadAt != nil && (a.LastReadAt == nil || b.LastReadAt.After(*a.LastReadAt)) {
		a.LastReadAt = b.LastReadAt
	}
	return a
}
