package upscaling

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/progress"
)

// overlapEngine counts how many runs it has at once.
type overlapEngine struct {
	now, most atomic.Int32
	out       []byte
}

func (e *overlapEngine) Test(context.Context) error { return nil }

func (e *overlapEngine) Info(context.Context) (*upscale.Info, error) {
	return &upscale.Info{Models: []upscale.Model{{Name: "m", Scales: []int{2}}}}, nil
}

func (e *overlapEngine) Upscale(ctx context.Context, images []upscale.Image, p upscale.Params) ([]upscale.Image, error) {
	n := e.now.Add(1)
	defer e.now.Add(-1)
	for {
		most := e.most.Load()
		if n <= most || e.most.CompareAndSwap(most, n) {
			break
		}
	}
	time.Sleep(20 * time.Millisecond)
	out := make([]upscale.Image, len(images))
	for i, img := range images {
		out[i] = upscale.Image{Name: strings.TrimSuffix(img.Name, filepath.Ext(img.Name)) + ".png", Data: e.out}
	}
	return out, nil
}

// TestRunsOverlap: the next run of a chapter starts while the last one is
// still going, so the GPU isn't left waiting between them.
func TestRunsOverlap(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var pages []downloads.PageFile
	for i := range 3 * ChunkPages {
		path := filepath.Join(dir, fmt.Sprintf("%04d.png", i))
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, downloads.PageFile{Name: filepath.Base(path), Path: path, Format: "png", Width: 4, Height: 4})
	}
	eng := &overlapEngine{out: buf.Bytes()}
	var mu sync.Mutex
	var last int
	ctx := progress.With(context.Background(), func(ev progress.Event) { mu.Lock(); last = ev.Done; mu.Unlock() })
	out, changed, _, err := NewFixed(eng).Process(ctx, model.UpscaleConfig{Enabled: true, MinWidth: 8, Model: "m", Format: "png"}, pages, dir)
	if err != nil || !changed || len(out) != len(pages) {
		t.Fatalf("process: %v %v %d", err, changed, len(out))
	}
	for i, p := range out {
		if p.Path == pages[i].Path {
			t.Fatalf("page %d was left out", i)
		}
	}
	if eng.most.Load() < 2 {
		t.Fatalf("runs went one after another (at most %d at once)", eng.most.Load())
	}
	if eng.most.Load() > int32(ChunksInFlight) {
		t.Fatalf("%d runs at once, more than %d", eng.most.Load(), ChunksInFlight)
	}
	if last != len(pages) {
		t.Fatalf("progress ended at %d of %d", last, len(pages))
	}
}
