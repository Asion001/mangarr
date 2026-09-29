package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/model"
)

type ScanResult struct {
	Series  int `json:"series"`
	Checked int `json:"checked"`
	Missing int `json:"missing"`
}

// DiskScan verifies that every tracked chapter file still exists. Missing
// files are untracked and their chapters become missing again (and will be
// re-downloaded if monitored); cleaned chapters are left alone.
func (l *Library) DiskScan(ctx context.Context, seriesID int64) (ScanResult, error) {
	var res ScanResult
	var list []model.Series
	q := l.db.NewSelect().Model(&list).Where("preview = ?", false) // previews have no files
	if seriesID > 0 {
		q = q.Where("id = ?", seriesID)
	}
	if err := q.Scan(ctx); err != nil {
		return res, err
	}
	for i := range list {
		s := &list[i]
		dir, err := l.SeriesDir(ctx, s)
		if err != nil {
			continue
		}
		// An unreachable root (unmounted share) must not untrack everything.
		rf, err := l.RootFolder(ctx, s.RootFolderID)
		if err != nil {
			continue
		}
		if _, err := os.Stat(rf.Path); err != nil {
			l.log.Warn("disk scan: root folder not accessible, skipping", "root", rf.Path, "err", err)
			continue
		}
		res.Series++
		var files []model.ChapterFile
		if err := l.db.NewSelect().Model(&files).Where("series_id = ?", s.ID).Scan(ctx); err != nil {
			return res, err
		}
		for _, f := range files {
			res.Checked++
			if _, err := os.Stat(filepath.Join(dir, f.RelativePath)); !errors.Is(err, os.ErrNotExist) {
				continue
			}
			res.Missing++
			err := l.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
				if _, err := tx.NewDelete().Model((*model.ChapterFile)(nil)).Where("id = ?", f.ID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = NULL").
					Set("state = CASE WHEN state = ? THEN state ELSE ? END", model.ChapterCleaned, model.ChapterMissing).
					Set("updated_at = ?", time.Now().UTC()).Where("id = ?", f.ChapterID).Exec(ctx); err != nil {
					return err
				}
				chID := f.ChapterID
				return history.Record(ctx, tx, s.ID, &chID, model.HistoryDeleted, f.RelativePath, map[string]string{"reason": "file missing on disk"})
			})
			if err != nil {
				return res, err
			}
		}
	}
	return res, nil
}
