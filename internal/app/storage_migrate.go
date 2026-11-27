package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/libfs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// MigrateOptions tunes MigrateStorage.
type MigrateOptions struct {
	// DryRun lists what would be copied without copying.
	DryRun bool
	// Workers is how many files are uploaded at once.
	Workers int
	// From reads a folder from another place on this machine: the library
	// path (as the database knows it) mapped to where it is mounted here.
	From map[string]string
}

// MigrateStorage copies the library (every root folder and the recycle bin)
// from the local disk to the object storage set in cfg.Storage. Paths keep
// their names (see libfs.Key). It can be run again: files already there
// with the same size are skipped, so a second run before switching over
// only copies what changed. Nothing is ever deleted.
func MigrateStorage(ctx context.Context, cfg *config.Config, opts MigrateOptions, out io.Writer) error {
	if cfg.Storage == nil {
		return errors.New("set MANGARR_STORAGE=s3 and the MANGARR_S3_* variables for the bucket to copy to")
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	out = &lockedWriter{w: out} // the uploads report from several goroutines
	s3, err := libfs.NewS3(*cfg.Storage)
	if err != nil {
		return err
	}
	if err := s3.Probe(ctx); err != nil {
		return fmt.Errorf("object storage %s: %w", s3.Identity(), err)
	}
	d, err := db.Open(ctx, cfg.DB)
	if err != nil {
		return err
	}
	defer d.Close()
	var roots []model.RootFolder
	if err := d.NewSelect().Model(&roots).Order("path").Scan(ctx); err != nil {
		return fmt.Errorf("root folders: %w", err)
	}
	mm, _ := settings.NewStore(d).MediaManagement(ctx)
	recycle := mm.RecycleBinPath
	if recycle == "" {
		recycle = filepath.Join(cfg.DataDir, "recycle")
	}
	dirs := []string{recycle}
	for _, r := range roots {
		dirs = append(dirs, r.Path)
	}
	fmt.Fprintf(out, "copying to %s\n", s3.Identity())

	type job struct{ local, name string }
	jobs := make(chan job)
	var copied, skipped, failed, bytes atomic.Int64
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	for range opts.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				st, err := os.Stat(j.local)
				if err != nil {
					continue
				}
				if size, err := s3.Head(ctx, j.name); err == nil && size == st.Size() {
					skipped.Add(1)
					continue
				}
				if opts.DryRun {
					fmt.Fprintf(out, "would copy %s (%d bytes)\n", j.name, st.Size())
					copied.Add(1)
					bytes.Add(st.Size())
					continue
				}
				err = s3.Upload(ctx, j.local, j.name)
				if err == nil {
					// read back what arrived before counting it as copied
					var size int64
					if size, err = s3.Head(ctx, j.name); err == nil && size != st.Size() {
						err = fmt.Errorf("%d bytes arrived of %d", size, st.Size())
					}
				}
				if err != nil {
					failed.Add(1)
					fmt.Fprintf(out, "FAILED %s: %v\n", j.name, err)
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					continue
				}
				n := copied.Add(1)
				bytes.Add(st.Size())
				if n%100 == 0 {
					fmt.Fprintf(out, "%d files copied (%.1f GB)\n", n, float64(bytes.Load())/(1<<30))
				}
			}
		}()
	}
	start := time.Now()
	var walkErr error
	for _, dir := range dirs {
		local := dir
		for from, to := range opts.From {
			if rel, err := filepath.Rel(filepath.Clean(from), dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				local = filepath.Join(to, rel)
			}
		}
		if _, err := os.Stat(local); err != nil {
			fmt.Fprintf(out, "skipping %s: %v\n", dir, err)
			continue
		}
		fmt.Fprintf(out, "copying %s\n", dir)
		err := filepath.WalkDir(local, func(p string, e fs.DirEntry, err error) error {
			if err != nil {
				fmt.Fprintf(out, "FAILED %s: %v\n", p, err)
				failed.Add(1)
				return nil
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !e.Type().IsRegular() || strings.HasSuffix(p, ".partial") || strings.HasPrefix(e.Name(), ".mangarr-write-test") {
				return nil
			}
			rel, err := filepath.Rel(local, p)
			if err != nil {
				return err
			}
			select {
			case jobs <- job{local: p, name: filepath.Join(dir, rel)}:
			case <-ctx.Done():
				return ctx.Err()
			}
			return nil
		})
		if err != nil {
			walkErr = err
			break
		}
	}
	close(jobs)
	wg.Wait()
	verb := "copied"
	if opts.DryRun {
		verb = "to copy"
	}
	fmt.Fprintf(out, "done in %s: %d files %s (%.2f GB), %d already there, %d failed\n",
		time.Since(start).Round(time.Second), copied.Load(), verb, float64(bytes.Load())/(1<<30), skipped.Load(), failed.Load())
	if walkErr != nil {
		return walkErr
	}
	if failed.Load() > 0 {
		return fmt.Errorf("%d files failed (first: %v); run it again to retry them", failed.Load(), firstErr)
	}
	if opts.DryRun {
		return nil
	}
	return checkMigrated(ctx, d, s3, out)
}

// checkMigrated looks for every chapter file the database knows in the
// bucket.
func checkMigrated(ctx context.Context, d *db.DB, s3 *libfs.S3, out io.Writer) error {
	var rows []struct {
		Root, Series, Rel string
		Size              int64
	}
	err := d.NewSelect().TableExpr("chapter_files AS f").
		Join("JOIN series AS s ON s.id = f.series_id").
		Join("JOIN root_folders AS r ON r.id = s.root_folder_id").
		ColumnExpr("r.path AS root, s.path AS series, f.relative_path AS rel, f.size AS size").
		Scan(ctx, &rows)
	if err != nil {
		return fmt.Errorf("check: %w", err)
	}
	missing := 0
	for _, r := range rows {
		name := filepath.Join(r.Root, r.Series, r.Rel)
		if size, err := s3.Head(ctx, name); err != nil || size != r.Size {
			missing++
			if missing <= 20 {
				fmt.Fprintf(out, "not in the bucket (or a different size): %s\n", name)
			}
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d of %d chapter files the database knows are not in the bucket as recorded", missing, len(rows))
	}
	fmt.Fprintf(out, "all %d chapter files the database knows are in the bucket\n", len(rows))
	return nil
}

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
