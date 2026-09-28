package downloads

import (
	"context"
	"errors"
	"os"

	"github.com/Asion001/mangarr/internal/decision"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
	"github.com/Asion001/mangarr/internal/sourcegov"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// jobCtx bundles the records passed between fetch, processing and import.
// release and link may be absent until a candidate is selected; file is the
// existing import, when present. job points to the attempt being run.
type jobCtx struct {
	job       *model.DownloadJob
	series    model.Series
	profile   model.Profile
	chapter   model.Chapter
	release   *model.ChapterRelease
	link      *model.SeriesSource
	file      *model.ChapterFile
	inputFile *model.ChapterFile
}

func (m *Manager) load(ctx context.Context, job *model.DownloadJob) (*jobCtx, error) {
	jc := &jobCtx{job: job}
	if err := m.db.NewSelect().Model(&jc.chapter).Where("id = ?", job.ChapterID).Scan(ctx); err != nil {
		return nil, err
	}
	if err := m.db.NewSelect().Model(&jc.series).Where("id = ?", job.SeriesID).Scan(ctx); err != nil {
		return nil, err
	}
	if err := m.db.NewSelect().Model(&jc.profile).Where("id = ?", jc.series.ProfileID).Scan(ctx); err != nil {
		return nil, err
	}
	if job.ConfigOverride != nil {
		jc.profile.Config = *job.ConfigOverride
	}
	if job.ProfileName != "" {
		jc.profile.Name = job.ProfileName
	}
	if jc.chapter.FileID != nil {
		var f model.ChapterFile
		if err := m.db.NewSelect().Model(&f).Where("id = ?", *jc.chapter.FileID).Scan(ctx); err == nil {
			jc.file = &f
		}
	}
	if job.ReleaseID != nil {
		var r model.ChapterRelease
		if err := m.db.NewSelect().Model(&r).Where("id = ?", *job.ReleaseID).Scan(ctx); err == nil {
			jc.release = &r
			var ss model.SeriesSource
			if err := m.db.NewSelect().Model(&ss).Where("id = ?", r.SeriesSourceID).Scan(ctx); err == nil {
				jc.link = &ss
			}
		}
	}
	if job.PinRelease && (jc.release == nil || jc.link == nil || jc.release.Removed || !jc.link.Enabled) {
		return nil, permanent(errors.New("pinned release is no longer available"))
	}
	return jc, nil
}

// run claims a local attempt and owns its staging directory until finish returns.
// Stage errors go through fail; worker downloads join the pipeline at finish.
func (m *Manager) run(ctx context.Context, job model.DownloadJob) {
	if !m.claim(ctx, &job) {
		return
	}
	m.Live.Start(job.ID, job.Kind)
	defer m.Live.Finish(job.ID)
	ctx = progress.With(ctx, m.Live.Reporter(job.ID))
	ctx = worktasks.WithJob(ctx, job.ID) // work started deeper in belongs to this job
	log := m.log.With("job", job.ID, "chapterId", job.ChapterID)
	jc, err := m.load(ctx, &job)
	if err != nil {
		log.Error("load job", "err", err)
		m.fail(ctx, &job, nil, err)
		return
	}
	workDir := m.workDir(job.ID)
	defer os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o775); err != nil {
		m.fail(ctx, &job, jc, infraError{err})
		return
	}
	var pages []PageFile
	switch job.Kind {
	case model.JobKindReprocess:
		pages, err = m.extractExisting(jc, workDir)
	default:
		if jc.release == nil || jc.link == nil {
			cand, upgrade, cerr := m.nextCandidate(ctx, jc)
			if cerr != nil || cand == nil {
				m.fail(ctx, &job, jc, permanent(errors.New("no downloadable release left for this chapter")))
				return
			}
			rel, link := cand.Release, cand.Source
			jc.release, jc.link, job.ReleaseID, job.IsUpgrade = &rel, &link, &rel.ID, upgrade
			_, _ = m.db.NewUpdate().Model(&job).Column("release_id", "is_upgrade").WherePK().Exec(ctx)
		}
		m.setStatus(ctx, &job, model.JobDownloading, model.ChapterDownloading)
		if m.Gov != nil {
			// random pause between chapters of the same catalog
			if err := m.Gov.Pace(ctx, sourcegov.Key{ModuleID: jc.link.ModuleID, SourceID: jc.link.SourceID}, "chapter"); err != nil {
				m.fail(ctx, &job, jc, err)
				return
			}
		}
		pages, err = m.fetchPages(ctx, jc, workDir)
	}
	if err != nil {
		m.fail(ctx, &job, jc, err)
		return
	}
	m.finish(ctx, job, jc, pages, workDir)
}

func (m *Manager) nextCandidate(ctx context.Context, jc *jobCtx) (*decision.Candidate, bool, error) {
	return m.searcher.NextCandidateConfigured(ctx, jc.series.ID, jc.chapter.ID, jc.job.ForceDownload, jc.job.ConfigOverride)
}
