package app

import (
	"context"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
)

// ClearProcessBacklog drops the background processing that hasn't started:
// queued and paused re-process jobs are removed, and every waiting chapter
// file is marked as settled under its profile's current settings so the
// backlog sweep leaves it alone. Running jobs finish. Changing the profile's
// processing settings, or Process existing, brings the chapters back.
func (a *App) ClearProcessBacklog(ctx context.Context) (jobs, chapters int, err error) {
	var profiles []model.Profile
	if err := a.DB.NewSelect().Model(&profiles).Scan(ctx); err != nil {
		return 0, 0, err
	}
	err = a.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		res, err := tx.NewDelete().Model((*model.DownloadJob)(nil)).
			Where("kind = ?", model.JobKindReprocess).
			Where("status IN (?)", bun.In([]string{model.JobQueued, model.JobPaused})).Exec(ctx)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		jobs = int(n)
		for _, p := range profiles {
			params := p.Config.ProcessParams()
			if params == "" {
				continue
			}
			res, err := tx.NewUpdate().Model((*model.ChapterFile)(nil)).Set("process_params = ?", params).
				Where("process_params <> ?", params).
				Where("series_id IN (SELECT id FROM series WHERE profile_id = ?)", p.ID).
				Where("process_attempts < ?", downloads.MaxProcessAttempts).
				Where("NOT EXISTS (SELECT 1 FROM download_jobs j WHERE j.chapter_id = chapter_file.chapter_id AND j.status IN (?))", bun.In(downloads.ActiveStatuses())).
				Exec(ctx)
			if err != nil {
				return err
			}
			n, _ := res.RowsAffected()
			chapters += int(n)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	a.Bus.Changed("queue", "sync", 0)
	return jobs, chapters, nil
}
