package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/uptrace/bun"
)

// RestoreRecycled retains the chapter file's identity and compensates filesystem
// changes if its database transaction fails. The bin copy remains until commit.
func (l *Library) RestoreRecycled(ctx context.Context, id int64) (int64, error) {
	var r model.RecycledFile
	if err := l.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return 0, err
	}
	sid := int64(0)
	if r.SeriesID != nil {
		sid = *r.SeriesID
	} else if r.FolderSnapshot != nil {
		sid = r.FolderSnapshot.Series.ID
	}
	defer LockSeries(sid)()
	l.recycleMu.Lock()
	defer l.recycleMu.Unlock()
	if err := l.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return 0, err
	}
	busy, err := l.recycleInUse(ctx, &r, true)
	if err != nil {
		return 0, err
	}
	if busy {
		return 0, ErrRecycleConflict
	}
	source, err := safeRecyclePath(l.RecycleDir(ctx), r.RecycledPath)
	if err != nil {
		return 0, err
	}
	if r.Kind == "folder" {
		return l.restoreFolder(ctx, &r, source)
	}
	if r.SeriesID == nil || r.ChapterID == nil {
		return 0, fmt.Errorf("%w: unknown series or chapter", ErrRecycleConflict)
	}
	var series model.Series
	if err := l.db.NewSelect().Model(&series).Where("id = ?", *r.SeriesID).Scan(ctx); err != nil {
		return 0, err
	}
	dir, err := l.EnsureSeriesDir(ctx, &series)
	if err != nil {
		return 0, err
	}
	target, err := safeRecyclePath(dir, r.OriginalRelativePath)
	if err != nil {
		return 0, err
	}
	f := model.ChapterFile{ChapterID: *r.ChapterID, SeriesID: series.ID, ImportedAt: time.Now().UTC()}
	if r.FileSnapshot != nil {
		f = *r.FileSnapshot
		f.ChapterID, f.SeriesID = *r.ChapterID, series.ID
	}
	f.ID, f.RelativePath, f.ProcessParams = 0, r.OriginalRelativePath, r.ProcessParams
	f.ProcessError, f.ProcessAttempts, f.ProcessRetryAt = "", 0, nil
	if err := measureFile(source, &f); err != nil {
		return 0, err
	}
	if f.ReleaseID != nil {
		exists, err := l.db.NewSelect().Model((*model.ChapterRelease)(nil)).Where("id = ? AND chapter_id = ?", *f.ReleaseID, f.ChapterID).Exists(ctx)
		if err != nil {
			return 0, err
		}
		if !exists {
			f.ReleaseID = nil
		}
	}
	var old model.ChapterFile
	err = l.db.NewSelect().Model(&old).Where("chapter_id = ?", f.ChapterID).Scan(ctx)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if err == nil {
		f.ID = old.ID
		if old.RelativePath != f.RelativePath {
			return 0, fmt.Errorf("%w: chapter path changed since recycling", ErrRecycleConflict)
		}
	}
	backup := ""
	if _, err := os.Stat(target); err == nil {
		backup, err = l.recycle(ctx, target, series.Path, true, RecycleInfo{Series: &series, File: &old, Reason: "restored_over"})
		if err != nil {
			return 0, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, err
	}
	tmp := target + ".restore-partial"
	defer os.Remove(tmp)
	if err := fsutil.LinkOrCopy(source, tmp); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, target); err != nil {
		return 0, err
	}
	err = l.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if f.ID == 0 {
			if _, err := tx.NewInsert().Model(&f).Exec(ctx); err != nil {
				return err
			}
		} else if _, err := tx.NewUpdate().Model(&f).WherePK().Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = ?", f.ID).Set("state = ?", model.ChapterImported).Set("cleaned_at = NULL").Set("updated_at = ?", time.Now().UTC()).Where("id = ?", f.ChapterID).Exec(ctx); err != nil {
			return err
		}
		if err := RemapProgress(ctx, tx, f.ChapterID, &old, &f); err != nil {
			return err
		}
		if err := history.Record(ctx, tx, series.ID, &f.ChapterID, model.HistoryRestored, f.RelativePath, map[string]string{"recycledId": fmt.Sprint(r.ID)}); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model(&r).WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		if backup != "" {
			undo := fsutil.LinkOrCopy(backup, target)
			return 0, errors.Join(err, undo)
		}
		return 0, errors.Join(err, os.Remove(target))
	}
	if err := os.Remove(source); err != nil {
		return series.ID, err
	}
	return series.ID, nil
}

func measureFile(path string, f *model.ChapterFile) error {
	r := model.RecycledFile{Kind: "file"}
	if err := inspectRecycled(path, &r); err != nil {
		return err
	}
	f.Size, f.SHA256, f.PageCount = r.Size, r.SHA256, r.PageCount
	entries, err := cbz.List(path)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return errors.New("archive contains no pages")
	}
	widths := 0
	formats := map[string]int{}
	for _, entry := range entries {
		data, err := cbz.ReadEntry(path, entry.Path)
		if err != nil {
			return err
		}
		info, err := imagecheck.Detect(data)
		if err != nil {
			return err
		}
		widths += info.Width
		formats[info.Format]++
	}
	f.AvgWidth = widths / len(entries)
	f.Format = ""
	for format, count := range formats {
		if count > formats[f.Format] {
			f.Format = format
		}
	}
	return nil
}

// RemapProgress maps split segments through their original page. Legacy files
// without mappings use proportional position; completed readers stay completed.
func RemapProgress(ctx context.Context, tx bun.IDB, chapterID int64, old, next *model.ChapterFile) error {
	var states []model.ChapterReadState
	if err := tx.NewSelect().Model(&states).Where("chapter_id = ?", chapterID).Scan(ctx); err != nil {
		return err
	}
	for i := range states {
		page := RemapVersionPage(states[i].Page, states[i].Completed, old, next)
		if page != states[i].Page {
			if _, err := tx.NewUpdate().Model(&states[i]).Set("page = ?", page).WherePK().Exec(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func RemapVersionPage(page int, completed bool, old, next *model.ChapterFile) int {
	if page <= 0 {
		return page
	}
	if completed {
		return next.PageCount
	}
	if len(old.SourcePages) > 0 || len(next.SourcePages) > 0 {
		source := page - 1
		if len(old.SourcePages) > 0 {
			source = old.SourcePages[min(source, len(old.SourcePages)-1)]
		}
		if len(next.SourcePages) == 0 {
			return min(source+1, next.PageCount)
		}
		for i, original := range next.SourcePages {
			if original >= source {
				return i + 1
			}
		}
		return next.PageCount
	}
	if old.PageCount > 0 && old.PageCount != next.PageCount {
		return min(1+(page-1)*next.PageCount/old.PageCount, next.PageCount)
	}
	return min(page, next.PageCount)
}

func (l *Library) restoreFolder(ctx context.Context, r *model.RecycledFile, source string) (int64, error) {
	var series model.Series
	if r.SeriesID != nil {
		if err := l.db.NewSelect().Model(&series).Where("id = ?", *r.SeriesID).Scan(ctx); err != nil {
			return 0, err
		}
	} else if r.FolderSnapshot != nil {
		series = r.FolderSnapshot.Series
		series.ID, series.WorkID = 0, 0
	} else {
		return 0, fmt.Errorf("%w: folder has no series metadata", ErrRecycleConflict)
	}
	dir, err := l.SeriesDir(ctx, &series)
	if err != nil {
		return 0, err
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("%w: series folder already exists", ErrRecycleConflict)
	}
	if err := fsutil.CopyTree(source, dir); err != nil {
		_ = os.RemoveAll(dir)
		return 0, err
	}
	err = l.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if series.ID == 0 {
			work := model.Work{Title: series.Title, SortTitle: series.SortTitle, Metadata: series.Metadata, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
			if _, err := tx.NewInsert().Model(&work).Exec(ctx); err != nil {
				return err
			}
			series.WorkID = work.ID
			if _, err := tx.NewInsert().Model(&series).Exec(ctx); err != nil {
				return err
			}
		}
		if snap := r.FolderSnapshot; snap != nil {
			byChapter := map[int64]model.ChapterFile{}
			for _, f := range snap.Files {
				byChapter[f.ChapterID] = f
			}
			for _, ch := range snap.Chapters {
				originalID := ch.ID
				ch.ID, ch.SeriesID, ch.FileID = 0, series.ID, nil
				ch.State = model.ChapterMissing
				var existing model.Chapter
				err := tx.NewSelect().Model(&existing).Where("series_id = ? AND number_key = ?", series.ID, ch.NumberKey).Scan(ctx)
				if err == nil {
					ch = existing
				} else if errors.Is(err, sql.ErrNoRows) {
					if _, err := tx.NewInsert().Model(&ch).Exec(ctx); err != nil {
						return err
					}
				} else {
					return err
				}
				if f, ok := byChapter[originalID]; ok {
					f.ProcessParams = snap.ProcessParams[f.ID]
					f.ID, f.ChapterID, f.SeriesID, f.ReleaseID = 0, ch.ID, series.ID, nil
					path, err := safeRecyclePath(dir, f.RelativePath)
					if err != nil {
						return err
					}
					if err := measureFile(path, &f); err != nil {
						return err
					}
					if _, err := tx.NewInsert().Model(&f).Exec(ctx); err != nil {
						return err
					}
					if _, err := tx.NewUpdate().Model(&ch).Set("file_id = ?", f.ID).Set("state = ?", model.ChapterImported).WherePK().Exec(ctx); err != nil {
						return err
					}
				}
			}
		}
		if err := history.Record(ctx, tx, series.ID, nil, model.HistoryRestored, series.Path, map[string]string{"recycledId": fmt.Sprint(r.ID)}); err != nil {
			return err
		}
		_, err := tx.NewDelete().Model(r).WherePK().Exec(ctx)
		return err
	})
	if err != nil {
		return 0, errors.Join(err, os.RemoveAll(dir))
	}
	if err := os.RemoveAll(source); err != nil {
		return series.ID, err
	}
	return series.ID, nil
}
