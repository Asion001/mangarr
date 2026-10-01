package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
)

// WorkerKeyPrefix marks a key as a worker's, so one can be told from an
// account's at a glance (and refused outside the worker endpoints).
const WorkerKeyPrefix = "mgw_"

// NewWorkerKey returns a random worker key.
func NewWorkerKey() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return WorkerKeyPrefix + hex.EncodeToString(b)
}

// HashWorkerKey returns the stored form of a worker key: only the hash is
// kept, so a lost key can be replaced but never read back.
func HashWorkerKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// workers caches worker keys by hash, like sessions: a worker polls for
// tasks, so this is on the hot path.
type workers struct {
	mu       sync.Mutex
	loaded   bool
	byHash   map[string]model.Worker
	lastSeen map[int64]time.Time
}

// InvalidateWorkers makes the next request re-read the worker keys (after
// one is created, changed or deleted).
func (s *Service) InvalidateWorkers() {
	s.workers.mu.Lock()
	s.workers.loaded = false
	s.workers.mu.Unlock()
}

// loadWorkers fills the cache; the caller holds the lock.
func (s *Service) loadWorkers(ctx context.Context) bool {
	w := &s.workers
	if w.loaded {
		return true
	}
	var list []model.Worker
	if err := s.db.NewSelect().Model(&list).Scan(ctx); err != nil {
		return false
	}
	w.byHash = make(map[string]model.Worker, len(list))
	for _, x := range list {
		w.byHash[x.KeyHash] = x
	}
	w.loaded = true
	return true
}

// WorkerByKey returns the worker a key belongs to.
func (s *Service) WorkerByKey(ctx context.Context, key string) (model.Worker, bool) {
	s.workers.mu.Lock()
	defer s.workers.mu.Unlock()
	if !s.loadWorkers(ctx) {
		return model.Worker{}, false
	}
	w, ok := s.workers.byHash[HashWorkerKey(key)]
	return w, ok
}

// SwitchedOff reports whether r carries the key of a worker that is
// switched off. The key still doesn't authenticate; this only lets the
// worker endpoints tell that worker why, so it waits instead of giving up.
func (s *Service) SwitchedOff(r *http.Request) bool {
	key := r.Header.Get("X-Api-Key")
	if !strings.HasPrefix(key, WorkerKeyPrefix) {
		return false
	}
	w, ok := s.WorkerByKey(r.Context(), key)
	return ok && !w.Enabled
}

// touchWorker records that a worker was here, at most once a minute.
func (s *Service) touchWorker(id int64, ip string) {
	w := &s.workers
	w.mu.Lock()
	if w.lastSeen == nil {
		w.lastSeen = map[int64]time.Time{}
	}
	if time.Since(w.lastSeen[id]) < time.Minute {
		w.mu.Unlock()
		return
	}
	w.lastSeen[id] = time.Now()
	w.mu.Unlock()
	go func() {
		now := time.Now().UTC()
		_, _ = s.db.NewUpdate().Model((*model.Worker)(nil)).Set("last_seen_at = ?", now).Set("last_ip = ?", ip).
			Where("id = ?", id).Exec(context.Background())
	}()
}

// ErrWorkerExists is returned when a name is already taken.
var ErrWorkerExists = errors.New("a worker with this name already exists")

// CreateWorker stores a worker and returns its key, which is the only time
// the key itself exists outside the machine that will hold it.
func (s *Service) CreateWorker(ctx context.Context, name string, roles []string, createdBy int64) (string, *model.Worker, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil, errors.New("a worker needs a name")
	}
	kept := make([]string, 0, len(roles))
	for _, r := range roles {
		for _, known := range model.WorkerRoles {
			if r == known && !contains(kept, r) {
				kept = append(kept, r)
			}
		}
	}
	if len(kept) == 0 {
		return "", nil, errors.New("a worker needs at least one role")
	}
	if n, err := s.db.NewSelect().Model((*model.Worker)(nil)).Where("name = ?", name).Count(ctx); err == nil && n > 0 {
		return "", nil, ErrWorkerExists
	}
	key := NewWorkerKey()
	w := &model.Worker{Name: name, KeyHash: HashWorkerKey(key), Prefix: key[:12], Roles: kept, Enabled: true, Priority: 100,
		Info: map[string]any{}, CreatedBy: createdBy, CreatedAt: time.Now().UTC()}
	if _, err := s.db.NewInsert().Model(w).Exec(ctx); err != nil {
		return "", nil, err
	}
	s.InvalidateWorkers()
	return key, w, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// WorkerPrincipal is who a worker is to the rest of the server: a caller
// with a name and no permissions at all, so every operation that isn't
// meant for workers denies it without a rule of its own.
func WorkerPrincipal(w model.Worker) *access.Principal {
	return &access.Principal{Kind: access.KindWorker, WorkerID: w.ID, Username: w.Name, DisplayName: w.Name,
		Perms: map[string]bool{}}
}
