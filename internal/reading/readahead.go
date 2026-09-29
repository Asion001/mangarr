package reading

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/model"
)

// readAheadRecent is how recent a reader's position must be to look ahead.
const readAheadRecent = 14 * 24 * time.Hour

// ReadAhead makes sure the chapters after a reader's position in a series
// get downloaded (reading.readAhead): the next N chapters after the
// furthest one the reader has progress on are monitored, even in series
// that aren't, and searched, as long as the reader got there in the last
// two weeks. It returns how many downloads were queued.
func (s *Service) ReadAhead(ctx context.Context, readerID, seriesID int64) (int, error) {
	cfg, err := s.Settings.Reading(ctx)
	if err != nil || !cfg.ReadAhead.Enabled || cfg.ReadAhead.Chapters <= 0 || s.Downloads == nil {
		return 0, err
	}
	if preview, err := s.DB.NewSelect().Model((*model.Series)(nil)).Where("id = ? AND preview = ?", seriesID, true).Exists(ctx); err != nil || preview {
		return 0, err // a preview isn't monitored or downloaded
	}
	var pos []struct {
		Number float64      `bun:"number_sort"`
		At     bun.NullTime `bun:"at"`
	}
	if err := s.DB.NewSelect().TableExpr("chapter_read_states AS rs").Join("JOIN chapters AS c ON c.id = rs.chapter_id").
		ColumnExpr("c.number_sort, COALESCE(rs.read_at, rs.synced_at) AS at").Where("rs.reader_id = ? AND rs.series_id = ?", readerID, seriesID).
		Where("(rs.completed OR rs.page > 0)").OrderExpr("c.number_sort DESC").Limit(1).Scan(ctx, &pos); err != nil || len(pos) == 0 {
		return 0, err
	}
	// old history (a first sync with a library server) isn't reading now
	if pos[0].At.IsZero() || time.Since(pos[0].At.Time) > readAheadRecent {
		return 0, nil
	}
	furthest := sql.NullFloat64{Float64: pos[0].Number, Valid: true}
	var next []model.Chapter
	if err := s.DB.NewSelect().Model(&next).Where("series_id = ? AND number_sort > ?", seriesID, furthest.Float64).
		Order("number_sort", "id").Limit(cfg.ReadAhead.Chapters).Scan(ctx); err != nil {
		return 0, err
	}
	var want, newlyMonitored []int64
	var numbers []string
	for _, c := range next {
		if c.FileID != nil {
			continue
		}
		want = append(want, c.ID)
		if !c.Monitored {
			newlyMonitored = append(newlyMonitored, c.ID)
			numbers = append(numbers, c.NumberKey)
		}
	}
	if len(want) == 0 {
		return 0, nil
	}
	if len(newlyMonitored) > 0 {
		if _, err := s.DB.NewUpdate().Model((*model.Chapter)(nil)).Set("monitored = ?", true).Set("updated_at = ?", time.Now().UTC()).
			Where("id IN (?)", bun.In(newlyMonitored)).Exec(ctx); err != nil {
			return 0, err
		}
		_ = history.Record(ctx, s.DB, seriesID, nil, model.HistoryReadAhead, "",
			map[string]string{"chapters": strings.Join(numbers, ", "), "after": fmt.Sprint(furthest.Float64)})
		s.Bus.Changed("chapter", "updated", 0)
		s.Bus.Changed("series", "updated", seriesID)
	}
	n, err := s.Downloads.Evaluate(ctx, seriesID, want, true)
	if n > 0 {
		s.Log.Info("read ahead: downloads queued", "series", seriesID, "chapters", n)
	}
	return n, err
}
