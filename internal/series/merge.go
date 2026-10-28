package series

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

// Merge folds drop into keep: two titles in one language that are one work.
// drop's sources move to keep, its downloaded chapters move into keep's
// folder where keep has no file for that chapter, what was read of it counts
// for keep (finished wins), its followers follow keep, and drop is deleted
// with what is left of its folder going to the recycle bin.
func (s *Service) Merge(ctx context.Context, keepID, dropID int64) error {
	if keepID == dropID {
		return ValidationError{"a title can't be merged into itself"}
	}
	keep, err := s.Get(ctx, keepID)
	if err != nil {
		return err
	}
	drop, err := s.Get(ctx, dropID)
	if err != nil {
		return err
	}
	if err := s.mergeInto(ctx, keep, drop); err != nil {
		return err
	}
	if err := s.Delete(ctx, drop.ID, true); err != nil {
		return err
	}
	_, _ = s.queue.Push(ctx, "RefreshSeries", map[string]any{"seriesId": keep.ID}, "merged")
	s.bus.Changed("series", "updated", keep.ID)
	return nil
}

// moved is a chapter file taken from drop's folder into keep's.
type moved struct{ from, to string }

func (s *Service) mergeInto(ctx context.Context, keep, drop *model.Series) error {
	unlockKeep := library.LockSeries(keep.ID)
	defer unlockKeep()
	unlockDrop := library.LockSeries(drop.ID)
	defer unlockDrop()
	keepDir, err := s.lib.SeriesDir(ctx, keep)
	if err != nil {
		return err
	}
	dropDir, err := s.lib.SeriesDir(ctx, drop)
	if err != nil {
		return err
	}
	var keepChapters, dropChapters []model.Chapter
	if err := s.db.NewSelect().Model(&keepChapters).Where("series_id = ?", keep.ID).Scan(ctx); err != nil {
		return err
	}
	if err := s.db.NewSelect().Model(&dropChapters).Where("series_id = ?", drop.ID).Scan(ctx); err != nil {
		return err
	}
	byNumber := map[string]*model.Chapter{}
	for i := range keepChapters {
		byNumber[keepChapters[i].NumberKey] = &keepChapters[i]
	}
	var files []model.ChapterFile
	if err := s.db.NewSelect().Model(&files).Where("series_id = ?", drop.ID).Scan(ctx); err != nil {
		return err
	}
	fileOf := map[int64]*model.ChapterFile{}
	for i := range files {
		fileOf[files[i].ID] = &files[i]
	}
	var done []moved
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		// what was read of drop's chapters counts for keep's title
		if err := readstate.Retitle(ctx, tx, drop.ID, readstate.TitleID(drop), readstate.TitleID(keep)); err != nil {
			return err
		}
		// downloaded chapters keep has no file for
		for i := range dropChapters {
			ch := &dropChapters[i]
			if ch.FileID == nil || fileOf[*ch.FileID] == nil {
				continue
			}
			f := fileOf[*ch.FileID]
			target := byNumber[ch.NumberKey]
			if target != nil && target.FileID != nil {
				continue // keep has it
			}
			rel := freeName(keepDir, f.RelativePath, done)
			done = append(done, moved{from: filepath.Join(dropDir, f.RelativePath), to: filepath.Join(keepDir, rel)})
			now := time.Now().UTC()
			if target == nil {
				// keep doesn't list the chapter: it moves over whole
				if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("series_id = ?", keep.ID).Set("updated_at = ?", now).
					Where("id = ?", ch.ID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewUpdate().Model((*model.ChapterRelease)(nil)).Set("series_id = ?", keep.ID).
					Where("chapter_id = ?", ch.ID).Exec(ctx); err != nil {
					return err
				}
				if _, err := tx.NewUpdate().Model((*model.ChapterFile)(nil)).Set("series_id = ?", keep.ID).Set("relative_path = ?", rel).
					Where("id = ?", f.ID).Exec(ctx); err != nil {
					return err
				}
				continue
			}
			if _, err := tx.NewUpdate().Model((*model.ChapterFile)(nil)).Set("series_id = ?", keep.ID).Set("chapter_id = ?", target.ID).
				Set("relative_path = ?", rel).Where("id = ?", f.ID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = NULL").Where("id = ?", ch.ID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = ?", f.ID).Set("state = ?", ch.State).
				Set("cleaned_at = NULL").Set("updated_at = ?", now).Where("id = ?", target.ID).Exec(ctx); err != nil {
				return err
			}
		}
		// sources keep doesn't have yet move over; their releases are found
		// again by keep's refresh, except those of chapters that moved whole
		var sources []model.SeriesSource
		if err := tx.NewSelect().Model(&sources).Where("series_id = ?", drop.ID).Scan(ctx); err != nil {
			return err
		}
		for _, src := range sources {
			n, err := tx.NewSelect().Model((*model.SeriesSource)(nil)).
				Where("series_id = ? AND source_id = ? AND manga_url = ?", keep.ID, src.SourceID, src.MangaURL).Count(ctx)
			if err != nil {
				return err
			}
			if n > 0 {
				continue // goes with drop
			}
			if _, err := tx.NewUpdate().Model((*model.SeriesSource)(nil)).Set("series_id = ?", keep.ID).Where("id = ?", src.ID).Exec(ctx); err != nil {
				return err
			}
			if _, err := tx.NewDelete().Model((*model.ChapterRelease)(nil)).Where("series_source_id = ? AND series_id = ?", src.ID, drop.ID).Exec(ctx); err != nil {
				return err
			}
		}
		// followers of drop follow keep
		if _, err := tx.NewRaw(`INSERT INTO follows (user_id, series_id, created_at)
SELECT f.user_id, ?, f.created_at FROM follows AS f
WHERE f.series_id = ? AND NOT EXISTS (SELECT 1 FROM follows AS k WHERE k.user_id = f.user_id AND k.series_id = ?)`, keep.ID, drop.ID, keep.ID).Exec(ctx); err != nil {
			return err
		}
		if len(done) == 0 {
			return nil
		}
		if err := os.MkdirAll(keepDir, 0o755); err != nil {
			return err
		}
		for i, m := range done {
			if err := os.MkdirAll(filepath.Dir(m.to), 0o755); err != nil {
				return undo(done[:i], err)
			}
			if err := moveFile(m.from, m.to); err != nil {
				return undo(done[:i], fmt.Errorf("move %s: %w", m.from, err))
			}
		}
		return nil
	})
	return err
}

// undo puts files moved so far back when the merge fails.
func undo(done []moved, err error) error {
	for _, m := range done {
		_ = moveFile(m.to, m.from)
	}
	return err
}

// moveFile renames, or copies and removes across file systems.
func moveFile(from, to string) error {
	if err := os.Rename(from, to); err == nil {
		return nil
	}
	data, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		return err
	}
	return os.Remove(from)
}

// freeName is rel, or rel with " (2)" and up before the extension when keep's
// folder (or an earlier move) already has that name.
func freeName(dir, rel string, taken []moved) string {
	used := func(name string) bool {
		full := filepath.Join(dir, name)
		if _, err := os.Stat(full); err == nil {
			return true
		}
		for _, m := range taken {
			if m.to == full {
				return true
			}
		}
		return false
	}
	if !used(rel) {
		return rel
	}
	ext := filepath.Ext(rel)
	base := strings.TrimSuffix(rel, ext)
	for i := 2; ; i++ {
		if name := fmt.Sprintf("%s (%d)%s", base, i, ext); !used(name) {
			return name
		}
	}
}
