package sourcegov

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
)

// Governed is a source module whose catalog requests go through a Governor.
// It always implements Latest and Thumbnails (returning ErrUnsupported when
// the wrapped module doesn't); other capabilities are reached through the
// raw instance (see modules.As).
type Governed struct {
	inner    source.Module
	moduleID int64
	g        *Governor
	latest   source.Latest
	thumbs   source.Thumbnails
	fetch    source.Fetchable
}

// Decorator returns a modules.Decorator that wraps source modules.
func Decorator(g *Governor) modules.Decorator {
	return func(def model.ProviderDefinition, inst modules.Instance) modules.Instance {
		m, ok := inst.(source.Module)
		if !ok {
			return inst
		}
		w := &Governed{inner: m, moduleID: def.ID, g: g}
		w.latest, _ = inst.(source.Latest)
		w.thumbs, _ = inst.(source.Thumbnails)
		w.fetch, _ = inst.(source.Fetchable)
		return w
	}
}

// Unwrap returns the wrapped module.
func (w *Governed) Unwrap() source.Module { return w.inner }

func (w *Governed) key(sourceID string) Key { return Key{ModuleID: w.moduleID, SourceID: sourceID} }

// mangaName names a title in a failure: its title when known, else its address.
func mangaName(ref source.MangaRef) string {
	if ref.TitleHint != "" {
		return ref.TitleHint
	}
	return ref.URL
}

func (w *Governed) Test(ctx context.Context) error { return w.inner.Test(ctx) }

func (w *Governed) Sources(ctx context.Context) ([]source.SourceInfo, error) {
	return w.inner.Sources(ctx)
}

func (w *Governed) Search(ctx context.Context, sourceID, query string, page int) (res *source.MangaPage, err error) {
	err = w.g.DoOp(ctx, w.key(sourceID), "Search “"+query+"”", func(ctx context.Context) error {
		res, err = w.inner.Search(ctx, sourceID, query, page)
		return err
	})
	return res, err
}

func (w *Governed) Manga(ctx context.Context, ref source.MangaRef, withChapters bool) (d *source.MangaDetails, chs []source.Chapter, err error) {
	what := "Details of " + mangaName(ref)
	if withChapters {
		what = "Chapter list of " + mangaName(ref)
	}
	err = w.g.DoOp(ctx, w.key(ref.SourceID), what, func(ctx context.Context) error {
		d, chs, err = w.inner.Manga(ctx, ref, withChapters)
		return err
	})
	return d, chs, err
}

func (w *Governed) Pages(ctx context.Context, ref source.ChapterRef) (pages []source.Page, err error) {
	err = w.g.DoOp(ctx, w.key(ref.Manga.SourceID), "Page list of a chapter of "+mangaName(ref.Manga), func(ctx context.Context) error {
		pages, err = w.inner.Pages(ctx, ref)
		return err
	})
	for i := range pages {
		pages[i].SourceID = ref.Manga.SourceID
	}
	return pages, err
}

// FetchPage holds a request slot until the body is closed.
func (w *Governed) FetchPage(ctx context.Context, p source.Page) (io.ReadCloser, string, error) {
	k := w.key(p.SourceID)
	release, err := w.g.Acquire(ctx, k)
	if err != nil {
		return nil, "", err
	}
	start := w.g.now()
	body, ct, err := w.inner.FetchPage(ctx, p)
	if ctx.Err() == nil {
		w.g.observe(k, fmt.Sprintf("Page %d image", p.Index+1), w.g.now().Sub(start), err)
		w.g.Report(k, err)
	}
	if err != nil {
		release()
		return nil, "", err
	}
	return &releasingBody{ReadCloser: body, release: release}, ct, nil
}

type releasingBody struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (b *releasingBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func (w *Governed) Latest(ctx context.Context, sourceID string, page int) (res *source.MangaPage, err error) {
	if w.latest == nil {
		return nil, source.ErrUnsupported
	}
	err = w.g.DoOp(ctx, w.key(sourceID), "Latest titles", func(ctx context.Context) error {
		res, err = w.latest.Latest(ctx, sourceID, page)
		return err
	})
	return res, err
}

func (w *Governed) Popular(ctx context.Context, sourceID string, page int) (res *source.MangaPage, err error) {
	if w.latest == nil {
		return nil, source.ErrUnsupported
	}
	err = w.g.DoOp(ctx, w.key(sourceID), "Popular titles", func(ctx context.Context) error {
		res, err = w.latest.Popular(ctx, sourceID, page)
		return err
	})
	return res, err
}

// Thumbnail is not throttled: covers are served from the engine's cache and
// our own disk cache, and grids load many at once.
func (w *Governed) Thumbnail(ctx context.Context, ref source.MangaRef) (io.ReadCloser, string, error) {
	if w.thumbs == nil {
		return nil, "", source.ErrUnsupported
	}
	return w.thumbs.Thumbnail(ctx, ref)
}

var (
	_ source.Module     = (*Governed)(nil)
	_ source.Latest     = (*Governed)(nil)
	_ source.Thumbnails = (*Governed)(nil)
)

// PageRequest passes through to the module, so a worker can be handed a page
// to fetch itself. The request costs nothing at the site, so it isn't paced.
func (w *Governed) PageRequest(ctx context.Context, p source.Page) (source.PageRequest, error) {
	if w.fetch == nil {
		return source.PageRequest{}, source.ErrUnsupported
	}
	return w.fetch.PageRequest(ctx, p)
}
