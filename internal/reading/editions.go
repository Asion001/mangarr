package reading

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

// Progress belongs to a title, so a change reported on one language
// edition's chapter is also a change of the same-numbered chapter of the
// title's other editions.

// editionsBy announces those. They come from mangarr, not from the server
// that reported the original, so that server hears about them too.
var editionsBy = By{Origin: model.EventOriginApp, Client: "Language editions"}

// announceEditions announces the applied changes in out for the same
// chapters of the titles' other editions, so reading apps and library
// servers showing those editions follow. It returns their series.
func (s *Service) announceEditions(ctx context.Context, readerID int64, out []Outcome) map[int64]bool {
	results := map[int64]Outcome{}
	var ids []int64
	for _, o := range out {
		if o.Result == model.OutcomeApplied || o.Result == model.OutcomeUnread {
			if _, ok := results[o.ChapterID]; !ok {
				ids = append(ids, o.ChapterID)
			}
			results[o.ChapterID] = o
		}
	}
	var others []Outcome
	for start := 0; start < len(ids); start += 500 {
		var rows []struct {
			From      int64 `bun:"from_id"`
			ChapterID int64 `bun:"chapter_id"`
			SeriesID  int64 `bun:"series_id"`
		}
		err := s.DB.NewSelect().TableExpr("chapters AS c").
			Join("JOIN series AS s ON s.id = c.series_id").
			Join("JOIN series AS s2 ON s2.work_id = s.work_id AND s2.id <> s.id").
			Join("JOIN chapters AS c2 ON c2.series_id = s2.id AND c2.number_key = c.number_key").
			ColumnExpr("c.id AS from_id, c2.id AS chapter_id, c2.series_id").
			Where("c.id IN (?)", bun.In(ids[start:min(start+500, len(ids))])).
			OrderExpr("c2.series_id, c2.number_sort, c2.id").Scan(ctx, &rows)
		if err != nil {
			s.Log.Warn("other editions not told about reading progress", "reader", readerID, "error", err)
			return nil
		}
		for _, r := range rows {
			if _, reported := results[r.ChapterID]; reported {
				continue
			}
			o := results[r.From]
			o.ChapterID, o.SeriesID = r.ChapterID, r.SeriesID
			others = append(others, o)
		}
	}
	s.announce(readerID, others, editionsBy)
	changed := map[int64]bool{}
	for _, o := range others {
		changed[o.SeriesID] = true
	}
	return changed
}

// PurgeOrphans deletes the progress of titles that are gone.
func (s *Service) PurgeOrphans(ctx context.Context) error {
	return readstate.Purge(ctx, s.DB)
}
