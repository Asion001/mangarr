// Package library manages the on-disk library: series folders, chapter file
// names, cover.jpg / series.json sidecars, the recycle bin and disk scans.
package library

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Asion001/mangarr/internal/comicinfo"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/naming"
	"github.com/Asion001/mangarr/internal/settings"
)

type Library struct {
	db        *db.DB
	settings  *settings.Store
	http      *http.Client
	dataDir   string
	log       *slog.Logger
	recycleMu sync.Mutex
}

func New(d *db.DB, s *settings.Store, hc *http.Client, dataDir string, log *slog.Logger) *Library {
	return &Library{db: d, settings: s, http: hc, dataDir: dataDir, log: log}
}

// RootFolder loads a root folder.
func (l *Library) RootFolder(ctx context.Context, id int64) (*model.RootFolder, error) {
	var rf model.RootFolder
	if err := l.db.NewSelect().Model(&rf).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, fmt.Errorf("root folder %d: %w", id, err)
	}
	return &rf, nil
}

// SeriesDir returns the absolute folder of a series.
func (l *Library) SeriesDir(ctx context.Context, s *model.Series) (string, error) {
	rf, err := l.RootFolder(ctx, s.RootFolderID)
	if err != nil {
		return "", err
	}
	return filepath.Join(rf.Path, s.Path), nil
}

// FolderName renders the folder name for a new series.
func (l *Library) FolderName(ctx context.Context, title string, year int) string {
	mm, _ := l.settings.MediaManagement(ctx)
	return naming.Render(mm.SeriesFolderFormat, naming.Values{SeriesTitle: title, SeriesYear: year})
}

// UniqueFolder picks a folder name not used by another series in the root folder.
func (l *Library) UniqueFolder(ctx context.Context, rootID int64, name string) (string, error) {
	candidate := name
	for i := 2; i < 100; i++ {
		n, err := l.db.NewSelect().Model((*model.Series)(nil)).Where("root_folder_id = ? AND path = ?", rootID, candidate).Count(ctx)
		if err != nil {
			return "", err
		}
		if n == 0 {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s (%d)", name, i)
	}
	return "", errors.New("could not find a free folder name")
}

// ChapterFileName renders the CBZ file name (without directory) for a chapter.
func (l *Library) ChapterFileName(ctx context.Context, s *model.Series, ch *model.Chapter, rel *model.ChapterRelease, sourceName string) string {
	mm, _ := l.settings.MediaManagement(ctx)
	v := naming.Values{SeriesTitle: s.Title, SeriesYear: s.Metadata.Year, Chapter: ch.NumberSort, HasChapter: true,
		Volume: ch.Volume, ChapterTitle: ch.Title, Language: s.Language, Source: sourceName}
	if rel != nil {
		v.Scanlator = rel.Scanlator
	}
	return naming.Render(mm.ChapterFormat, v) + ".cbz"
}

// Modes returns the configured file and directory modes.
func (l *Library) Modes(ctx context.Context) (file, dir os.FileMode) {
	mm, _ := l.settings.MediaManagement(ctx)
	return fsutil.ParseMode(mm.FileMode, 0o664), fsutil.ParseMode(mm.DirMode, 0o775)
}

// EnsureSeriesDir creates the series folder.
func (l *Library) EnsureSeriesDir(ctx context.Context, s *model.Series) (string, error) {
	dir, err := l.SeriesDir(ctx, s)
	if err != nil {
		return "", err
	}
	_, dmode := l.Modes(ctx)
	if err := os.MkdirAll(dir, dmode); err != nil {
		return "", err
	}
	return dir, nil
}

// WriteSidecars writes series.json and cover.jpg according to settings.
func (l *Library) WriteSidecars(ctx context.Context, s *model.Series, fallbackCover func(ctx context.Context) (io.ReadCloser, error)) error {
	mm, err := l.settings.MediaManagement(ctx)
	if err != nil {
		return err
	}
	dir, err := l.EnsureSeriesDir(ctx, s)
	if err != nil {
		return err
	}
	fmode, _ := l.Modes(ctx)
	if mm.WriteSeriesJSON {
		id := "mangarr:" + fmt.Sprint(s.ID)
		if v := s.Metadata.ExternalIDs["anilist"]; v != "" {
			id = "anilist:" + v
		}
		b, err := comicinfo.BuildSeriesJSON(comicinfo.SeriesInput{
			Title: s.Title, ID: id, Publisher: s.Metadata.Publisher, Year: s.Metadata.Year, Description: s.Metadata.Description,
			CoverURL: s.Metadata.CoverURL, TotalCount: s.Metadata.TotalChapters,
			Ended:     s.Status == model.StatusCompleted || s.Status == model.StatusCancelled,
			AgeRating: s.Metadata.AgeRating,
		})
		if err == nil {
			err = writeAtomic(filepath.Join(dir, "series.json"), b, fmode)
		}
		if err != nil {
			l.log.Warn("write series.json", "series", s.Title, "err", err)
		}
	}
	if mm.WriteCover {
		coverPath := filepath.Join(dir, "cover.jpg")
		if _, err := os.Stat(coverPath); errors.Is(err, os.ErrNotExist) {
			if err := l.downloadCover(ctx, s, coverPath, fmode, fallbackCover); err != nil {
				l.log.Warn("write cover", "series", s.Title, "err", err)
			}
		}
	}
	return nil
}

// RefreshCover replaces cover.jpg (e.g. after a metadata change).
func (l *Library) RefreshCover(ctx context.Context, s *model.Series, fallback func(ctx context.Context) (io.ReadCloser, error)) error {
	dir, err := l.SeriesDir(ctx, s)
	if err != nil {
		return err
	}
	fmode, _ := l.Modes(ctx)
	return l.downloadCover(ctx, s, filepath.Join(dir, "cover.jpg"), fmode, fallback)
}

func (l *Library) downloadCover(ctx context.Context, s *model.Series, dst string, mode os.FileMode, fallback func(ctx context.Context) (io.ReadCloser, error)) error {
	var body io.ReadCloser
	if s.Metadata.CoverURL != "" && strings.HasPrefix(s.Metadata.CoverURL, "http") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.Metadata.CoverURL, nil)
		if err == nil {
			if resp, err := l.http.Do(req); err == nil {
				if resp.StatusCode == 200 {
					body = resp.Body
				} else {
					resp.Body.Close()
				}
			}
		}
	}
	if body == nil && fallback != nil {
		b, err := fallback(ctx)
		if err != nil {
			return err
		}
		body = b
	}
	if body == nil {
		return errors.New("no cover available")
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, 20<<20))
	if err != nil {
		return err
	}
	// Komga/Kavita only look at the name; the content may be PNG/WebP too,
	// but convert nothing here: both servers sniff the real format.
	return writeAtomic(dst, data, mode)
}

// CoverPath returns the cover file of a series if it exists.
func (l *Library) CoverPath(ctx context.Context, s *model.Series) string {
	dir, err := l.SeriesDir(ctx, s)
	if err != nil {
		return ""
	}
	p := filepath.Join(dir, "cover.jpg")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return ""
}

func writeAtomic(dst string, data []byte, mode os.FileMode) error {
	tmp := dst + ".partial"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// ---- recycle bin -----------------------------------------------------------------

// RecycleDir returns the recycle bin path.
func (l *Library) RecycleDir(ctx context.Context) string {
	mm, _ := l.settings.MediaManagement(ctx)
	if mm.RecycleBinPath != "" {
		return mm.RecycleBinPath
	}
	return filepath.Join(l.dataDir, "recycle")
}

func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() && p != root {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // only succeeds when empty
	}
}

// seriesLocks serialize changes to a series folder: imports, sidecars and
// cleanup take a read lock; moves and renames take the write lock.
var seriesLocks sync.Map // series id -> *sync.RWMutex

func seriesLock(id int64) *sync.RWMutex {
	v, _ := seriesLocks.LoadOrStore(id, &sync.RWMutex{})
	return v.(*sync.RWMutex)
}

// RLockSeries is held while writing files into a series folder.
func RLockSeries(id int64) (unlock func()) {
	l := seriesLock(id)
	l.RLock()
	return l.RUnlock
}

// LockSeries is held while moving or renaming a series folder.
func LockSeries(id int64) (unlock func()) {
	l := seriesLock(id)
	l.Lock()
	return l.Unlock
}
