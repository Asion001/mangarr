package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Asion001/mangarr/internal/libfs"
	"github.com/Asion001/mangarr/internal/model"
)

// useStorage connects to the object storage configured for the library,
// if any, and refuses to start when it can't be read and written.
func (a *App) useStorage(ctx context.Context) error {
	if a.Cfg.Storage == nil {
		libfs.Use(nil)
		return nil
	}
	s3, err := libfs.NewS3(*a.Cfg.Storage)
	if err != nil {
		return fmt.Errorf("object storage: %w", err)
	}
	pctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if err := s3.Probe(pctx); err != nil {
		return fmt.Errorf("object storage %s: %w", s3.Identity(), err)
	}
	libfs.Use(s3)
	a.Log.Info("the library is kept on object storage", "storage", s3.Identity())
	return nil
}

// checkStorage warns when the library on object storage looks empty while
// the database knows files: most likely the files were never copied over.
func (a *App) checkStorage(ctx context.Context) {
	if libfs.Remote() == nil {
		return
	}
	var f model.ChapterFile
	if err := a.DB.NewSelect().Model(&f).Order("id DESC").Limit(1).Scan(ctx); err != nil {
		return
	}
	var s model.Series
	if err := a.DB.NewSelect().Model(&s).Where("id = ?", f.SeriesID).Scan(ctx); err != nil {
		return
	}
	dir, err := a.Library.SeriesDir(ctx, &s)
	if err != nil {
		return
	}
	if p := filepath.Join(dir, f.RelativePath); !libfs.Exists(p) {
		a.Log.Warn("a library file the database knows is not on the object storage; copy the library over with `mangarr storage migrate` if it isn't yet", "path", p)
	}
}
