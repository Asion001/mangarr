package userimport

import (
	"context"
	"slices"
	"time"

	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/userdata"
)

// Placement is a title already in the library whose chapter reached the site
// kept only by its id (or place), to look up in the site's chapter list.
type Placement struct {
	SeriesID int64                `json:"seriesId"`
	Title    userdata.ExportTitle `json:"title"`
}

// Place looks each title's chapter reached up at the site, Pause apart so
// the site isn't asked too fast, and writes the progress it gives. It
// returns how many titles got their progress.
func (s *Service) Place(ctx context.Context, userID int64, items []Placement, pause time.Duration) (int, error) {
	done := 0
	for i := range items {
		it := &items[i]
		r := it.Title.Reached
		if r == nil || r.Chapter >= 0 || len(it.Title.Sources) == 0 {
			continue
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				return done, ctx.Err()
			case <-time.After(pause):
			}
		}
		n, ok := s.chapterAt(ctx, it.Title.Sources[0], r)
		if !ok {
			continue
		}
		t := it.Title
		placed := *r
		placed.Chapter = n
		t.Reached = &placed
		if err := s.write(ctx, userID, it.SeriesID, &t); err != nil {
			return done, err
		}
		done++
	}
	return done, nil
}

// chapterAt finds a chapter's number in the site's list: by its id, else by
// its place. The site's own module answers (one that reads its accounts).
func (s *Service) chapterAt(ctx context.Context, src userdata.ExportSource, r *userdata.ExportReached) (float64, bool) {
	for _, m := range modules.ActiveAs[source.Accounts](s.Mods, modules.KindSource) {
		list, err := m.Instance.AccountSources(ctx)
		if err != nil || !slices.ContainsFunc(list, func(a source.AccountSource) bool { return a.SourceID == src.SourceID }) {
			continue
		}
		mod, _, err := modules.GetAs[source.Module](s.Mods, m.Def.ID)
		if err != nil {
			continue
		}
		var chapters []source.Chapter
		for try := 0; try < 3; try++ {
			if try > 0 { // the site pauses quick askers: wait it out
				select {
				case <-ctx.Done():
					return 0, false
				case <-time.After(time.Duration(try) * 10 * time.Second):
				}
			}
			if _, chapters, err = mod.Manga(ctx, source.MangaRef{SourceID: src.SourceID, URL: src.URL}, true); err == nil {
				break
			}
		}
		if err != nil {
			s.Log.Warn("list chapters to place progress", "url", src.URL, "err", err)
			return 0, false
		}
		return imports.ChapterIn(chapters, r.ChapterRef, r.ChapterPlace)
	}
	return 0, false
}
