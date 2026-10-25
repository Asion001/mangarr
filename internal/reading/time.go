package reading

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// RecordTime stores one web-reader session's cumulative active time. Repeated
// or out-of-order heartbeats can only increase the stored duration.
func (s *Service) RecordTime(ctx context.Context, userID, chapterID int64, sessionID string, activeSeconds int) error {
	var chapter model.Chapter
	if err := s.DB.NewSelect().Model(&chapter).Column("id", "series_id").Where("id = ?", chapterID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !s.CanSee(ctx, chapter.SeriesID) {
		return ErrNotFound
	}
	now := time.Now().UTC()
	session := &model.ReadingSession{ID: sessionID, UserID: userID, SeriesID: chapter.SeriesID, ChapterID: chapter.ID,
		ActiveSeconds: activeSeconds, StartedAt: now, UpdatedAt: now}
	if _, err := s.DB.NewInsert().Model(session).On("CONFLICT (id) DO NOTHING").Exec(ctx); err != nil {
		return err
	}
	_, err := s.DB.NewUpdate().Model((*model.ReadingSession)(nil)).
		Set("active_seconds = ?", activeSeconds).Set("updated_at = ?", now).
		Where("id = ? AND user_id = ? AND series_id = ? AND chapter_id = ? AND active_seconds < ?",
			sessionID, userID, chapter.SeriesID, chapter.ID, activeSeconds).Exec(ctx)
	return err
}
