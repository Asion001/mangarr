// Package organize moves and renames series folders and chapter files:
// moving a series to another root folder (or fixing its path), renaming
// files after the naming format changed, and renaming folders after a title
// change. File operations hold the series write lock so imports, sidecars
// and cleanup wait; cross-device moves copy, verify and only then delete.
package organize

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/naming"
)

// rename is os.Rename (replaced in tests to simulate another file system).
var rename = os.Rename

// MarkerName is written into a folder while it's being copied; a leftover
// marker means the move was interrupted (see ResumeMoves).
const MarkerName = ".mangarr-move"

type Service struct {
	db  *db.DB
	lib *library.Library
	bus *events.Bus
	log *slog.Logger
	// AfterChange (optional) is told about folders whose contents changed
	// (library rescans) and series whose files moved (progress restore).
	AfterChange func(seriesID int64, dirs []string, moved bool)
}

func New(d *db.DB, lib *library.Library, bus *events.Bus, log *slog.Logger) *Service {
	return &Service{db: d, lib: lib, bus: bus, log: log}
}

var ErrExists = errors.New("the destination folder already exists and isn't empty")

// cleanRel validates a series path relative to its root folder.
func cleanRel(p string) (string, error) {
	p = filepath.Clean(strings.TrimSpace(p))
	if p == "." || p == "" || filepath.IsAbs(p) || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid series folder %q", p)
	}
	return p, nil
}

// under reports whether p is inside (or equal to) dir.
func under(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func emptyOrMissing(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err != nil || len(entries) == 0
}

// MoveRequest moves a series to a root folder and/or folder name.
type MoveRequest struct {
	SeriesID     int64  `json:"seriesId"`
	RootFolderID int64  `json:"rootFolderId,omitempty"` // 0 = same root
	Path         string `json:"path,omitempty"`         // "" = same folder name
	// MoveFiles moves the folder on disk; false only updates mangarr
	// (the files were already moved by hand).
	MoveFiles bool `json:"moveFiles"`
}

// Move relocates a series.
func (s *Service) Move(ctx context.Context, req MoveRequest) error {
	var ser model.Series
	if err := s.db.NewSelect().Model(&ser).Where("id = ?", req.SeriesID).Scan(ctx); err != nil {
		return fmt.Errorf("series %d: %w", req.SeriesID, err)
	}
	oldRoot, err := s.lib.RootFolder(ctx, ser.RootFolderID)
	if err != nil {
		return err
	}
	newRoot := oldRoot
	if req.RootFolderID != 0 && req.RootFolderID != ser.RootFolderID {
		if newRoot, err = s.lib.RootFolder(ctx, req.RootFolderID); err != nil {
			return err
		}
	}
	rel := ser.Path
	if req.Path != "" {
		if rel, err = cleanRel(req.Path); err != nil {
			return err
		}
	}
	oldDir, newDir := filepath.Join(oldRoot.Path, ser.Path), filepath.Join(newRoot.Path, rel)
	if oldDir == newDir {
		return nil
	}
	if under(newDir, oldDir) || under(oldDir, newDir) {
		return fmt.Errorf("can't move %s into %s", oldDir, newDir)
	}
	if n, _ := s.db.NewSelect().Model((*model.Series)(nil)).Where("root_folder_id = ? AND path = ? AND id <> ?", newRoot.ID, rel, ser.ID).Count(ctx); n > 0 {
		return fmt.Errorf("another series already uses %s", newDir)
	}

	unlock := library.LockSeries(ser.ID)
	defer unlock()
	copied := false
	if req.MoveFiles {
		if !emptyOrMissing(newDir) {
			return fmt.Errorf("%w: %s", ErrExists, newDir)
		}
		if _, err := os.Stat(oldDir); err == nil {
			_ = os.Remove(newDir) // empty
			_, dmode := s.lib.Modes(ctx)
			if err := os.MkdirAll(filepath.Dir(newDir), dmode); err != nil {
				return err
			}
			if err := rename(oldDir, newDir); err != nil {
				// another file system: copy, verify, then delete
				s.log.Info("copying series to another file system", "from", oldDir, "to", newDir)
				if err := s.copyVerified(ctx, ser.ID, oldDir, newDir); err != nil {
					_ = os.RemoveAll(newDir)
					return err
				}
				copied = true
			}
		}
	}
	now := time.Now().UTC()
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model((*model.Series)(nil)).Set("root_folder_id = ?", newRoot.ID).Set("path = ?", rel).
			Set("updated_at = ?", now).Where("id = ?", ser.ID).Exec(ctx); err != nil {
			return err
		}
		return history.Record(ctx, tx, ser.ID, nil, model.HistoryMoved, "", map[string]string{"from": oldDir, "to": newDir, "files": fmt.Sprint(req.MoveFiles)})
	})
	if err != nil {
		if copied {
			_ = os.RemoveAll(newDir) // the old copy is still complete
		}
		return err
	}
	if copied {
		if err := os.RemoveAll(oldDir); err != nil {
			s.log.Warn("remove old series folder after copy", "dir", oldDir, "err", err)
		}
		_ = os.Remove(filepath.Join(newDir, MarkerName))
	}
	s.log.Info("series moved", "series", ser.Title, "from", oldDir, "to", newDir, "files", req.MoveFiles)
	s.bus.Changed("series", "updated", ser.ID)
	if s.AfterChange != nil {
		s.AfterChange(ser.ID, []string{oldDir, newDir}, req.MoveFiles)
	}
	return nil
}

// copyVerified copies oldDir to newDir and checks every chapter file's size.
func (s *Service) copyVerified(ctx context.Context, seriesID int64, oldDir, newDir string) error {
	if err := fsutil.CopyTree(oldDir, newDir); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(newDir, MarkerName), []byte(oldDir), 0o664); err != nil {
		return err
	}
	var files []model.ChapterFile
	if err := s.db.NewSelect().Model(&files).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return err
	}
	for _, f := range files {
		src, errA := os.Stat(filepath.Join(oldDir, f.RelativePath))
		dst, errB := os.Stat(filepath.Join(newDir, f.RelativePath))
		if errA != nil {
			continue // missing before the move; the disk scan will notice
		}
		if errB != nil || dst.Size() != src.Size() {
			return fmt.Errorf("verifying copy of %s failed", f.RelativePath)
		}
	}
	return nil
}

// ResumeMoves finishes cross-device moves interrupted by a crash: a marker
// in the destination tells where the copy came from. If mangarr already
// points at the destination the old folder is removed, otherwise the
// partial copy is.
func (s *Service) ResumeMoves(ctx context.Context) error {
	var roots []model.RootFolder
	if err := s.db.NewSelect().Model(&roots).Scan(ctx); err != nil {
		return err
	}
	for _, rf := range roots {
		markers, _ := filepath.Glob(filepath.Join(rf.Path, "*", MarkerName))
		for _, m := range markers {
			dir := filepath.Dir(m)
			src, _ := os.ReadFile(m)
			n, _ := s.db.NewSelect().Model((*model.Series)(nil)).Where("root_folder_id = ? AND path = ?", rf.ID, filepath.Base(dir)).Count(ctx)
			if n > 0 {
				if old := filepath.Clean(strings.TrimSpace(string(src))); old != "." && old != dir && inAnyRoot(old, roots) {
					_ = os.RemoveAll(old)
				}
				_ = os.Remove(m)
				s.log.Info("finished an interrupted series move", "dir", dir)
			} else {
				_ = os.RemoveAll(dir)
				s.log.Warn("removed the partial copy of an interrupted series move", "dir", dir)
			}
		}
	}
	return nil
}

// inAnyRoot reports whether p is a folder strictly inside one of the roots.
func inAnyRoot(p string, roots []model.RootFolder) bool {
	for _, rf := range roots {
		if under(p, rf.Path) && filepath.Clean(p) != filepath.Clean(rf.Path) {
			return true
		}
	}
	return false
}

// FileRename is one planned rename.
type FileRename struct {
	ChapterID int64  `json:"chapterId"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// SeriesRename is the rename plan of one series.
type SeriesRename struct {
	SeriesID   int64        `json:"seriesId"`
	Title      string       `json:"title"`
	FolderFrom string       `json:"folderFrom"`
	FolderTo   string       `json:"folderTo,omitempty"` // "" = unchanged
	Files      []FileRename `json:"files"`
}

// Preview lists the renames needed for the current naming settings.
func (s *Service) Preview(ctx context.Context, seriesIDs []int64, folders bool) ([]SeriesRename, error) {
	var list []model.Series
	if err := s.db.NewSelect().Model(&list).Where("id IN (?)", bun.List(seriesIDs)).Order("sort_title").Scan(ctx); err != nil {
		return nil, err
	}
	out := []SeriesRename{}
	for i := range list {
		ser := &list[i]
		r := SeriesRename{SeriesID: ser.ID, Title: ser.Title, FolderFrom: ser.Path, Files: []FileRename{}}
		if folders {
			want := naming.Sanitize(s.lib.FolderName(ctx, ser.Title, ser.Metadata.Year))
			if want != "" && want != ser.Path {
				r.FolderTo = want
			}
		}
		var files []model.ChapterFile
		if err := s.db.NewSelect().Model(&files).Where("series_id = ?", ser.ID).Scan(ctx); err != nil {
			return nil, err
		}
		var chapters []model.Chapter
		_ = s.db.NewSelect().Model(&chapters).Where("series_id = ?", ser.ID).Scan(ctx)
		byID := map[int64]*model.Chapter{}
		for j := range chapters {
			byID[chapters[j].ID] = &chapters[j]
		}
		taken := map[string]bool{}
		for _, f := range files {
			taken[f.RelativePath] = true
		}
		for _, f := range files {
			ch := byID[f.ChapterID]
			if ch == nil {
				continue
			}
			var rel *model.ChapterRelease
			if f.ReleaseID != nil {
				var cr model.ChapterRelease
				if s.db.NewSelect().Model(&cr).Where("id = ?", *f.ReleaseID).Scan(ctx) == nil {
					rel = &cr
				}
			}
			if rel == nil && f.Scanlator != "" {
				rel = &model.ChapterRelease{Scanlator: f.Scanlator}
			}
			want := s.lib.ChapterFileName(ctx, ser, ch, rel, f.SourceName)
			if want == f.RelativePath || taken[want] {
				continue
			}
			taken[want] = true
			r.Files = append(r.Files, FileRename{ChapterID: f.ChapterID, From: f.RelativePath, To: want})
		}
		if r.FolderTo != "" || len(r.Files) > 0 {
			out = append(out, r)
		}
	}
	return out, nil
}

// Rename applies the preview of one series (files, then the folder).
func (s *Service) Rename(ctx context.Context, seriesID int64, folders bool) (int, error) {
	plans, err := s.Preview(ctx, []int64{seriesID}, folders)
	if err != nil || len(plans) == 0 {
		return 0, err
	}
	plan := plans[0]
	var ser model.Series
	if err := s.db.NewSelect().Model(&ser).Where("id = ?", seriesID).Scan(ctx); err != nil {
		return 0, err
	}
	dir, err := s.lib.SeriesDir(ctx, &ser)
	if err != nil {
		return 0, err
	}
	n := 0
	if len(plan.Files) > 0 {
		unlock := library.LockSeries(seriesID)
		for _, f := range plan.Files {
			from, to := filepath.Join(dir, f.From), filepath.Join(dir, f.To)
			if _, err := os.Stat(to); err == nil {
				s.log.Warn("rename target exists, skipping", "file", to)
				continue
			}
			if err := os.Rename(from, to); err != nil {
				unlock()
				return n, fmt.Errorf("rename %s: %w", f.From, err)
			}
			if _, err := s.db.NewUpdate().Model((*model.ChapterFile)(nil)).Set("relative_path = ?", f.To).
				Where("series_id = ? AND chapter_id = ?", seriesID, f.ChapterID).Exec(ctx); err != nil {
				_ = os.Rename(to, from)
				unlock()
				return n, err
			}
			chID := f.ChapterID
			_ = history.Record(ctx, s.db, seriesID, &chID, model.HistoryRetitled, "", map[string]string{"from": f.From, "to": f.To})
			n++
		}
		unlock()
		s.bus.Changed("chapter", "updated", 0)
		if s.AfterChange != nil {
			s.AfterChange(seriesID, []string{dir}, true)
		}
	}
	if plan.FolderTo != "" {
		if err := s.Move(ctx, MoveRequest{SeriesID: seriesID, Path: plan.FolderTo, MoveFiles: true}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
