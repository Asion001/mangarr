package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/decision"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/naming"
	"github.com/Asion001/mangarr/internal/organize"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/sourcepriority"
)

func init() { register((*Server).registerSeries) }

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

// AddEditionsResponse is the title and the editions an add touched.
type AddEditionsResponse struct {
	WorkID   int64            `json:"workId"`
	Editions []SeriesResource `json:"editions"`
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

func (s *Server) workContext(ctx context.Context, seriesID int64) (string, []EditionSummary) {
	var selected model.Series
	if err := s.app.DB.NewSelect().Model(&selected).Column("work_id").Where("id = ?", seriesID).Scan(ctx); err != nil || selected.WorkID == 0 {
		return "", nil
	}
	var work model.Work
	_ = s.app.DB.NewSelect().Model(&work).Column("title").Where("id = ?", selected.WorkID).Scan(ctx)
	var editions []model.Series
	_ = s.app.DB.NewSelect().Model(&editions).Where("work_id = ?", selected.WorkID).Order("language", "id").Scan(ctx)
	p := access.From(ctx)
	out := make([]EditionSummary, 0, len(editions))
	for _, edition := range editions {
		if p.Sees(&edition) {
			out = append(out, editionSummary(edition))
		}
	}
	return work.Title, out
}

type ReleaseView struct {
	model.ChapterRelease
	SourceName  string `json:"sourceName"`
	Priority    int    `json:"priority"`
	Blocklisted bool   `json:"blocklisted"`
}

type ReadStateView struct {
	ReaderID  int64      `json:"readerId"`
	Reader    string     `json:"reader"`
	Completed bool       `json:"completed"`
	Page      int        `json:"page"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
}

type ChapterResource struct {
	model.Chapter
	File     *model.ChapterFile `json:"file,omitempty"`
	Releases []ReleaseView      `json:"releases"`
	ReadBy   []ReadStateView    `json:"readBy"`
	Job      *model.DownloadJob `json:"job,omitempty"`
}

func (s *Server) chapterResources(ctx context.Context, seriesID int64) ([]ChapterResource, error) {
	db := s.app.DB
	var chapters []model.Chapter
	if err := db.NewSelect().Model(&chapters).Where("series_id = ?", seriesID).Order("number_sort DESC").Scan(ctx); err != nil {
		return nil, err
	}
	var files []model.ChapterFile
	if err := db.NewSelect().Model(&files).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := db.NewSelect().Model(&sources).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	var rels []model.ChapterRelease
	if err := db.NewSelect().Model(&rels).Where("series_id = ?", seriesID).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	var bl []model.Blocklist
	_ = db.NewSelect().Model(&bl).Where("series_id = ?", seriesID).Scan(ctx)
	var jobs []model.DownloadJob
	_ = db.NewSelect().Model(&jobs).Where("series_id = ?", seriesID).Order("id").Scan(ctx)
	type rs struct {
		model.ChapterReadState
		Name string `bun:"name"`
	}
	var reads []rs
	_ = db.NewSelect().TableExpr("chapter_read_states AS r").ColumnExpr("r.*, rd.name").
		Join("JOIN readers AS rd ON rd.id = r.reader_id").Where("r.series_id = ?", seriesID).Scan(ctx, &reads)

	fileBy := map[int64]*model.ChapterFile{}
	for i := range files {
		fileBy[files[i].ChapterID] = &files[i]
	}
	srcBy := map[int64]model.SeriesSource{}
	for _, ss := range sources {
		srcBy[ss.ID] = ss
	}
	blocked := map[string]bool{}
	for _, b := range bl {
		blocked[strconv.FormatInt(b.SeriesSourceID, 10)+"|"+b.ChapterURL] = true
	}
	relBy := map[int64][]ReleaseView{}
	for _, r := range rels {
		if r.ChapterID == nil {
			continue
		}
		ss := srcBy[r.SeriesSourceID]
		relBy[*r.ChapterID] = append(relBy[*r.ChapterID], ReleaseView{ChapterRelease: r, SourceName: ss.SourceName, Priority: ss.Priority,
			Blocklisted: blocked[strconv.FormatInt(r.SeriesSourceID, 10)+"|"+r.ChapterURL]})
	}
	jobBy := map[int64]*model.DownloadJob{}
	activeStatuses := downloads.ActiveStatuses()
	for i := range jobs {
		previous := jobBy[jobs[i].ChapterID]
		if previous == nil || !slices.Contains(activeStatuses, previous.Status) {
			jobBy[jobs[i].ChapterID] = &jobs[i] // active job wins; otherwise the latest
		}
	}
	readBy := map[int64][]ReadStateView{}
	own, onlyOwn := ownReaderOnly(ctx)
	labels := s.progressReaderLabels(ctx)
	for _, r := range reads {
		if onlyOwn && r.ReaderID != own {
			continue
		}
		readBy[r.ChapterID] = append(readBy[r.ChapterID], ReadStateView{ReaderID: r.ReaderID,
			Reader: progressReaderLabel(ctx, labels, r.ReaderID, r.Name), Completed: r.Completed, Page: r.Page, ReadAt: r.ReadAt})
	}
	out := make([]ChapterResource, 0, len(chapters))
	for _, ch := range chapters {
		cr := ChapterResource{Chapter: ch, File: fileBy[ch.ID], Releases: relBy[ch.ID], ReadBy: readBy[ch.ID], Job: jobBy[ch.ID]}
		if cr.Releases == nil {
			cr.Releases = []ReleaseView{}
		}
		if cr.ReadBy == nil {
			cr.ReadBy = []ReadStateView{}
		}
		out = append(out, cr)
	}
	return out, nil
}

func seriesError(err error) error {
	var ve series.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ve):
		return huma.Error400BadRequest(ve.Msg)
	case errors.Is(err, series.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, series.ErrExists):
		return huma.Error409Conflict(err.Error())
	}
	return toHTTPError(err)
}

type LookupResult struct {
	metadataagg.Candidate
	// ExistingSeriesID is the series in the library (one you can see).
	ExistingSeriesID int64 `json:"existingSeriesId,omitempty"`
	// Request is an open request for it.
	Request *LookupRequest `json:"request,omitempty"`
}

// LookupRequest describes an open request for a lookup result.
type LookupRequest struct {
	ID     int64  `json:"id"`
	Status string `json:"status"`
	// Mine: you asked for it.
	Mine bool `json:"mine"`
}

// lookupIDs are a result's external ids including its own provider's.
func lookupIDs(md metadata.SeriesMetadata) map[string]string {
	ids := map[string]string{}
	for k, v := range md.ExternalIDs {
		ids[k] = v
	}
	if md.Provider != "" && md.ID != "" {
		ids[md.Provider] = md.ID
	}
	return ids
}

// requestedByExternalID finds open requests for lookup results.
func (s *Server) requestedByExternalID(ctx context.Context) func(ids map[string]string) *LookupRequest {
	var open []model.Request
	_ = s.app.DB.NewSelect().Model(&open).Where("status IN (?)", bun.In([]string{model.RequestPending, model.RequestApproved})).Scan(ctx)
	mine := map[int64]bool{}
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser && len(open) > 0 {
		var ids []int64
		_ = s.app.DB.NewSelect().Model((*model.RequestUser)(nil)).Column("request_id").Where("user_id = ?", p.UserID).Scan(ctx, &ids)
		for _, id := range ids {
			mine[id] = true
		}
	}
	return func(ids map[string]string) *LookupRequest {
		for _, r := range open {
			for k, v := range ids {
				if k != "mal" && v != "" && r.Metadata.ExternalIDs[k] == v {
					return &LookupRequest{ID: r.ID, Status: r.Status, Mine: mine[r.ID]}
				}
			}
		}
		return nil
	}
}

// moveAfterUpdate queues a MoveSeries command when the root folder or folder
// changed, or when the title changed and folders follow titles.
func (s *Server) moveAfterUpdate(ctx context.Context, before, after model.Series, req series.UpdateRequest) error {
	move := organize.MoveRequest{SeriesID: after.ID, MoveFiles: req.MoveFiles == nil || *req.MoveFiles}
	if req.RootFolderID != nil && *req.RootFolderID != before.RootFolderID {
		if _, err := s.app.Library.RootFolder(ctx, *req.RootFolderID); err != nil {
			return huma.Error400BadRequest("unknown root folder")
		}
		move.RootFolderID = *req.RootFolderID
	}
	if req.Path != nil && strings.TrimSpace(*req.Path) != "" && *req.Path != before.Path {
		move.Path = *req.Path
	} else if before.Title != after.Title {
		if mm, _ := s.app.Settings.MediaManagement(ctx); mm.RenameFolderOnTitleChange {
			if name := naming.Sanitize(s.app.Library.FolderName(ctx, after.Title, after.Metadata.Year)); name != "" && name != before.Path {
				move.Path = name
			}
		}
	}
	if move.RootFolderID == 0 && move.Path == "" {
		return nil
	}
	_, err := s.app.Queue.Push(ctx, "MoveSeries", toBody(move), "series-edit")
	return toHTTPError(err)
}

// toBody converts a request struct to a command body.
func toBody(v any) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// existingByExternalID returns a matcher from external ids to series in the library.
func (s *Server) existingByExternalID(ctx context.Context) func(ids map[string]string) int64 {
	var existing []model.Series
	_ = s.app.DB.NewSelect().Model(&existing).Column("id", "metadata", "tags", "root_folder_id").Where("preview = ?", false).Scan(ctx)
	p := access.From(ctx)
	return func(ids map[string]string) int64 {
		for _, e := range existing {
			if p != nil && !p.Sees(&e) {
				continue // hidden series stay hidden
			}
			for k, v := range ids {
				if k != "mal" && v != "" && e.Metadata.ExternalIDs[k] == v {
					return e.ID
				}
			}
		}
		return 0
	}
}

// visibleSeries loads a series the caller may see (404 otherwise, so hidden
// series don't reveal they exist).
func (s *Server) visibleSeries(ctx context.Context, id int64) (*model.Series, error) {
	ser, err := s.app.Series.Get(ctx, id)
	if err != nil {
		return nil, seriesError(err)
	}
	if !access.From(ctx).Sees(ser) {
		return nil, huma.Error404NotFound("series not found")
	}
	return ser, nil
}

// groupedSeriesResources turns visible language editions into one row per
// canonical work. Callers may pre-filter list by library or language; only
// those editions then contribute to the row and its aggregate statistics.
func (s *Server) groupedSeriesResources(ctx context.Context, list []model.Series) ([]SeriesResource, error) {
	stats, err := s.seriesStats(ctx, 0)
	if err != nil {
		return nil, err
	}
	p := access.From(ctx)
	following := s.follows(ctx)
	visible := make([]model.Series, 0, len(list))
	for _, ser := range list {
		if p.Sees(&ser) {
			visible = append(visible, ser)
		}
	}
	works := map[int64]model.Work{}
	var workRows []model.Work
	if err := s.app.DB.NewSelect().Model(&workRows).Scan(ctx); err != nil {
		return nil, err
	}
	for _, work := range workRows {
		works[work.ID] = work
	}
	groups := map[int64][]model.Series{}
	var order []int64
	for _, ser := range visible {
		key := ser.WorkID
		if key == 0 {
			key = -ser.ID
		}
		if len(groups[key]) == 0 {
			order = append(order, key)
		}
		groups[key] = append(groups[key], ser)
	}
	out := make([]SeriesResource, 0, len(groups))
	for _, key := range order {
		editions := groups[key]
		representative := editions[0]
		r := s.seriesResource(ctx, representative, stats, false)
		r.Editions = make([]EditionSummary, 0, len(editions))
		r.Stats = SeriesStats{}
		for _, edition := range editions {
			r.Editions = append(r.Editions, editionSummary(edition))
			r.Stats = addStats(r.Stats, stats[edition.ID])
			r.Following = r.Following || following[edition.ID]
		}
		if work, ok := works[representative.WorkID]; ok {
			r.WorkTitle = work.Title
			r.Title, r.SortTitle = work.Title, work.SortTitle
		}
		out = append(out, r)
	}
	return out, nil
}

func (s *Server) registerSeries() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-list", Method: http.MethodGet, Path: "/api/v1/series", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []SeriesResource }, error) {
			var list []model.Series
			if err := s.app.DB.NewSelect().Model(&list).Where("preview = ?", false).Order("sort_title").Scan(ctx); err != nil {
				return nil, toHTTPError(err)
			}
			out, err := s.groupedSeriesResources(ctx, list)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body []SeriesResource }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-get", Method: http.MethodGet, Path: "/api/v1/series/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body SeriesResource }, error) {
			ser, err := s.visibleSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if ser.Preview {
				_ = s.app.Series.TouchPreview(ctx, ser.ID) // opened: keep it
			}
			stats, err := s.seriesStats(ctx, in.ID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			r := s.seriesResource(ctx, *ser, stats, true)
			r.Following = s.follows(ctx)[ser.ID]
			r.WorkTitle, r.Editions = s.workContext(ctx, ser.ID)
			return &struct{ Body SeriesResource }{r}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-work-update", Method: http.MethodPut, Path: "/api/v1/series/{id}/work", Tags: tags,
		Summary: "Group this language edition with a work, or separate it with workId 0"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				WorkID int64 `json:"workId"`
			}
		}) (*struct{}, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			return nil, seriesError(s.app.Series.SetWork(ctx, in.ID, in.Body.WorkID))
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-lookup", Method: http.MethodGet, Path: "/api/v1/series/lookup", Tags: tags,
		Summary: "Search metadata modules (merged by priority) for a new series"},
		func(ctx context.Context, in *struct {
			Query string `query:"q" minLength:"1"`
			Lang  string `query:"lang" required:"false"`
		}) (*struct {
			Body struct {
				Results []LookupResult `json:"results"`
				Errors  []string       `json:"errors"`
				// Providers is how many metadata modules were searched; 0 means
				// none is set up, so an empty result says nothing about the title.
				Providers int `json:"providers"`
			}
		}, error) {
			cands, errs := s.app.Metadata.SearchLanguage(ctx, in.Query, in.Lang, 10)
			out := &struct {
				Body struct {
					Results   []LookupResult `json:"results"`
					Errors    []string       `json:"errors"`
					Providers int            `json:"providers"`
				}
			}{}
			out.Body.Results, out.Body.Errors = []LookupResult{}, []string{}
			out.Body.Providers = len(s.app.Modules.Active(modules.KindMetadata))
			existing := s.existingByExternalID(ctx)
			requested := s.requestedByExternalID(ctx)
			for _, c := range cands {
				out.Body.Results = append(out.Body.Results, LookupResult{Candidate: c, ExistingSeriesID: existing(lookupIDs(c.SeriesMetadata)),
					Request: requested(lookupIDs(c.SeriesMetadata))})
			}
			for _, e := range errs {
				out.Body.Errors = append(out.Body.Errors, e.Error())
			}
			return out, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-lookup-get", Method: http.MethodGet, Path: "/api/v1/series/lookup/{moduleId}/{id}", Tags: tags,
		Summary: "Get one metadata result by module and provider id"},
		func(ctx context.Context, in *struct {
			ModuleID int64  `path:"moduleId"`
			ID       string `path:"id"`
			Lang     string `query:"lang" required:"false"`
		}) (*struct{ Body LookupResult }, error) {
			mod, def, err := modules.GetAs[metadata.Module](s.app.Modules, in.ModuleID)
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			var md *metadata.SeriesMetadata
			if localized, ok := mod.(metadata.LanguageGetter); ok && in.Lang != "" {
				md, err = localized.GetLanguage(ctx, in.ID, in.Lang)
			} else {
				md, err = mod.Get(ctx, in.ID)
			}
			if err != nil {
				return nil, huma.Error404NotFound(err.Error())
			}
			c := metadataagg.Candidate{SeriesMetadata: *md, ModuleID: def.ID, ModuleName: def.Name}
			ids := lookupIDs(*md)
			return &struct{ Body LookupResult }{LookupResult{Candidate: c, ExistingSeriesID: s.existingByExternalID(ctx)(ids), Request: s.requestedByExternalID(ctx)(ids)}}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-add", Method: http.MethodPost, Path: "/api/v1/series", Tags: tags},
		func(ctx context.Context, in *struct{ Body series.AddRequest }) (*struct{ Body SeriesResource }, error) {
			ser, err := s.app.Series.Add(ctx, in.Body)
			var exists series.ExistsError
			if err != nil && in.Body.RequestID > 0 && errors.As(err, &exists) {
				// Retrying an add after the series row was committed but request
				// fulfilment failed must finish the link, not create a duplicate.
				ser, err = s.app.Series.Get(ctx, exists.SeriesID)
			}
			if err != nil {
				return nil, seriesError(err)
			}
			if in.Body.RequestID > 0 {
				if err := s.app.Requests.Link(ctx, in.Body.RequestID, ser.ID, access.From(ctx)); err != nil {
					return nil, huma.Error500InternalServerError(fmt.Sprintf(
						"series %d was added, but the request is still pending: %v; retry Add or link it from Requests", ser.ID, err))
				}
			}
			return &struct{ Body SeriesResource }{s.seriesResource(ctx, *ser, nil, true)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-add-editions", Method: http.MethodPost, Path: "/api/v1/series/editions", Tags: tags,
		Summary: "Add a title in one or more languages", Description: "Sources are split by language; each language becomes an edition in its own root folder."},
		func(ctx context.Context, in *struct{ Body series.AddEditionsRequest }) (*struct{ Body AddEditionsResponse }, error) {
			res, err := s.app.Series.AddEditions(ctx, in.Body)
			if err != nil && (res == nil || len(res.Editions) == 0) {
				return nil, seriesError(err)
			}
			if err != nil {
				return nil, huma.Error500InternalServerError(fmt.Sprintf("some editions were added, but not all: %v", err))
			}
			if in.Body.RequestID > 0 {
				if err := s.app.Requests.Link(ctx, in.Body.RequestID, res.Editions[0].ID, access.From(ctx)); err != nil {
					return nil, huma.Error500InternalServerError(fmt.Sprintf(
						"series %d was added, but the request is still pending: %v; retry Add or link it from Requests", res.Editions[0].ID, err))
				}
			}
			out := AddEditionsResponse{WorkID: res.WorkID, Editions: make([]SeriesResource, 0, len(res.Editions))}
			for _, ser := range res.Editions {
				out.Editions = append(out.Editions, s.seriesResource(ctx, *ser, nil, true))
			}
			return &struct{ Body AddEditionsResponse }{out}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-update", Method: http.MethodPut, Path: "/api/v1/series/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body series.UpdateRequest
		}) (*struct{ Body SeriesResource }, error) {
			var before model.Series
			if err := s.app.DB.NewSelect().Model(&before).Where("id = ?", in.ID).Scan(ctx); err != nil {
				return nil, huma.Error404NotFound("series not found")
			}
			ser, err := s.app.Series.Update(ctx, in.ID, in.Body)
			if err != nil {
				return nil, seriesError(err)
			}
			if in.Body.ProfileID != nil {
				s.app.PushProcessBacklog("series-profile") // the new profile may process differently
			}
			if err := s.moveAfterUpdate(ctx, before, *ser, in.Body); err != nil {
				return nil, err
			}
			stats, _ := s.seriesStats(ctx, in.ID)
			return &struct{ Body SeriesResource }{s.seriesResource(ctx, *ser, stats, true)}, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-delete", Method: http.MethodDelete, Path: "/api/v1/series/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID          int64 `path:"id"`
			DeleteFiles bool  `query:"deleteFiles"`
		}) (*struct{}, error) {
			return nil, seriesError(s.app.Series.Delete(ctx, in.ID, in.DeleteFiles))
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-chapters", Method: http.MethodGet, Path: "/api/v1/series/{id}/chapters", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{ Body []ChapterResource }, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			out, err := s.chapterResources(ctx, in.ID)
			return &struct{ Body []ChapterResource }{out}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "chapters-monitor", Method: http.MethodPut, Path: "/api/v1/chapters/monitor", Tags: tags},
		func(ctx context.Context, in *struct {
			Body struct {
				ChapterIDs []int64 `json:"chapterIds" minItems:"1"`
				Monitored  bool    `json:"monitored"`
			}
		}) (*struct{}, error) {
			_, err := s.app.DB.NewUpdate().Model((*model.Chapter)(nil)).Set("monitored = ?", in.Body.Monitored).
				Set("updated_at = ?", time.Now().UTC()).Where("id IN (?)", bun.In(in.Body.ChapterIDs)).Exec(ctx)
			s.app.Bus.Changed("chapter", "updated", 0)
			return nil, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "chapter-decision", Method: http.MethodGet, Path: "/api/v1/series/{id}/chapters/{chapterId}/decision", Tags: tags,
		Summary: "Explain why a chapter would or would not be downloaded"},
		func(ctx context.Context, in *struct {
			ID        int64 `path:"id"`
			ChapterID int64 `path:"chapterId"`
		}) (*struct {
			Body struct {
				Decision *decision.Decision `json:"decision"`
				Approved *ReleaseView       `json:"approved,omitempty"`
			}
		}, error) {
			d, best, err := s.app.Searcher.Explain(ctx, in.ID, in.ChapterID)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out := &struct {
				Body struct {
					Decision *decision.Decision `json:"decision"`
					Approved *ReleaseView       `json:"approved,omitempty"`
				}
			}{}
			if d == nil {
				d = &decision.Decision{}
			}
			if d.Rejections == nil {
				d.Rejections = []decision.Rejection{}
			}
			out.Body.Decision = d
			if best != nil {
				out.Body.Approved = &ReleaseView{ChapterRelease: best.Release, SourceName: best.Source.SourceName, Priority: best.Source.Priority}
			}
			return out, nil
		})

	type commandResult struct{ Body *model.Command }
	push := func(ctx context.Context, name string, body map[string]any) (*commandResult, error) {
		c, err := s.app.Queue.Push(ctx, name, body, "manual")
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &commandResult{c}, nil
	}
	huma.Register(s.api, huma.Operation{OperationID: "series-refresh", Method: http.MethodPost, Path: "/api/v1/series/{id}/refresh", Tags: tags},
		func(ctx context.Context, in *IDPath) (*commandResult, error) {
			return push(ctx, "RefreshSeries", map[string]any{"seriesId": in.ID})
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-search", Method: http.MethodPost, Path: "/api/v1/series/{id}/search", Tags: tags,
		Summary: "Grab missing chapters. With chapterIds, only those (explicit search ignores monitoring)."},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body *struct {
				ChapterIDs []int64 `json:"chapterIds,omitempty"`
			}
		}) (*commandResult, error) {
			body := map[string]any{"seriesId": in.ID}
			if in.Body != nil && len(in.Body.ChapterIDs) > 0 {
				body["chapterIds"], body["explicit"] = in.Body.ChapterIDs, true
			}
			return push(ctx, "SearchMissing", body)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-metadata-refresh", Method: http.MethodPost, Path: "/api/v1/series/{id}/metadata/refresh", Tags: tags},
		func(ctx context.Context, in *IDPath) (*commandResult, error) {
			return push(ctx, "RefreshMetadata", map[string]any{"seriesId": in.ID})
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-metadata-link", Method: http.MethodPut, Path: "/api/v1/series/{id}/metadata", Tags: tags,
		Summary: "Replace the metadata match of a series"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body metadataagg.Ref
		}) (*struct{ Body SeriesResource }, error) {
			ser, err := s.app.Series.LinkMetadata(ctx, in.ID, in.Body)
			if err != nil {
				return nil, seriesError(err)
			}
			return &struct{ Body SeriesResource }{s.seriesResource(ctx, *ser, nil, true)}, nil
		})

	// ---- source links
	huma.Register(s.api, huma.Operation{OperationID: "series-source-link", Method: http.MethodPost, Path: "/api/v1/series/{id}/sources", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body series.SourceLink
		}) (*struct{ Body *model.SeriesSource }, error) {
			ss, err := s.app.Series.LinkSource(ctx, in.ID, in.Body)
			if err != nil {
				if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
					return nil, huma.Error409Conflict("source already linked")
				}
				return nil, seriesError(err)
			}
			return &struct{ Body *model.SeriesSource }{ss}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-update", Method: http.MethodPut, Path: "/api/v1/series/{id}/sources/{linkId}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID     int64 `path:"id"`
			LinkID int64 `path:"linkId"`
			Body   series.SourceUpdate
		}) (*struct{ Body *model.SeriesSource }, error) {
			ss, err := s.app.Series.UpdateSource(ctx, in.ID, in.LinkID, in.Body)
			return &struct{ Body *model.SeriesSource }{ss}, seriesError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-replace", Method: http.MethodPost, Path: "/api/v1/series/{id}/sources/{linkId}/replace", Tags: tags,
		Summary: "Swap a source link for another manga at the same priority (change a wrong match)"},
		func(ctx context.Context, in *struct {
			ID     int64 `path:"id"`
			LinkID int64 `path:"linkId"`
			Body   series.SourceLink
		}) (*struct{ Body *model.SeriesSource }, error) {
			ss, err := s.app.Series.ReplaceSource(ctx, in.ID, in.LinkID, in.Body)
			if err != nil {
				if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "duplicate") {
					return nil, huma.Error409Conflict("source already linked")
				}
				return nil, seriesError(err)
			}
			return &struct{ Body *model.SeriesSource }{ss}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-order", Method: http.MethodPut, Path: "/api/v1/series/{id}/sources/order", Tags: tags,
		Summary: "Set the series' own source order in one step (switches it to a custom order)"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				LinkIDs []int64 `json:"linkIds" minItems:"1"`
			}
		}) (*struct{}, error) {
			return nil, seriesError(s.app.Series.ReorderSources(ctx, in.ID, in.Body.LinkIDs))
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-source-unlink", Method: http.MethodDelete, Path: "/api/v1/series/{id}/sources/{linkId}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID     int64 `path:"id"`
			LinkID int64 `path:"linkId"`
		}) (*struct{}, error) {
			return nil, seriesError(s.app.Series.UnlinkSource(ctx, in.ID, in.LinkID))
		})

	huma.Register(s.api, huma.Operation{OperationID: "series-cover", Method: http.MethodGet, Path: "/api/v1/series/{id}/cover", Tags: tags},
		func(ctx context.Context, in *struct {
			ID   int64  `path:"id"`
			V    string `query:"v"`
			Size string `query:"size" enum:",full" doc:"full = the library's cover.jpg as is (default: a resized copy)"`
		}) (*imageOutput, error) {
			ser, err := s.visibleSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if in.Size == "full" {
				if p := s.app.Library.CoverPath(ctx, ser); p != "" {
					if data, err := os.ReadFile(p); err == nil {
						return &imageOutput{ContentType: http.DetectContentType(data), CacheControl: "public, max-age=3600", Body: data}, nil
					}
				}
			}
			data, ct, err := s.app.Reading.Cover(ctx, ser)
			if err != nil {
				return nil, huma.Error404NotFound("no cover")
			}
			return &imageOutput{ContentType: ct, CacheControl: "public, max-age=3600", Body: data}, nil
		})
}
