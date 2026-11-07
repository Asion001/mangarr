package app

import (
	"context"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/events"
)

// measureAfter is how long a written file waits before its pages are
// measured: processing often rewrites a fresh download soon after, and the
// rewrite would need measuring again.
const measureAfter = 10 * time.Minute

// measureNewFiles measures the pages of every file written to the library
// (an import, an upgrade, processing) in the background, one file at a
// time, so the reader finds each page's borders ready. Files already in the
// library are measured as readers open them.
func (a *App) measureNewFiles(ctx context.Context) {
	type target struct {
		seriesID int64
		path     string
	}
	// written again: wait from now
	settle := NewDebouncer[string](measureAfter)
	work := make(chan target, 256)
	a.Bus.Subscribe(func(e events.Event) {
		path, ok := e.Payload.(string)
		if !ok || path == "" || e.SeriesID == 0 {
			return
		}
		t := target{e.SeriesID, path}
		settle.Do(path, func() {
			select {
			case work <- t:
			default: // far behind: those pages get measured when read
			}
		})
	}, downloads.EventFileWritten)
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-work:
			id := a.Reading.FileAt(ctx, t.seriesID, t.path)
			if id == 0 {
				continue
			}
			start := time.Now()
			if n, err := a.Reading.MeasureFile(ctx, id); err == nil && n > 0 {
				a.Log.Debug("measured page borders", "file", id, "pages", n, "took", time.Since(start).Round(time.Millisecond))
			}
		}
	}
}
