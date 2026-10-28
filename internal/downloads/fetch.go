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

// sourceFor returns the module that serves a job's release and the chapter
// to ask it for. A link whose module was disabled or removed goes to another
// enabled module with the same catalog, as the built-in sites have under
// their Keiyoushi ids: the job then works on copies of the link and release
// re-pointed there, without the engine ids, which belong to the module that
// issued them. With no such module the release can't be had here at all.
func (m *Manager) sourceFor(ctx context.Context, jc *jobCtx) (source.Module, source.ChapterRef, error) {
	mod, _, err := modules.GetAs[source.Module](m.mods, jc.link.ModuleID)
	if err != nil {
		if l, ok := m.mods.Get(jc.link.ModuleID); ok && l.Def.Enabled && l.Err != nil {
			// misconfigured: ours to fix, so keep retrying
			return nil, source.ChapterRef{}, infraError{err}
		}
		alt, ok := m.otherModuleFor(ctx, jc.link.ModuleID, jc.link.SourceID)
		if !ok {
			return nil, source.ChapterRef{}, permanent(fmt.Errorf("%w, and no enabled source module has %s", err, firstNonEmpty(jc.link.SourceName, "this catalog")))
		}
		from, link, rel := jc.link.ModuleID, *jc.link, *jc.release
		link.ModuleID, link.EngineRef, rel.EngineRef = alt.Def.ID, "", ""
		jc.link, jc.release = &link, &rel
		mod = alt.Instance
		m.log.Info("source module unavailable, downloading through another", "job", jc.job.ID, "from", from, "to", alt.Def.Name, "catalog", link.SourceName)
	}
	return mod, source.ChapterRef{
		Manga:     source.MangaRef{SourceID: jc.link.SourceID, URL: jc.link.MangaURL, EngineRef: jc.link.EngineRef, TitleHint: firstNonEmpty(jc.link.Title, jc.series.Title)},
		URL:       jc.release.ChapterURL,
		EngineRef: jc.release.EngineRef,
	}, nil
}

// otherModuleFor finds an enabled source module other than moduleID that has
// catalog sourceID, by module priority.
func (m *Manager) otherModuleFor(ctx context.Context, moduleID int64, sourceID string) (modules.Typed[source.Module], bool) {
	for _, t := range modules.ActiveAs[source.Module](m.mods, modules.KindSource) {
		if t.Def.ID == moduleID {
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		list, err := t.Instance.Sources(lctx)
		cancel()
		if err != nil {
			continue
		}
		for _, si := range list {
			if si.ID == sourceID {
				return t, true
			}
		}
	}
	return modules.Typed[source.Module]{}, false
}

// fetchPages downloads and validates every page into workDir.
func (m *Manager) fetchPages(ctx context.Context, jc *jobCtx, workDir string) ([]PageFile, error) {
	mod, ref, err := m.sourceFor(ctx, jc)
	if err != nil {
		return nil, err
	}
	dl, _ := m.settings.Downloads(ctx)
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
