package readsync

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
)

// WatchStatus describes one account's live connection.
type WatchStatus struct {
	AccountID   int64      `json:"accountId"`
	Connected   bool       `json:"connected"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
	LastEventAt *time.Time `json:"lastEventAt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// Watcher keeps a live connection to every library account whose server
// pushes progress changes (Komga), applying them as they come.
type Watcher struct {
	s *Syncer
	// OnChange is called after live changes were stored (debounced by the caller).
	OnChange func(seriesID int64)
	// ResyncDelay batches resync requests per account.
	ResyncDelay time.Duration
	// Backoff limits reconnect attempts.
	MinBackoff, MaxBackoff time.Duration

	mu      sync.Mutex
	running map[int64]*watch // account id
	status  map[int64]*WatchStatus
}

type watch struct {
	cancel context.CancelFunc
	key    string // module + credentials; a change restarts the watch
}

// NewWatcher returns a watcher for s.
func NewWatcher(s *Syncer) *Watcher {
	return &Watcher{s: s, ResyncDelay: 5 * time.Second, MinBackoff: 5 * time.Second, MaxBackoff: 5 * time.Minute,
		running: map[int64]*watch{}, status: map[int64]*WatchStatus{}}
}

// Status reports the live accounts.
func (w *Watcher) Status() map[int64]WatchStatus {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make(map[int64]WatchStatus, len(w.status))
	for id, st := range w.status {
		out[id] = *st
	}
	return out
}

// Run starts and stops account watches as accounts and modules change,
// checking every interval, until ctx ends.
func (w *Watcher) Run(ctx context.Context, interval time.Duration) {
	w.Refresh(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for _, r := range w.running {
				r.cancel()
			}
			w.mu.Unlock()
			return
		case <-t.C:
			w.Refresh(ctx)
		}
	}
}

// Refresh reconciles running watches with the library accounts.
func (w *Watcher) Refresh(ctx context.Context) {
	var accounts []model.LibraryAccount
	if err := w.s.db.NewSelect().Model(&accounts).Scan(ctx); err != nil {
		return
	}
	want := map[int64]model.LibraryAccount{}
	keys := map[int64]string{}
	for _, acc := range accounts {
		if _, def, err := modules.GetAs[library.ProgressWatcher](w.s.mods, acc.ModuleID); err == nil {
			want[acc.ID] = acc
			// a module settings change (url, path mappings) reconnects
			keys[acc.ID] = watchKey(acc) + "|" + def.UpdatedAt.String()
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, r := range w.running {
		if _, ok := want[id]; !ok || r.key != keys[id] {
			r.cancel()
			delete(w.running, id)
			delete(w.status, id)
		}
	}
	for id, acc := range want {
		if _, ok := w.running[id]; ok {
			continue
		}
		wctx, cancel := context.WithCancel(ctx)
		w.running[id] = &watch{cancel: cancel, key: keys[id]}
		w.status[id] = &WatchStatus{AccountID: id}
		go w.loop(wctx, acc)
	}
}

// watchKey changes when an account's module or credentials change.
func watchKey(acc model.LibraryAccount) string {
	keys := make([]string, 0, len(acc.Credentials))
	for k := range acc.Credentials {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	key := strconv.FormatInt(acc.ModuleID, 10)
	for _, k := range keys {
		key += "|" + k + "=" + acc.Credentials[k]
	}
	return key
}

func (w *Watcher) setStatus(id int64, fn func(*WatchStatus)) {
	w.mu.Lock()
	if st, ok := w.status[id]; ok {
		fn(st)
	}
	w.mu.Unlock()
}

// loop keeps one account connected, reconnecting with backoff.
func (w *Watcher) loop(ctx context.Context, acc model.LibraryAccount) {
	backoff := w.MinBackoff
	var resyncMu sync.Mutex
	var resync *time.Timer
	scheduleResync := func() {
		resyncMu.Lock()
		defer resyncMu.Unlock()
		if resync != nil {
			return
		}
		resync = time.AfterFunc(w.ResyncDelay, func() {
			resyncMu.Lock()
			resync = nil
			resyncMu.Unlock()
			if ctx.Err() != nil {
				return
			}
			a := acc
			if n, err := w.s.SyncAccount(ctx, &a); err != nil {
				w.s.log.Warn("live read sync: full sync failed", "account", acc.ID, "err", err)
			} else if n > 0 && w.OnChange != nil {
				w.OnChange(0)
			}
		})
	}
	for ctx.Err() == nil {
		pw, _, err := modules.GetAs[library.ProgressWatcher](w.s.mods, acc.ModuleID)
		if err != nil {
			return
		}
		started := time.Now()
		err = pw.WatchProgress(ctx, library.Account{Credentials: acc.Credentials}, func(ev library.ProgressEvent) {
			now := time.Now().UTC()
			w.setStatus(acc.ID, func(st *WatchStatus) {
				if !st.Connected {
					st.Connected, st.ConnectedAt, st.Error = true, &now, ""
				}
				if !ev.Resync || ev.Book != nil {
					st.LastEventAt = &now
				}
			})
			if ev.Resync {
				scheduleResync()
				return
			}
			a := acc
			seriesID, err := w.s.ApplyEvent(ctx, &a, ev)
			if err != nil {
				w.s.log.Warn("live read sync", "account", acc.ID, "err", err)
				return
			}
			if seriesID != 0 && w.OnChange != nil {
				w.OnChange(seriesID)
			}
		})
		if ctx.Err() != nil {
			return
		}
		msg := "disconnected"
		if err != nil {
			msg = err.Error()
		}
		w.setStatus(acc.ID, func(st *WatchStatus) { st.Connected, st.Error = false, msg })
		if time.Since(started) > time.Minute {
			backoff = w.MinBackoff // it was up for a while: reconnect soon
		}
		w.s.log.Debug("live read sync disconnected", "account", acc.ID, "err", msg, "retry", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, w.MaxBackoff)
	}
}
