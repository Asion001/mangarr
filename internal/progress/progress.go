// Package progress carries a progress callback through a context, so deep
// work (page encoding, upscaling batches) can report without changing
// every interface in between.
package progress

import "context"

// Stages of a job.
const (
	StageDownload = "download"
	StageUpscale  = "upscale"
	StageSplit    = "split"
	StageEncode   = "encode"
	StageWrite    = "write"
	// StageWait: the pages are handed to the workers and none has started on
	// them yet.
	StageWait = "wait"
)

// Event reports how far a stage is. Done and the byte counts are totals so
// far, not deltas.
type Event struct {
	Stage    string
	Done     int
	Total    int
	BytesIn  int64 // input size of the finished items
	BytesOut int64 // their output size
}

// Func receives events; it must be cheap and safe for concurrent use.
type Func func(Event)

type key struct{}

// With returns a context that reports to fn.
func With(ctx context.Context, fn Func) context.Context {
	return context.WithValue(ctx, key{}, fn)
}

// Report sends ev to the context's callback, if any.
func Report(ctx context.Context, ev Event) {
	if fn, ok := ctx.Value(key{}).(Func); ok && fn != nil {
		fn(ev)
	}
}
