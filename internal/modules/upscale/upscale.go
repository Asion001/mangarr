// Package upscale defines upscaler modules. The v1 implementation talks to a
// mangarr-upscaler worker (ncnn/Vulkan); other backends (PyTorch/ONNX
// MangaJaNai, cloud APIs) can be added as separate modules.
package upscale

import (
	"context"
	"slices"
	"sync"

	"github.com/Asion001/mangarr/internal/modules"
)

type Model struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Scales      []int  `json:"scales"`
	NoiseLevels []int  `json:"noiseLevels,omitempty"`
}

type Info struct {
	Version string   `json:"version"`
	Devices []string `json:"devices"`
	Models  []Model  `json:"models"`
}

type Params struct {
	Model    string `json:"model"`
	Scale    int    `json:"scale"`
	Noise    int    `json:"noise"`
	Format   string `json:"format"` // webp, jpeg, png
	Quality  int    `json:"quality"`
	MaxWidth int    `json:"maxWidth"` // 0 = no cap
	// Pinned keeps Model as it is: an upscale route chose it, so a machine
	// with a model of its own doesn't swap it in.
	Pinned bool `json:"pinned,omitempty"`
}

// Image is an in-memory page.
type Image struct {
	Name string
	Data []byte
}

type Module interface {
	modules.Instance
	Info(ctx context.Context) (*Info, error)
	// Upscale processes a batch of images with the same parameters. Output
	// names keep the input base name with the new format's extension.
	Upscale(ctx context.Context, images []Image, p Params) ([]Image, error)
}

// Ranked is an upscaler whose place in the order depends on the machines
// behind it: the workers module ranks as its best online worker, so this
// server and the workers share one priority list.
type Ranked interface {
	// Rank is the priority to order by now; false when it has none (no
	// machine that could take the work is around).
	Rank(ctx context.Context) (int, bool)
}

// FitScale is the scale a model runs a batch at when it was asked for
// want: the smallest it supports that is at least as large, else its
// largest. A machine that swaps in its own model uses it, since the server
// picked want for the profile's model.
func FitScale(want int, scales []int) int {
	best, largest := 0, 0
	for _, s := range scales {
		if s < 2 {
			continue
		}
		largest = max(largest, s)
		if s >= want && (best == 0 || s < best) {
			best = s
		}
	}
	if best > 0 {
		return best
	}
	if largest > 0 {
		return largest
	}
	return want
}

// usedKey carries the recorder of the models a batch actually ran with.
type usedKey struct{}

type usedModels struct {
	mu    sync.Mutex
	names []string
}

// WithUsed returns a context that collects the models the upscaler ran
// with (a worker may use its own instead of the profile's), and a function
// that lists them in the order they were first seen.
func WithUsed(ctx context.Context) (context.Context, func() []string) {
	u := &usedModels{}
	return context.WithValue(ctx, usedKey{}, u), func() []string {
		u.mu.Lock()
		defer u.mu.Unlock()
		return append([]string(nil), u.names...)
	}
}

// ReportUsed records the model a batch ran with.
func ReportUsed(ctx context.Context, name string) {
	u, _ := ctx.Value(usedKey{}).(*usedModels)
	if u == nil || name == "" {
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if !slices.Contains(u.names, name) {
		u.names = append(u.names, name)
	}
}
