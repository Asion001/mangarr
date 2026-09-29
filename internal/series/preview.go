package series

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/model"
)

// PreviewTTL is how long a preview nobody opens is kept.
const PreviewTTL = 30 * 24 * time.Hour

// Preview opens a title from search without adding it to the library: a
// series row marked as a preview, linked to its sources, whose chapters
// stream from the source. Nothing is monitored or downloaded, no folder is
// made, and it stays out of the library, Updates, Discover and reading apps.
// An existing preview of the same source link is reused. created reports
// whether a new preview was made. A title already in the library is an
// ExistsError.
func (s *Service) Preview(ctx context.Context, req AddRequest) (ser *model.Series, created bool, err error) {
	if len(req.Sources) == 0 {
		return nil, false, ValidationError{"at least one source is required"}
	}
	if pv, err := s.previewFor(ctx, req.Sources, req.Language); err != nil {
		return nil, false, err
	} else if pv != nil {
		return pv, false, s.TouchPreview(ctx, pv.ID)
	}
	req.Preview, req.NoRefresh = true, true
	req.Monitor, req.SearchMissing, req.RequestID = model.MonitorNone, false, 0
	ser, err = s.Add(ctx, req)
	return ser, err == nil, err
}

// TouchPreview records that a preview was opened, so cleanup keeps it.
func (s *Service) TouchPreview(ctx context.Context, id int64) error {
	_, err := s.db.NewUpdate().Model((*model.Series)(nil)).Set("preview_seen_at = ?", time.Now().UTC()).
		Where("id = ? AND preview = ?", id, true).Exec(ctx)
	return err
}

// previewFor finds a preview linked to any of the source links.
func (s *Service) previewFor(ctx context.Context, links []SourceLink, language string) (*model.Series, error) {
	for _, l := range links {
		if l.ModuleID == 0 || l.SourceID == "" || l.URL == "" {
			continue
		}
		var ser model.Series
		q := s.db.NewSelect().Model(&ser).
			Where("preview = ?", true).
			Where("id IN (SELECT series_id FROM series_sources WHERE module_id = ? AND source_id = ? AND manga_url = ?)", l.ModuleID, l.SourceID, l.URL).
			Order("id").Limit(1)
		if lang := library.NormalizeLanguage(language); lang != "" {
			q = q.Where("LOWER(language) = ?", lang)
		}
		err := q.Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return &ser, nil
	}
	return nil, nil
}

// adopt turns a preview into a library series with req's settings, keeping
// its id, chapters and reading progress.
func (s *Service) adopt(ctx context.Context, pv *model.Series, req AddRequest) (*model.Series, error) {
	rf, err := s.lib.RootFolder(ctx, pv.RootFolderID)
	if req.RootFolderID > 0 {
		if rf, err = s.lib.RootFolder(ctx, req.RootFolderID); err != nil {
			return nil, ValidationError{"root folder not found"}
		}
	} else if err != nil {
		if rf, err = s.lib.FolderForLanguage(ctx, firstNonEmpty(library.NormalizeLanguage(req.Language), pv.Language)); err != nil {
			return nil, ValidationError{err.Error()}
		}
	}
	profileID, err := s.profileID(ctx, req.ProfileID)
	if err != nil {
		return nil, err
	}
	defer library.LockSeries(pv.ID)()
	ser := *pv
	if rf.ID != ser.RootFolderID {
		folder, err := s.lib.UniqueFolder(ctx, rf.ID, s.lib.FolderName(ctx, ser.Title, ser.Metadata.Year))
		if err != nil {
			return nil, err
		}
		ser.RootFolderID, ser.Path = rf.ID, folder
	}
	now := time.Now().UTC()
	ser.Preview, ser.PreviewSeenAt = false, nil
	ser.Monitored, ser.MonitorNew, ser.ProfileID = true, orDefault(req.MonitorNew, model.MonitorAll), profileID
	if req.ReadingDirection != "" {
		ser.ReadingDirection = req.ReadingDirection
	}
	if req.Tags != nil {
		ser.Tags = req.Tags
	}
	if len(req.BlockedScanlators) > 0 {
		ser.BlockedScanlators = cleanNames(req.BlockedScanlators)
	}
	// applied by the next refresh, over the chapters the preview already has
	ser.AddOptions = model.AddOptions{Pending: true, Monitor: orDefault(req.Monitor, model.MonitorAll), LatestCount: req.LatestCount,
		FromChapter: req.FromChapter, SearchMissing: req.SearchMissing}
	ser.AddedAt, ser.UpdatedAt = now, now
	err = s.db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model(&ser).Column("preview", "preview_seen_at", "monitored", "monitor_new", "profile_id", "root_folder_id", "path",
			"reading_direction", "tags", "blocked_scanlators", "add_options", "added_at", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		var have []model.SeriesSource
		if err := tx.NewSelect().Model(&have).Where("series_id = ?", ser.ID).Scan(ctx); err != nil {
			return err
		}
		next := len(have)
		for _, l := range req.Sources {
			linked := false
			for _, h := range have {
				linked = linked || (h.ModuleID == l.ModuleID && h.SourceID == l.SourceID && h.MangaURL == l.URL)
			}
			if !linked {
				if _, err := s.insertLink(ctx, tx, &ser, l, next); err != nil {
					return err
				}
				next++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if _, err := s.lib.EnsureSeriesDir(ctx, &ser); err != nil {
		s.log.Warn("create series folder", "series", ser.Title, "err", err)
	}
	if !req.NoRefresh {
		if _, err := s.queue.Push(ctx, "RefreshSeries", map[string]any{"seriesId": ser.ID}, "series-add"); err != nil {
			s.log.Warn("queue refresh", "err", err)
		}
	}
	s.bus.Publish(events.Event{Type: events.SeriesAdded, SeriesID: ser.ID, Payload: events.MessagePayload{Title: "Series added", Message: ser.Title}})
	s.bus.Changed("series", "created", ser.ID)
	return &ser, nil
}

// PurgePreviews deletes previews nobody has opened since before, with their
// chapters and progress. It returns how many went.
func (s *Service) PurgePreviews(ctx context.Context, before time.Time) (int, error) {
	var list []model.Series
	if err := s.db.NewSelect().Model(&list).Column("id", "work_id").Where("preview = ?", true).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("preview_seen_at IS NULL").WhereOr("preview_seen_at < ?", before)
		}).Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	for _, ser := range list {
		unlock := library.LockSeries(ser.ID)
		_, err := s.db.NewDelete().Model((*model.Series)(nil)).Where("id = ? AND preview = ?", ser.ID, true).Exec(ctx)
		if err == nil {
			_, _ = s.db.NewDelete().Model((*model.Work)(nil)).Where("id = ? AND NOT EXISTS (SELECT 1 FROM series WHERE work_id = ?)", ser.WorkID, ser.WorkID).Exec(ctx)
			n++
		}
		unlock()
		if err != nil {
			return n, err
		}
	}
	return n, nil
}
