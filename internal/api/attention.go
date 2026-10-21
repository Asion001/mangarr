package api

import (
	"context"
	"net/http"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerAttention) }

// staleWorkerAfter is how long an enabled worker may go without asking for
// work before it counts as stopped.
const staleWorkerAfter = 10 * time.Minute

// AttentionCount is how many of something one source had.
type AttentionCount struct {
	Source string `json:"source" doc:"moduleId:sourceId"`
	Name   string `json:"name"`
	Count  int    `json:"count"`
}

// AttentionSource is a source that cools down or fails.
type AttentionSource struct {
	Source        string     `json:"source" doc:"moduleId:sourceId"`
	Name          string     `json:"name"`
	CooldownUntil *time.Time `json:"cooldownUntil,omitempty"`
	Reason        string     `json:"reason,omitempty"`
	Requests      int        `json:"requests"`
	Failures      int        `json:"failures"`
}

// AttentionWorker is a worker that stopped asking for work.
type AttentionWorker struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// Attention sums up what an admin may need to act on.
type Attention struct {
	FailedDownloads struct {
		Count   int              `json:"count" doc:"Download entries that failed in the last 24 hours"`
		Sources []AttentionCount `json:"sources"`
	} `json:"failedDownloads"`
	Sources struct {
		Unhealthy []AttentionSource `json:"unhealthy"`
	} `json:"sources"`
	Backups struct {
		Count          int        `json:"count"`
		LatestAt       *time.Time `json:"latestAt,omitempty"`
		LatestName     string     `json:"latestName,omitempty"`
		LatestVerified bool       `json:"latestVerified"`
		LastVerifiedAt *time.Time `json:"lastVerifiedAt,omitempty"`
	} `json:"backups"`
	Workers struct {
		Enabled int               `json:"enabled"`
		Stale   []AttentionWorker `json:"stale"`
	} `json:"workers"`
	Processing struct {
		Failed  int `json:"failed" doc:"Reprocessing entries that failed in the last 24 hours"`
		Running int `json:"running"`
		Waiting int `json:"waiting"`
	} `json:"processing"`
	Disk struct {
		Path    string `json:"path,omitempty" doc:"The root folder with the least free space"`
		Free    int64  `json:"free"`
		Total   int64  `json:"total"`
		Low     bool   `json:"low"`
		Folders int    `json:"folders"`
	} `json:"disk"`
}

func (s *Server) registerAttention() {
	huma.Register(s.api, huma.Operation{OperationID: "system-attention", Method: http.MethodGet, Path: "/api/v1/system/attention", Tags: []string{"System"},
		Summary: "Failed downloads, unhealthy sources, unverified backups, stopped workers, processing and disk space at a glance"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body Attention }, error) {
			a, err := s.attention(ctx)
			return &struct{ Body Attention }{a}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "catalogs-reliability", Method: http.MethodGet, Path: "/api/v1/catalogs/reliability", Tags: []string{"Catalogs"},
		Summary: "How each catalog answered requests over the last day or week: success, response times, cooldowns, fallbacks and recent failures"},
		func(ctx context.Context, in *struct {
			Days int `query:"days" default:"1" enum:"1,7"`
		}) (*struct{ Body []catalogs.Reliability }, error) {
			step := time.Hour
			if in.Days == 7 {
				step = 6 * time.Hour
			} else {
				in.Days = 1
			}
			list, err := s.app.Catalogs.Reliability(ctx, time.Now().Add(-time.Duration(in.Days)*24*time.Hour), step)
			if list == nil {
				list = []catalogs.Reliability{}
			}
			return &struct{ Body []catalogs.Reliability }{list}, toHTTPError(err)
		})
}

func (s *Server) attention(ctx context.Context) (Attention, error) {
	var a Attention
	day := time.Now().UTC().Add(-24 * time.Hour)

	var failed []struct {
		ModuleID   int64  `bun:"module_id"`
		SourceID   string `bun:"source_id"`
		SourceName string `bun:"source_name"`
		N          int    `bun:"n"`
	}
	err := s.app.DB.NewSelect().TableExpr("download_jobs AS j").
		Join("LEFT JOIN chapter_releases AS r ON r.id = j.release_id").
		Join("LEFT JOIN series_sources AS ss ON ss.id = r.series_source_id").
		ColumnExpr("COALESCE(ss.module_id, 0) AS module_id, COALESCE(ss.source_id, '') AS source_id, COALESCE(ss.source_name, '') AS source_name, COUNT(*) AS n").
		Where("j.kind = ?", model.JobKindDownload).Where("j.status = ?", model.JobFailed).Where("j.updated_at > ?", day).
		GroupExpr("ss.module_id, ss.source_id, ss.source_name").Scan(ctx, &failed)
	if err != nil {
		return a, err
	}
	a.FailedDownloads.Sources = []AttentionCount{}
	for _, f := range failed {
		a.FailedDownloads.Count += f.N
		if f.SourceID != "" {
			a.FailedDownloads.Sources = append(a.FailedDownloads.Sources, AttentionCount{Source: catalogs.Key(f.ModuleID, f.SourceID), Name: f.SourceName, Count: f.N})
		}
	}
	sort.SliceStable(a.FailedDownloads.Sources, func(i, j int) bool { return a.FailedDownloads.Sources[i].Count > a.FailedDownloads.Sources[j].Count })

	rel, err := s.app.Catalogs.Reliability(ctx, time.Now().Add(-24*time.Hour), time.Hour)
	if err != nil {
		return a, err
	}
	a.Sources.Unhealthy = []AttentionSource{}
	for _, r := range rel {
		if r.CooldownUntil == nil && !r.Failing() {
			continue
		}
		a.Sources.Unhealthy = append(a.Sources.Unhealthy, AttentionSource{Source: catalogs.Key(r.ModuleID, r.SourceID), Name: r.Name,
			CooldownUntil: r.CooldownUntil, Reason: r.CooldownReason, Requests: r.Requests, Failures: r.Failures})
	}

	if list, err := s.app.Backups.List(); err == nil {
		for _, b := range list {
			if b.Type == "export" {
				continue // made for another server, not to restore this one
			}
			a.Backups.Count++
			if a.Backups.LatestAt == nil {
				at := b.Created.UTC()
				a.Backups.LatestAt, a.Backups.LatestName, a.Backups.LatestVerified = &at, b.Name, b.Verification != nil
			}
			if b.Verification != nil && (a.Backups.LastVerifiedAt == nil || b.Verification.VerifiedAt.After(*a.Backups.LastVerifiedAt)) {
				at := b.Verification.VerifiedAt.UTC()
				a.Backups.LastVerifiedAt = &at
			}
		}
	}

	var workers []model.Worker
	if err := s.app.DB.NewSelect().Model(&workers).Column("id", "name", "last_seen_at").Where("enabled = ?", true).Order("name").Scan(ctx); err != nil {
		return a, err
	}
	a.Workers.Enabled = len(workers)
	a.Workers.Stale = []AttentionWorker{}
	for _, w := range workers {
		// one that never connected (its key not set up yet) isn't working either
		if w.LastSeenAt == nil || time.Since(*w.LastSeenAt) > staleWorkerAfter {
			a.Workers.Stale = append(a.Workers.Stale, AttentionWorker{ID: w.ID, Name: w.Name, LastSeenAt: w.LastSeenAt})
		}
	}

	var jobs []struct {
		Status string `bun:"status"`
		N      int    `bun:"n"`
	}
	if err := s.app.DB.NewSelect().Model((*model.DownloadJob)(nil)).ColumnExpr("status, COUNT(*) AS n").
		Where("kind = ?", model.JobKindReprocess).
		Where("status <> ? OR updated_at > ?", model.JobFailed, day).
		Where("status <> ?", model.JobCompleted).
		Group("status").Scan(ctx, &jobs); err != nil {
		return a, err
	}
	for _, j := range jobs {
		switch j.Status {
		case model.JobFailed:
			a.Processing.Failed += j.N
		case model.JobQueued, model.JobPaused:
			a.Processing.Waiting += j.N
		default:
			a.Processing.Running += j.N
		}
	}

	var roots []model.RootFolder
	if err := s.app.DB.NewSelect().Model(&roots).Scan(ctx); err != nil {
		return a, err
	}
	mm, _ := s.app.Settings.MediaManagement(ctx)
	a.Disk.Folders = len(roots)
	first := true
	for _, r := range roots {
		total, free, _, err := fsutil.Usage(r.Path)
		if err != nil {
			continue
		}
		if first || int64(free) < a.Disk.Free {
			a.Disk.Path, a.Disk.Free, a.Disk.Total = r.Path, int64(free), int64(total)
			first = false
		}
	}
	// the same line the health check draws
	a.Disk.Low = !first && mm.MinFreeSpaceMB > 0 && a.Disk.Free < int64(mm.MinFreeSpaceMB)*2<<20
	return a, nil
}
