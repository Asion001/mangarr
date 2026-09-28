package downloads

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/sourcegov"
)

// permanentError marks failures that should blocklist the release.
type permanentError struct{ error }

func permanent(err error) error { return permanentError{err} }

// infraError marks failures of our own infrastructure (engine down, disk full)
// that must never blocklist a release.
type infraError struct{ error }

func classify(err error) error {
	if err == nil {
		return nil
	}
	var ne net.Error
	msg := strings.ToLower(err.Error())
	if errors.As(err, &ne) || strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "unauthorized (check") {
		return infraError{err}
	}
	return err
}

var retryDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// fail handles a failed attempt: retry, fall back to another release, or fail.
func (m *Manager) fail(ctx context.Context, job *model.DownloadJob, jc *jobCtx, err error) {
	if ctx.Err() != nil {
		// cancelled by the user or shutting down; the queue entry decides what happens
		return
	}
	bg := context.Background()
	dl, _ := m.settings.Downloads(bg)
	maxAttempts := dl.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	now := time.Now().UTC()
	job.Error = err.Error()
	job.UpdatedAt = now
	// the site throttled us: retry when the catalog's cooldown ends, without
	// counting it against the release
	if until := m.cooldownUntil(err, jc); !until.IsZero() {
		job.Status, job.NotBefore = model.JobQueued, until
		_, _ = m.db.NewUpdate().Model(job).Column("status", "error", "not_before", "updated_at", "release_id").WherePK().Where("status <> ?", model.JobPaused).Exec(bg)
		m.log.Info("source is cooling down, download postponed", "job", job.ID, "until", until.Local().Format(time.TimeOnly))
		m.bus.Changed("queue", "updated", job.ID)
		return
	}
	job.Attempt++
	var perm permanentError
	var infra infraError
	isPerm := errors.As(err, &perm)
	isInfra := errors.As(err, &infra)
	m.log.Warn("download attempt failed", "job", job.ID, "attempt", job.Attempt, "err", err, "permanent", isPerm)

	requeue := func(delay time.Duration) {
		job.Status, job.NotBefore = model.JobQueued, now.Add(delay)
		_, _ = m.db.NewUpdate().Model(job).Column("status", "attempt", "error", "not_before", "updated_at", "release_id").WherePK().Where("status <> ?", model.JobPaused).Exec(bg)
		m.bus.Changed("queue", "updated", job.ID)
	}
	switch {
	case isInfra:
		// our side is broken: keep retrying with growing delay, never blocklist
		d := time.Duration(job.Attempt) * 5 * time.Minute
		if d > time.Hour {
			d = time.Hour
		}
		requeue(d)
		return
	case !isPerm && job.Attempt < maxAttempts && job.Kind == model.JobKindDownload:
		requeue(retryDelays[min(job.Attempt-1, len(retryDelays)-1)])
		return
	}
	if job.Kind == model.JobKindDownload && job.ReleaseID != nil {
		if berr := BlocklistRelease(bg, m.db, *job.ReleaseID, err.Error()); berr != nil {
			m.log.Warn("blocklist release", "err", berr)
		}
		if jc != nil && !job.PinRelease {
			if cand, upgrade, cerr := m.nextCandidate(bg, jc); cerr == nil && cand != nil {
				rid := cand.Release.ID
				job.ReleaseID, job.IsUpgrade, job.Attempt = &rid, upgrade, 0
				_, _ = m.db.NewUpdate().Model(job).Column("is_upgrade").WherePK().Exec(bg)
				m.log.Info("trying next release", "job", job.ID, "source", cand.Source.SourceName, "scanlator", cand.Release.Scanlator)
				requeue(5 * time.Second)
				return
			}
		}
	}
	job.Status = model.JobFailed
	_, _ = m.db.NewUpdate().Model(job).Column("status", "attempt", "error", "updated_at").WherePK().Where("status <> ?", model.JobPaused).Exec(bg)
	if job.Kind == model.JobKindDownload {
		_ = resetChapterState(bg, m.db, job.ChapterID)
		var ch model.Chapter
		if m.db.NewSelect().Model(&ch).Where("id = ?", job.ChapterID).Scan(bg) == nil && ch.FileID == nil {
			_, _ = m.db.NewUpdate().Model((*model.Chapter)(nil)).Set("state = ?", model.ChapterFailed).Where("id = ?", ch.ID).Exec(bg)
		}
	}
	if jc != nil {
		chID := jc.chapter.ID
		_ = history.Record(bg, m.db, jc.series.ID, &chID, model.HistoryFailed, "", map[string]string{"error": err.Error()})
		m.bus.Publish(events.Event{Type: events.DownloadFailed, SeriesID: jc.series.ID, Payload: events.MessagePayload{
			Title:   "Download failed",
			Message: fmt.Sprintf("%s ch. %s: %v", jc.series.Title, jc.chapter.NumberKey, err)}})
	}
	m.bus.Changed("queue", "updated", job.ID)
	m.bus.Changed("chapter", "updated", job.ChapterID)
}

// cooldownUntil returns when a throttled catalog may be used again (zero
// when err isn't about throttling).
func (m *Manager) cooldownUntil(err error, jc *jobCtx) time.Time {
	if cd, ok := sourcegov.CoolingDown(err); ok {
		return cd.Until
	}
	if m.Gov == nil || jc == nil || jc.link == nil {
		return time.Time{}
	}
	if _, throttled := sourcegov.Classify(err); !throttled {
		return time.Time{}
	}
	until, _ := m.Gov.Cooldown(sourcegov.Key{ModuleID: jc.link.ModuleID, SourceID: jc.link.SourceID})
	return until
}
