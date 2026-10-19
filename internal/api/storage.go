package api

import (
	"context"
	"net/http"
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerStorage) }

// StorageLibrary sums the chapter files of the library's series.
type StorageLibrary struct {
	Bytes  int64 `json:"bytes"`
	Series int   `json:"series"`
	Files  int   `json:"files"`
	Pages  int64 `json:"pages"`
	// Saved is how much smaller processing made the files.
	Saved int64 `json:"saved"`
}

// StorageRoot is one root folder: what the library keeps there and the
// drive it is on.
type StorageRoot struct {
	ID       int64  `json:"id"`
	Path     string `json:"path"`
	Language string `json:"language"`
	Series   int    `json:"series"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"bytes"`
	// DriveTotal and DriveFree are 0 when the folder can't be read (Error).
	DriveTotal uint64 `json:"driveTotal"`
	DriveFree  uint64 `json:"driveFree"`
	// Drive is the same for root folders on one drive.
	Drive string `json:"drive"`
	Error string `json:"error,omitempty"`
}

// StorageSeries is one of the largest series.
type StorageSeries struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Language string `json:"language"`
	Files    int    `json:"files"`
	Bytes    int64  `json:"bytes"`
}

// StorageStatus sums the series with one status.
type StorageStatus struct {
	Status string `json:"status"`
	Series int    `json:"series"`
	Bytes  int64  `json:"bytes"`
}

// StorageSummary is how much space the library takes and where.
type StorageSummary struct {
	Library  StorageLibrary  `json:"library"`
	Roots    []StorageRoot   `json:"roots"`
	Largest  []StorageSeries `json:"largest"`
	ByStatus []StorageStatus `json:"byStatus"`
	// ReadFiles and ReadBytes are the chapter files already read (by the
	// caller, or by the readers counting for cleanup).
	ReadFiles     int   `json:"readFiles"`
	ReadBytes     int64 `json:"readBytes"`
	RecycleFiles  int   `json:"recycleFiles"`
	RecycleBytes  int64 `json:"recycleBytes"`
	RecycleDays   int   `json:"recycleDays"`
	ImageBytes    int64 `json:"imageBytes"`
	ImageMaxBytes int64 `json:"imageMaxBytes"`
}

// largestSeries is how many series the summary lists by size.
const largestSeries = 8

func (s *Server) registerStorage() {
	huma.Register(s.api, huma.Operation{OperationID: "system-storage", Method: http.MethodGet, Path: "/api/v1/system/storage", Tags: []string{"System"},
		Summary: "How much space the library takes: totals, root folders and their drives, the largest series"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body StorageSummary }, error) {
			out, err := s.storageSummary(ctx)
			return &struct{ Body StorageSummary }{out}, toHTTPError(err)
		})
}

func (s *Server) storageSummary(ctx context.Context) (StorageSummary, error) {
	out := StorageSummary{Roots: []StorageRoot{}, Largest: []StorageSeries{}, ByStatus: []StorageStatus{}}
	files := s.app.DB.NewSelect().TableExpr("chapter_files").
		ColumnExpr("series_id, COUNT(*) AS files, SUM(size) AS size, SUM(page_count) AS pages").
		ColumnExpr("SUM(CASE WHEN size_original > size THEN size_original - size ELSE 0 END) AS saved").
		GroupExpr("series_id")
	var rows []struct {
		ID           int64  `bun:"id"`
		Title        string `bun:"title"`
		Language     string `bun:"language"`
		Status       string `bun:"status"`
		RootFolderID int64  `bun:"root_folder_id"`
		Files        int    `bun:"files"`
		Size         int64  `bun:"size"`
		Pages        int64  `bun:"pages"`
		Saved        int64  `bun:"saved"`
	}
	err := s.app.DB.NewSelect().TableExpr("series AS s").
		ColumnExpr("s.id, s.title, s.language, s.status, s.root_folder_id").
		ColumnExpr("COALESCE(f.files, 0) AS files, COALESCE(f.size, 0) AS size, COALESCE(f.pages, 0) AS pages, COALESCE(f.saved, 0) AS saved").
		Join("LEFT JOIN (?) AS f ON f.series_id = s.id", files).
		Where("s.preview = ?", false).Scan(ctx, &rows)
	if err != nil {
		return out, err
	}
	type sum struct {
		series, files int
		bytes         int64
	}
	byRoot, byStatus := map[int64]sum{}, map[string]sum{}
	for _, r := range rows {
		lib := &out.Library
		lib.Series++
		lib.Files += r.Files
		lib.Bytes += r.Size
		lib.Pages += r.Pages
		lib.Saved += r.Saved
		rs := byRoot[r.RootFolderID]
		rs.series, rs.files, rs.bytes = rs.series+1, rs.files+r.Files, rs.bytes+r.Size
		byRoot[r.RootFolderID] = rs
		st := r.Status
		if st == "" {
			st = model.StatusUnknown
		}
		ss := byStatus[st]
		ss.series, ss.bytes = ss.series+1, ss.bytes+r.Size
		byStatus[st] = ss
		if r.Size > 0 {
			out.Largest = append(out.Largest, StorageSeries{ID: r.ID, Title: r.Title, Language: r.Language, Files: r.Files, Bytes: r.Size})
		}
	}
	sort.Slice(out.Largest, func(i, j int) bool {
		a, b := out.Largest[i], out.Largest[j]
		return a.Bytes > b.Bytes || a.Bytes == b.Bytes && a.ID < b.ID
	})
	out.Largest = out.Largest[:min(len(out.Largest), largestSeries)]
	for _, st := range []string{model.StatusOngoing, model.StatusCompleted, model.StatusHiatus, model.StatusCancelled, model.StatusUnknown} {
		if ss, ok := byStatus[st]; ok {
			out.ByStatus = append(out.ByStatus, StorageStatus{Status: st, Series: ss.series, Bytes: ss.bytes})
			delete(byStatus, st)
		}
	}
	for st, ss := range byStatus { // a status this list doesn't know yet
		out.ByStatus = append(out.ByStatus, StorageStatus{Status: st, Series: ss.series, Bytes: ss.bytes})
	}

	var roots []model.RootFolder
	if err := s.app.DB.NewSelect().Model(&roots).Order("path").Scan(ctx); err != nil {
		return out, err
	}
	for _, r := range roots {
		rs := byRoot[r.ID]
		sr := StorageRoot{ID: r.ID, Path: r.Path, Language: r.Language, Series: rs.series, Files: rs.files, Bytes: rs.bytes}
		if total, free, drive, err := fsutil.Usage(r.Path); err != nil {
			sr.Error = err.Error()
		} else {
			sr.DriveTotal, sr.DriveFree, sr.Drive = total, free, drive
		}
		out.Roots = append(out.Roots, sr)
	}

	if readers := s.statsReaders(ctx); len(readers) > 0 {
		var read struct {
			Files int   `bun:"files"`
			Size  int64 `bun:"size"`
		}
		readChapters := s.app.DB.NewSelect().TableExpr("chapter_read_states").ColumnExpr("chapter_id").
			Where("completed = ?", true).Where("reader_id IN (?)", bun.In(readers))
		err := s.app.DB.NewSelect().TableExpr("chapter_files AS cf").
			ColumnExpr("COUNT(*) AS files, COALESCE(SUM(cf.size), 0) AS size").
			Where("cf.series_id IN (SELECT id FROM series WHERE preview = ?)", false).
			Where("cf.chapter_id IN (?)", readChapters).Scan(ctx, &read)
		if err != nil {
			return out, err
		}
		out.ReadFiles, out.ReadBytes = read.Files, read.Size
	}

	var bin struct {
		Files int   `bun:"files"`
		Size  int64 `bun:"size"`
	}
	if err := s.app.DB.NewSelect().Model((*model.RecycledFile)(nil)).
		ColumnExpr("COUNT(*) AS files, COALESCE(SUM(size), 0) AS size").Scan(ctx, &bin); err != nil {
		return out, err
	}
	out.RecycleFiles, out.RecycleBytes = bin.Files, bin.Size
	if mm, err := s.app.Settings.MediaManagement(ctx); err == nil {
		out.RecycleDays = mm.RecycleBinDays
	}
	out.ImageBytes, out.ImageMaxBytes = s.app.ImageCache.Size(), s.imageCap(ctx)
	return out, nil
}
