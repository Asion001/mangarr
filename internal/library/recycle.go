package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/chapternum"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/uptrace/bun"
)

var ErrRecycleConflict = errors.New("recycled file is in use or cannot be restored")

// RecycleInfo captures the file being replaced, and the operation replacing it.
type RecycleInfo struct {
	Series  *model.Series
	File    *model.ChapterFile
	Reason  string
	Job     *model.DownloadJob
	Profile *model.Profile
}

// Recycle moves or hardlinks a whole file and records its recycle time, never
// changing the inode's mtime (a hardlink may still be serving readers).
func (l *Library) Recycle(ctx context.Context, path, seriesFolder string, keepOriginal bool, meta RecycleInfo) (string, error) {
	l.recycleMu.Lock()
	defer l.recycleMu.Unlock()
	return l.recycle(ctx, path, seriesFolder, keepOriginal, meta)
}

func (l *Library) recycle(ctx context.Context, path, seriesFolder string, keepOriginal bool, meta RecycleInfo) (string, error) {
	now := time.Now().UTC()
	r := model.RecycledFile{Kind: "file", Reason: meta.Reason, RecycledAt: now, OriginalRelativePath: filepath.Base(path)}
	if meta.Series != nil {
		r.SeriesID, r.SeriesTitle = &meta.Series.ID, meta.Series.Title
	}
	if meta.File != nil {
		f := *meta.File
		r.ChapterID, r.ChapterFileID, r.FileSnapshot = &f.ChapterID, &f.ID, &f
		r.OriginalRelativePath, r.ProcessParams = f.RelativePath, f.ProcessParams
		r.SourceName, r.Scanlator = f.SourceName, f.Scanlator
	}
	if meta.Job != nil {
		r.JobID, r.JobKind = &meta.Job.ID, meta.Job.Kind
	}
	if meta.Profile != nil {
		r.ProfileName = meta.Profile.Name
	}
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("cannot recycle a symlink")
	}
	if st.IsDir() {
		if keepOriginal {
			return "", errors.New("cannot hardlink a folder")
		}
		r.Kind = "folder"
		if meta.Series != nil {
			snap := &model.RecycledFolder{Series: *meta.Series, ProcessParams: map[int64]string{}}
			if err := l.db.NewSelect().Model(&snap.Chapters).Where("series_id = ?", meta.Series.ID).Scan(ctx); err != nil {
				return "", err
			}
			if err := l.db.NewSelect().Model(&snap.Files).Where("series_id = ?", meta.Series.ID).Scan(ctx); err != nil {
				return "", err
			}
			for _, f := range snap.Files {
				snap.ProcessParams[f.ID] = f.ProcessParams
			}
			r.FolderSnapshot = snap
			r.OriginalRelativePath = meta.Series.Path
		}
	}
	if err := inspectRecycled(path, &r); err != nil {
		return "", err
	}
	root := l.RecycleDir(ctx)
	if seriesFolder != "" && !filepath.IsLocal(seriesFolder) {
		return "", errors.New("invalid series folder")
	}
	// Nanoseconds avoid overwriting versions of the same chapter within a second.
	r.RecycledPath = filepath.Join(seriesFolder, now.Format("20060102-150405.000000000")+"-"+filepath.Base(path))
	dst, err := safeRecyclePath(root, r.RecycledPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		return "", errors.New("recycle destination already exists")
	}
	if keepOriginal {
		err = fsutil.LinkOrCopy(path, dst)
	} else {
		err = fsutil.Move(path, dst)
	}
	if err != nil {
		return "", err
	}
	if _, err = l.db.NewInsert().Model(&r).Exec(ctx); err != nil {
		if keepOriginal {
			_ = os.Remove(dst)
		} else if undo := fsutil.Move(dst, path); undo != nil {
			return "", errors.Join(err, undo)
		}
		return "", err
	}
	return dst, nil
}

// safeRecyclePath rejects escapes and symlinks, including parent components.
func safeRecyclePath(root, rel string) (string, error) {
	if !filepath.IsLocal(rel) || rel == "." {
		return "", errors.New("invalid relative path")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := root
	for _, part := range strings.Split(filepath.Clean(rel), string(filepath.Separator)) {
		path = filepath.Join(path, part)
		st, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err == nil && st.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlink in recycle path")
		}
	}
	return path, nil
}

func inspectRecycled(path string, r *model.RecycledFile) error {
	if r.Kind == "folder" {
		return filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return errors.New("symlink in recycled folder")
			}
			if !d.IsDir() {
				st, err := d.Info()
				if err != nil {
					return err
				}
				r.Size += st.Size()
			}
			return nil
		})
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	r.Size, err = io.Copy(h, f)
	if err != nil {
		return err
	}
	r.SHA256 = hex.EncodeToString(h.Sum(nil))
	pages, err := cbz.List(path)
	if err != nil {
		return err
	}
	r.PageCount = len(pages)
	return nil
}

// WithRecycled serializes source reservation and reads against deletion/purge.
func (l *Library) WithRecycled(ctx context.Context, id int64, fn func(*model.RecycledFile, string) error) error {
	l.recycleMu.Lock()
	defer l.recycleMu.Unlock()
	var r model.RecycledFile
	if err := l.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return err
	}
	path, err := safeRecyclePath(l.RecycleDir(ctx), r.RecycledPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return fn(&r, path)
}

func (l *Library) recycleInUse(ctx context.Context, r *model.RecycledFile, chapter bool) (bool, error) {
	q := l.db.NewSelect().Model((*model.DownloadJob)(nil)).Where("status IN (?)", bun.In([]string{model.JobQueued, model.JobPaused, model.JobDownloading, model.JobProcessing, model.JobImporting}))
	if chapter && r.ChapterID != nil {
		q = q.Where("chapter_id = ?", *r.ChapterID)
	} else {
		q = q.Where("recycled_file_id = ?", r.ID)
	}
	return q.Exists(ctx)
}

// DeleteRecycled removes an entry only after its bytes have been removed.
func (l *Library) DeleteRecycled(ctx context.Context, id int64) error {
	l.recycleMu.Lock()
	defer l.recycleMu.Unlock()
	var r model.RecycledFile
	if err := l.db.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return err
	}
	return l.deleteRecycled(ctx, &r)
}

func (l *Library) deleteRecycled(ctx context.Context, r *model.RecycledFile) error {
	busy, err := l.recycleInUse(ctx, r, false)
	if err != nil {
		return err
	}
	if busy {
		return ErrRecycleConflict
	}
	path, err := safeRecyclePath(l.RecycleDir(ctx), r.RecycledPath)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	_, err = l.db.NewDelete().Model(r).WherePK().Exec(ctx)
	return err
}

// PurgeRecycleBin uses the indexed recycle time, not the source inode's mtime.
func (l *Library) PurgeRecycleBin(ctx context.Context) (int, error) {
	mm, err := l.settings.MediaManagement(ctx)
	if err != nil || mm.RecycleBinDays <= 0 {
		return 0, err
	}
	if _, err := l.RescanRecycleBin(ctx); err != nil {
		return 0, err
	}
	l.recycleMu.Lock()
	defer l.recycleMu.Unlock()
	var rows []model.RecycledFile
	if err := l.db.NewSelect().Model(&rows).Where("recycled_at < ?", time.Now().UTC().Add(-time.Duration(mm.RecycleBinDays)*24*time.Hour)).Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for i := range rows {
		if err := l.deleteRecycled(ctx, &rows[i]); errors.Is(err, ErrRecycleConflict) {
			continue
		} else if err != nil {
			return n, err
		}
		n++
	}
	removeEmptyDirs(l.RecycleDir(ctx))
	return n, nil
}

func recycleStamp(name string) (time.Time, string, bool) {
	for _, layout := range []string{"20060102-150405.000000000", "20060102-150405"} {
		n := len(layout)
		if len(name) > n && name[n] == '-' {
			if at, err := time.ParseInLocation(layout, name[:n], time.UTC); err == nil {
				return at, name[n+1:], true
			}
		}
	}
	return time.Time{}, name, false
}

// RescanRecycleBin indexes legacy archives and whole series folders. Unknown
// timestamps start retention at discovery, never at a hardlink's old mtime.
func (l *Library) RescanRecycleBin(ctx context.Context) (int, error) {
	l.recycleMu.Lock()
	defer l.recycleMu.Unlock()
	root := l.RecycleDir(ctx)
	if _, err := os.Stat(root); errors.Is(err, os.ErrNotExist) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	var rows []model.RecycledFile
	if err := l.db.NewSelect().Model(&rows).Scan(ctx); err != nil {
		return 0, err
	}
	known := map[string]bool{}
	for _, r := range rows {
		p, err := safeRecyclePath(root, r.RecycledPath)
		if err != nil {
			return 0, err
		}
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			if _, err := l.db.NewDelete().Model(&r).WherePK().Exec(ctx); err != nil {
				return 0, err
			}
		} else if err != nil {
			return 0, err
		} else {
			known[r.RecycledPath] = true
		}
	}
	n := 0
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if known[rel] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		at, name, stamped := recycleStamp(d.Name())
		if d.IsDir() && !stamped {
			return nil
		}
		if !d.IsDir() && !strings.EqualFold(filepath.Ext(name), ".cbz") {
			return nil
		}
		if !stamped {
			at = time.Now().UTC()
		}
		r := model.RecycledFile{Kind: "file", Reason: "unknown", RecycledPath: rel, OriginalRelativePath: name, RecycledAt: at}
		folder := filepath.Dir(rel)
		if d.IsDir() {
			r.Kind, r.Reason, folder = "folder", "series_deleted", name
		}
		var matches []model.Series
		if err := l.db.NewSelect().Model(&matches).Where("path = ?", folder).Scan(ctx); err != nil {
			return err
		}
		if folder != "." {
			r.SeriesTitle = folder // until a series matches, the folder names it
		}
		if len(matches) == 1 {
			s := matches[0]
			r.SeriesID, r.SeriesTitle = &s.ID, s.Title
			if r.Kind == "file" {
				var f model.ChapterFile
				err := l.db.NewSelect().Model(&f).Where("series_id = ? AND relative_path = ?", s.ID, name).Scan(ctx)
				if err == nil {
					r.ChapterID, r.ChapterFileID = &f.ChapterID, &f.ID
				} else if !errors.Is(err, sql.ErrNoRows) {
					return err
				} else if num, ok := chapternum.Parse(s.Title, strings.TrimSuffix(name, filepath.Ext(name)), -1); ok {
					var ch model.Chapter
					if err := l.db.NewSelect().Model(&ch).Where("series_id = ? AND number_key = ?", s.ID, chapternum.Key(num)).Scan(ctx); err == nil {
						r.ChapterID = &ch.ID
					} else if !errors.Is(err, sql.ErrNoRows) {
						return err
					}
				}
			}
		}
		if err := inspectRecycled(p, &r); err != nil {
			return fmt.Errorf("index %s: %w", rel, err)
		}
		if _, err := l.db.NewInsert().Model(&r).Exec(ctx); err != nil {
			return err
		}
		n++
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	return n, err
}
