package model

import (
	"slices"
	"time"

	"github.com/uptrace/bun"
)

// Worker roles. A worker may hold several: the all-in-one image can be a
// downloader and an encoder at once.
const (
	// RoleDownload fetches a chapter's pages and uploads them here, which
	// spreads downloading across machines (and addresses).
	RoleDownload = "download"
	// RoleUpscale runs an upscaler on pages.
	RoleUpscale = "upscale"
	// RoleEncode re-encodes pages (AVIF, JPEG XL).
	RoleEncode = "encode"
)

// WorkerRoles are the roles a worker can be given, in the order the UI
// shows them.
var WorkerRoles = []string{RoleDownload, RoleUpscale, RoleEncode}

// Worker is a machine that asks this server for work. It holds a key of its
// own, so it can be switched off or removed without touching anything else,
// and its counters are what System → Workers shows.
type Worker struct {
	bun.BaseModel `bun:"table:workers"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Name          string `bun:"name,notnull" json:"name"`
	KeyHash       string `bun:"key_hash,notnull" json:"-"`
	// Prefix is the start of the key, so a worker can be told apart in the UI.
	Prefix  string   `bun:"prefix,notnull" json:"prefix"`
	Roles   []string `bun:"roles,type:jsonb,notnull" json:"roles"`
	Enabled bool     `bun:"enabled,notnull" json:"enabled"`
	// Priority orders workers in the remote pool (lower first).
	Priority int `bun:"priority,notnull" json:"priority"`
	// Concurrent overrides the installation default; 0 inherits it.
	Concurrent int `bun:"concurrent,notnull" json:"concurrent"`
	// PageConcurrency is how many pages it fetches at a time within one
	// download; 0 leaves it to the worker (MANGARR_WORKER_PAGE_CONCURRENCY).
	PageConcurrency int `bun:"page_concurrency,notnull" json:"pageConcurrency"`
	// UpscaleModel is what this worker upscales with, in place of the
	// profile's model; empty (or a model it does not have) uses the profile's.
	UpscaleModel string `bun:"upscale_model,notnull" json:"upscaleModel"`
	// Version, Platform and Info are what the worker said about itself when
	// it last said hello (its build, its OS, its upscaling devices).
	Version  string         `bun:"version,notnull" json:"version"`
	Platform string         `bun:"platform,notnull" json:"platform"`
	Info     map[string]any `bun:"info,type:jsonb,notnull" json:"info"`
	LastIP   string         `bun:"last_ip,notnull" json:"lastIp"`
	// CreatedBy is the account that made the key (0 when it came from the
	// admin API key).
	CreatedBy   int64      `bun:"created_by,nullzero" json:"createdBy,omitempty"`
	CreatedAt   time.Time  `bun:"created_at,notnull" json:"createdAt"`
	LastSeenAt  *time.Time `bun:"last_seen_at" json:"lastSeenAt,omitempty"`
	TasksDone   int64      `bun:"tasks_done,notnull" json:"tasksDone"`
	TasksFailed int64      `bun:"tasks_failed,notnull" json:"tasksFailed"`
	PagesDone   int64      `bun:"pages_done,notnull" json:"pagesDone"`
	BytesIn     int64      `bun:"bytes_in,notnull" json:"bytesIn"`
	BytesOut    int64      `bun:"bytes_out,notnull" json:"bytesOut"`
	BusySeconds float64    `bun:"busy_seconds,notnull" json:"busySeconds"`
}

// HasRole reports whether the worker may do this kind of work.
func (w *Worker) HasRole(role string) bool {
	for _, r := range w.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// InfoBuild and InfoCommit are where a worker's hello records the CI run
// and commit it was built from.
const (
	InfoBuild  = "build"
	InfoCommit = "commit"
)

// InfoRoles is where a worker's hello records the roles it offered: what
// its own configuration (MANGARR_WORKER_ROLES) lets it do.
const InfoRoles = "roles"

// Offers reports whether the worker may do this kind of work and said it
// would. A role given here that the worker itself was not set up for is
// never asked for, so work handed to it on that role would wait forever. A
// worker whose hello did not say keeps every role it was given.
func (w *Worker) Offers(role string) bool {
	if !w.HasRole(role) {
		return false
	}
	switch offered := w.Info[InfoRoles].(type) {
	case []string:
		return slices.Contains(offered, role)
	case []any:
		return slices.Contains(offered, any(role))
	}
	return true
}

// Worker task kinds and states.
const (
	TaskDownload = "download"
	TaskUpscale  = "upscale"
	TaskEncode   = "encode"

	// TaskPending is waiting for a worker to take it.
	TaskPending = "pending"
	// TaskLeased is being worked on; the lease is renewed by heartbeats.
	TaskLeased = "leased"
	TaskDone   = "done"
	TaskFailed = "failed"
	// TaskAbandoned: nobody finished it after several tries.
	TaskAbandoned = "abandoned"
)

// WorkerTask is one piece of a download job handed to a worker: fetching a
// chapter's pages, or processing a slice of them. Its finished rows are the
// history the worker statistics are counted from.
type WorkerTask struct {
	bun.BaseModel `bun:"table:worker_tasks"`
	ID            int64 `bun:"id,pk,autoincrement" json:"id"`
	JobID         int64 `bun:"job_id,notnull" json:"jobId"`
	// WorkerID is who holds it (0 while it waits).
	WorkerID int64  `bun:"worker_id,nullzero" json:"workerId,omitempty"`
	Kind     string `bun:"kind,notnull" json:"kind"`
	// Seq orders the tasks of one job (chunk 0, 1, 2...).
	Seq   int    `bun:"seq,notnull" json:"seq"`
	State string `bun:"state,notnull" json:"state"`
	// Spec is what the worker needs to do it: page URLs and headers for a
	// download, the processing settings for the rest.
	Spec map[string]any `bun:"spec,type:jsonb,notnull" json:"spec"`
	// NotBefore keeps a task waiting (the source's own pacing).
	NotBefore time.Time `bun:"not_before,notnull" json:"notBefore"`
	// LeaseUntil is when the task returns to the queue unless the worker
	// says it is still alive.
	LeaseUntil  *time.Time `bun:"lease_until" json:"leaseUntil,omitempty"`
	HeartbeatAt *time.Time `bun:"heartbeat_at" json:"heartbeatAt,omitempty"`
	// Cancel asks the worker to stop at its next heartbeat.
	Cancel bool `bun:"cancel,notnull" json:"cancel"`
	// Attempt counts how often this task has been handed out.
	Attempt    int        `bun:"attempt,notnull" json:"attempt"`
	PagesTotal int        `bun:"pages_total,notnull" json:"pagesTotal"`
	PagesDone  int        `bun:"pages_done,notnull" json:"pagesDone"`
	BytesIn    int64      `bun:"bytes_in,notnull" json:"bytesIn"`
	BytesOut   int64      `bun:"bytes_out,notnull" json:"bytesOut"`
	Error      string     `bun:"error,notnull" json:"error"`
	CreatedAt  time.Time  `bun:"created_at,notnull" json:"createdAt"`
	StartedAt  *time.Time `bun:"started_at" json:"startedAt,omitempty"`
	FinishedAt *time.Time `bun:"finished_at" json:"finishedAt,omitempty"`
}

// Open reports whether the task still has to be done.
func (t *WorkerTask) Open() bool { return t.State == TaskPending || t.State == TaskLeased }
