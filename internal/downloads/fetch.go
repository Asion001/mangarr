package downloads

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
)

// PageFile is a validated page on disk. Fetch and processing return pages in
// archive order; the caller keeps their files alive until import completes.
type PageFile struct {
	Name   string // archive name, e.g. 0001.jpg
	Path   string
	Format string
	Width  int
	Height int
}

// fetchPages downloads and validates every page into workDir.
func (m *Manager) fetchPages(ctx context.Context, jc *jobCtx, workDir string) ([]PageFile, error) {
	mod, _, err := modules.GetAs[source.Module](m.mods, jc.link.ModuleID)
	if err != nil {
		return nil, infraError{err}
	}
	dl, _ := m.settings.Downloads(ctx)
	ref := source.ChapterRef{
		Manga:     source.MangaRef{SourceID: jc.link.SourceID, URL: jc.link.MangaURL, EngineRef: jc.link.EngineRef, TitleHint: firstNonEmpty(jc.link.Title, jc.series.Title)},
		URL:       jc.release.ChapterURL,
		EngineRef: jc.release.EngineRef,
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	list, err := mod.Pages(pctx, ref)
	cancel()
	if err != nil {
		if errors.Is(err, source.ErrNotFound) {
			return nil, permanent(err)
		}
		return nil, classify(err)
	}
	if min := jc.profile.Config.MinPages; min > 0 && len(list) < min {
		return nil, permanent(fmt.Errorf("chapter has %d pages, profile requires at least %d", len(list), min))
	}
	pages := make([]PageFile, len(list))
	conc := dl.PageConcurrency
	if conc <= 0 {
		conc = 2
	}
	retries := dl.PageRetries
	if retries <= 0 {
		retries = 3
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	done := 0
	var fetched int64 // bytes pulled from the site, for the queue's progress
	m.progress(jc.job, 0, len(list), 0)
	for i, p := range list {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			mu.Lock()
			stop := firstErr != nil
			mu.Unlock()
			if stop {
				return
			}
			pf, err := m.fetchPage(ctx, mod, p, i, workDir, retries)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("page %d: %w", i+1, err)
				}
				return
			}
			pages[i] = pf
			done++
			if st, err := os.Stat(pf.Path); err == nil {
				fetched += st.Size()
			}
			m.progress(jc.job, done, len(list), fetched)
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return pages, nil
}

func (m *Manager) fetchPage(ctx context.Context, mod source.Module, p source.Page, i int, workDir string, retries int) (PageFile, error) {
	var lastErr error
	for attempt := 0; attempt < retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return PageFile{}, ctx.Err()
			case <-time.After(time.Duration(1<<(2*attempt)) * time.Second): // 4s, 16s
			}
		}
		fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		body, _, err := mod.FetchPage(fctx, p)
		if err != nil {
			cancel()
			lastErr = classify(err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(body, 64<<20))
		body.Close()
		cancel()
		if err != nil {
			lastErr = err
			continue
		}
		info, err := imagecheck.Detect(data)
		if err != nil {
			lastErr = err
			continue
		}
		if imagecheck.Truncated(info.Format, data) {
			// a page cut short downloads again; one that always comes like
			// this is kept, as the site serves it
			if attempt < retries-1 {
				lastErr = fmt.Errorf("page %d is cut off (%d bytes)", i+1, len(data))
				continue
			}
			m.log.Warn("page is cut off on every download; keeping it", "page", i+1, "bytes", len(data))
		}
		name := cbz.PageName(i, imagecheck.Ext(info.Format))
		path := filepath.Join(workDir, name)
		// written through a temporary name: the reader serves pages out of
		// this folder while the chapter downloads and must never see half a page
		tmp := path + ".part"
		if err := os.WriteFile(tmp, data, 0o664); err != nil {
			return PageFile{}, infraError{err}
		}
		if err := os.Rename(tmp, path); err != nil {
			return PageFile{}, infraError{err}
		}
		return PageFile{Name: name, Path: path, Format: info.Format, Width: info.Width, Height: info.Height}, nil
	}
	return PageFile{}, lastErr
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}

// workDir is where a job's pages are written while it runs.
func (m *Manager) workDir(jobID int64) string {
	return filepath.Join(m.dataDir, "staging", "job-"+strconv.FormatInt(jobID, 10))
}

// StagedPage is the path of page n of a chapter being downloaded right now,
// so a reader waiting for it gets the page the downloader already fetched
// instead of asking the source for it a second time. It returns "" when
// there's no such page yet.
func (m *Manager) StagedPage(ctx context.Context, chapterID int64, n int) string {
	if n < 1 {
		return ""
	}
	var job model.DownloadJob
	err := m.db.NewSelect().Model(&job).Column("id", "status").
		Where("chapter_id = ? AND status = ?", chapterID, model.JobDownloading).Limit(1).Scan(ctx)
	if err != nil {
		return ""
	}
	dir := m.workDir(job.ID)
	for _, format := range []string{"jpeg", "png", "webp", "avif", "jxl", "gif"} {
		ext := imagecheck.Ext(format)
		path := filepath.Join(dir, cbz.PageName(n-1, ext))
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			return path
		}
	}
	return ""
}
