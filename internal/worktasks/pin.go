package worktasks

import (
	"context"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// Spec keys that keep a task for one worker (an upscale route's target).
const (
	SpecPinWorker = "pinWorker"
	SpecPinStrict = "pinStrict"
)

// Pin keeps a task for one worker. A strict pin waits for it however long
// it is away; otherwise the task goes to anyone while that worker is
// offline.
type Pin struct {
	Worker int64
	Strict bool
}

type pinKey struct{}

// WithPin returns a context whose tasks are kept for a worker.
func WithPin(ctx context.Context, p Pin) context.Context {
	return context.WithValue(ctx, pinKey{}, p)
}

// PinFrom is the pin a context carries.
func PinFrom(ctx context.Context) (Pin, bool) {
	p, ok := ctx.Value(pinKey{}).(Pin)
	return p, ok && p.Worker > 0
}

// Apply writes the pin into a task's spec.
func (p Pin) Apply(spec map[string]any) {
	if p.Worker <= 0 {
		return
	}
	spec[SpecPinWorker], spec[SpecPinStrict] = p.Worker, p.Strict
}

// PinOf reads a task's pin; Worker is 0 when it has none.
func PinOf(t *model.WorkerTask) Pin {
	var p Pin
	switch v := t.Spec[SpecPinWorker].(type) {
	case int64:
		p.Worker = v
	case int:
		p.Worker = int64(v)
	case float64:
		p.Worker = int64(v)
	}
	p.Strict, _ = t.Spec[SpecPinStrict].(bool)
	return p
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
