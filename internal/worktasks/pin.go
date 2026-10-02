package worktasks

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// Spec keys that keep a task for some workers (upscale routes' targets).
const (
	SpecPinWorkers = "pinWorkers"
	SpecPinStrict  = "pinStrict"
	SpecPinModels  = "pinModels"
	// specPinWorker is how a pin to one worker was written before a pin
	// listed several.
	specPinWorker = "pinWorker"
)

// Pin keeps a task for a list of workers, in order: the first of them that
// is online and has room takes it. A strict pin waits for one of them
// however long they are away; otherwise the task goes to anyone while none
// of them is online.
type Pin struct {
	Workers []int64
	Strict  bool
	// Models are the upscale models the routes chose, by worker; a worker
	// without one runs its usual model.
	Models map[int64]string
}

// Has reports whether the pin lists a worker.
func (p Pin) Has(id int64) bool { return slices.Contains(p.Workers, id) }

type pinKey struct{}

// WithPin returns a context whose tasks are kept for some workers.
func WithPin(ctx context.Context, p Pin) context.Context {
	return context.WithValue(ctx, pinKey{}, p)
}

// PinFrom is the pin a context carries.
func PinFrom(ctx context.Context) (Pin, bool) {
	p, ok := ctx.Value(pinKey{}).(Pin)
	return p, ok && len(p.Workers) > 0
}

// Apply writes the pin into a task's spec.
func (p Pin) Apply(spec map[string]any) {
	if len(p.Workers) == 0 {
		return
	}
	spec[SpecPinWorkers], spec[SpecPinStrict] = p.Workers, p.Strict
	if len(p.Models) > 0 {
		models := make(map[string]string, len(p.Models))
		for id, m := range p.Models {
			models[strconv.FormatInt(id, 10)] = m
		}
		spec[SpecPinModels] = models
	}
}

// PinOf reads a task's pin; Workers is empty when it has none.
func PinOf(t *model.WorkerTask) Pin {
	var p Pin
	switch v := t.Spec[SpecPinWorkers].(type) {
	case []int64:
		p.Workers = v
	case []any:
		for _, x := range v {
			if id := asID(x); id > 0 {
				p.Workers = append(p.Workers, id)
			}
		}
	}
	if id := asID(t.Spec[specPinWorker]); id > 0 && len(p.Workers) == 0 {
		p.Workers = []int64{id}
	}
	p.Strict, _ = t.Spec[SpecPinStrict].(bool)
	switch v := t.Spec[SpecPinModels].(type) {
	case map[string]string:
		for k, m := range v {
			p.addModel(k, m)
		}
	case map[string]any:
		for k, m := range v {
			if s, ok := m.(string); ok {
				p.addModel(k, s)
			}
		}
	}
	return p
}

func (p *Pin) addModel(key, name string) {
	id, err := strconv.ParseInt(key, 10, 64)
	if err != nil || name == "" {
		return
	}
	if p.Models == nil {
		p.Models = map[int64]string{}
	}
	p.Models[id] = name
}

func asID(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}

// online reports whether a worker is enabled and has been here recently.
func online(w *model.Worker, now time.Time) bool {
	return w.Enabled && w.LastSeenAt != nil && now.Sub(*w.LastSeenAt) < OnlineWithin
}

// Ready reports whether a worker is enabled, has been here recently and
// can do a kind of task.
func (l *Ledger) Ready(ctx context.Context, workerID int64, kind string) bool {
	var w model.Worker
	if err := l.db.NewSelect().Model(&w).Where("id = ?", workerID).Scan(ctx); err != nil {
		return false
	}
	return online(&w, time.Now()) && takes(&w, kind)
}

// pinState is what a listed worker can do about a pinned task right now.
type pinState struct{ ready, room bool }

// pinAllows reports whether a worker may take a pinned task: a listed one
// when every worker listed before it is away or full, anyone else (unless
// the pin is strict) only while none of the listed workers is online.
func (l *Ledger) pinAllows(ctx context.Context, p Pin, workerID int64, kind string, perWorker int, seen map[string]pinState) bool {
	anyReady := false
	for _, id := range p.Workers {
		if id == workerID {
			return true
		}
		key := strconv.FormatInt(id, 10) + "/" + kind
		st, ok := seen[key]
		if !ok {
			st = l.pinState(ctx, id, kind, perWorker)
			seen[key] = st
		}
		if st.ready && st.room {
			return false // one listed before it can take it now
		}
		anyReady = anyReady || st.ready
	}
	return !p.Strict && !anyReady
}

func (l *Ledger) pinState(ctx context.Context, id int64, kind string, perWorker int) pinState {
	var w model.Worker
	if err := l.db.NewSelect().Model(&w).Where("id = ?", id).Scan(ctx); err != nil {
		return pinState{}
	}
	// a pin is for pages that need upscaling, whatever the kind of task
	if !online(&w, time.Now()) || !takes(&w, kind) || !takes(&w, model.TaskUpscale) {
		return pinState{}
	}
	limit := w.Concurrent
	if limit <= 0 {
		limit = perWorker
	}
	held, err := l.db.NewSelect().Model((*model.WorkerTask)(nil)).
		Where("worker_id = ? AND state = ?", id, model.TaskLeased).Count(ctx)
	return pinState{ready: true, room: err == nil && held < max(limit, 1)}
}
