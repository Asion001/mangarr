package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/jobs"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/organize"
)

// RestoreRetries is how often RestoreProgress waits for a library server
// to pick up moved files (every RestoreRetryDelay).
const RestoreRetries = 20

var RestoreRetryDelay = 30 * time.Second

// wireOrganize registers moving, renaming and progress restoring.
func (a *App) wireOrganize(ctx context.Context) error {
	a.Organize = organize.New(a.DB, a.Library, a.Bus, a.Log.With("component", "organize"))
	a.Organize.AfterChange = func(seriesID int64, dirs []string, moved bool) {
		a.Rescanner.Queue(dirs...)
		if moved {
			_, _ = a.Queue.Push(context.Background(), "RestoreProgress", map[string]any{"seriesId": seriesID}, "files-moved")
		}
	}
	if err := a.Organize.ResumeMoves(ctx); err != nil {
		a.Log.Warn("resume interrupted moves", "err", err)
	}

	a.Queue.Register(jobs.Definition{Name: "MoveSeries", Description: "Move a series to another root folder or folder name",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var req organize.MoveRequest
			if err := r.Body(&req); err != nil || req.SeriesID == 0 {
				return fmt.Errorf("seriesId required")
			}
			if req.MoveFiles {
				a.snapshotProgress(ctx, req.SeriesID)
			}
			r.Progress("moving")
			if err := a.Organize.Move(ctx, req); err != nil {
				a.ReadSync.Unprotect(req.SeriesID)
				return err
			}
			r.Progress("moved")
			return nil
		}})

	a.Queue.Register(jobs.Definition{Name: "RenameFiles", Description: "Rename chapter files (and folders) to the current naming format",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesIDs []int64 `json:"seriesIds"`
				Folders   bool    `json:"folders"`
			}
			if err := r.Body(&body); err != nil || len(body.SeriesIDs) == 0 {
				return fmt.Errorf("seriesIds required")
			}
			total := 0
			for i, id := range body.SeriesIDs {
				a.snapshotProgress(ctx, id)
				n, err := a.Organize.Rename(ctx, id, body.Folders)
				if err != nil {
					a.ReadSync.Unprotect(id)
					return err
				}
				total += n
				r.Progress("renamed %d files (%d/%d series)", total, i+1, len(body.SeriesIDs))
			}
			return nil
		}})

	a.Queue.Register(jobs.Definition{Name: "RestoreProgress", Description: "Write readers' progress back to library servers after files moved",
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				SeriesID int64 `json:"seriesId"`
				Attempt  int   `json:"attempt"`
			}
			if err := r.Body(&body); err != nil || body.SeriesID == 0 {
				return fmt.Errorf("seriesId required")
			}
			a.ReadSync.Protect(body.SeriesID, RestoreRetryDelay*(RestoreRetries+2))
			res, err := a.ReadSync.RestoreSeries(ctx, body.SeriesID)
			r.Progress("restored %d, waiting for %d", res.Written, res.Missing)
			if res.Written > 0 {
				_ = history.Record(ctx, a.DB, body.SeriesID, nil, model.HistoryProgress, "", map[string]string{"written": fmt.Sprint(res.Written)})
			}
			if (res.Missing > 0 || err != nil) && body.Attempt < RestoreRetries {
				// the server hasn't scanned the new files yet: try again
				time.AfterFunc(RestoreRetryDelay, func() {
					_, _ = a.Queue.Push(context.Background(), "RestoreProgress", map[string]any{"seriesId": body.SeriesID, "attempt": body.Attempt + 1}, "retry")
				})
				return nil
			}
			a.ReadSync.Unprotect(body.SeriesID)
			return err
		}})

	a.Queue.Register(jobs.Definition{Name: "MoveRootFolder", Description: "Change a root folder's location (moving its series or not)", Exclusive: true,
		Handler: func(ctx context.Context, r *jobs.Run) error {
			var body struct {
				RootFolderID int64  `json:"rootFolderId"`
				Path         string `json:"path"`
				MoveFiles    bool   `json:"moveFiles"`
			}
			if err := r.Body(&body); err != nil || body.RootFolderID == 0 || body.Path == "" {
				return fmt.Errorf("rootFolderId and path required")
			}
			return a.moveRootFolder(ctx, r, body.RootFolderID, filepath.Clean(body.Path), body.MoveFiles)
		}})
	return nil
}

// snapshotProgress captures the latest reader progress before files move
// and keeps syncs from clearing it until it's restored.
func (a *App) snapshotProgress(ctx context.Context, seriesID int64) {
	n, _ := a.DB.NewSelect().Model((*model.ReaderAccount)(nil)).Count(ctx)
	if n == 0 {
		return
	}
	a.ReadSync.Protect(seriesID, time.Hour)
	if _, err := a.ReadSync.Sync(ctx); err != nil {
		a.Log.Warn("reader progress snapshot before moving files", "err", err)
	}
}

// moveRootFolder points a root folder at a new location. With moveFiles the
// series folders are moved there one by one (rolled back on failure).
func (a *App) moveRootFolder(ctx context.Context, r *jobs.Run, rootID int64, newPath string, moveFiles bool) error {
	rf, err := a.Library.RootFolder(ctx, rootID)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(newPath) {
		return fmt.Errorf("%s is not an absolute path", newPath)
	}
	if rf.ManagedBy != "" {
		return fmt.Errorf("this root folder is set by MANGARR_ROOT_FOLDERS")
	}
	var series []model.Series
	if err := a.DB.NewSelect().Model(&series).Where("root_folder_id = ? AND preview = ?", rootID, false).Scan(ctx); err != nil {
		return err
	}
	if moveFiles {
		_, dmode := a.Library.Modes(ctx)
		if err := os.MkdirAll(newPath, dmode); err != nil {
			return err
		}
		type done struct{ from, to string }
		var moved []done
		rollback := func() {
			for _, d := range moved {
				_ = fsutil.Move(d.to, d.from)
			}
		}
		for i, s := range series {
			a.snapshotProgress(ctx, s.ID)
			from, to := filepath.Join(rf.Path, s.Path), filepath.Join(newPath, s.Path)
			if _, err := os.Stat(from); err != nil {
				continue
			}
			if _, err := os.Stat(to); err == nil {
				rollback()
				return fmt.Errorf("%s already exists", to)
			}
			unlock := library.LockSeries(s.ID)
			err := fsutil.Move(from, to) // renames, or copies across file systems
			unlock()
			if err != nil {
				rollback()
				return fmt.Errorf("move %s: %w", s.Title, err)
			}
			moved = append(moved, done{from, to})
			r.Progress("moved %d/%d series", i+1, len(series))
		}
	} else if st, err := os.Stat(newPath); err != nil || !st.IsDir() {
		return fmt.Errorf("%s doesn't exist (move the files first, or let mangarr move them)", newPath)
	}
	err = a.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		_, err := tx.NewUpdate().Model((*model.RootFolder)(nil)).Set("path = ?", newPath).Where("id = ?", rootID).Exec(ctx)
		return err
	})
	if err != nil {
		return err
	}
	a.Bus.Changed("rootfolder", "updated", rootID)
	for _, s := range series {
		_ = history.Record(ctx, a.DB, s.ID, nil, model.HistoryMoved, "", map[string]string{"from": filepath.Join(rf.Path, s.Path), "to": filepath.Join(newPath, s.Path), "files": fmt.Sprint(moveFiles)})
		a.Organize.AfterChange(s.ID, []string{filepath.Join(rf.Path, s.Path), filepath.Join(newPath, s.Path)}, moveFiles)
	}
	return nil
}
