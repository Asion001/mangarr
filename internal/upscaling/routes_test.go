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
	"testing"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/upscale"
)

// recordingEngine notes the parameters of every run.
type recordingEngine struct {
	models []upscale.Model
	out    []byte
	mu     sync.Mutex
	runs   []upscale.Params
	pinned []bool // whether the run's context carried the route's mark
}

type markKey struct{}

func (e *recordingEngine) Test(context.Context) error { return nil }

func (e *recordingEngine) Info(context.Context) (*upscale.Info, error) {
	return &upscale.Info{Models: e.models}, nil
}

func (e *recordingEngine) Upscale(ctx context.Context, images []upscale.Image, p upscale.Params) ([]upscale.Image, error) {
	e.mu.Lock()
	e.runs = append(e.runs, p)
	e.pinned = append(e.pinned, ctx.Value(markKey{}) != nil)
	e.mu.Unlock()
	out := make([]upscale.Image, len(images))
	for i, img := range images {
		out[i] = upscale.Image{Name: strings.TrimSuffix(img.Name, filepath.Ext(img.Name)) + ".png", Data: e.out}
	}
	return out, nil
}

func routePages(t *testing.T, widths ...int) ([]downloads.PageFile, []byte, string) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var pages []downloads.PageFile
	for i, w := range widths {
		path := filepath.Join(dir, fmt.Sprintf("%04d.png", i))
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, downloads.PageFile{Name: filepath.Base(path), Path: path, Format: "png", Width: w, Height: w})
	}
	return pages, buf.Bytes(), dir
}

// TestRoutesSendPagesElsewhere: pages that need ×4 go to the route's
// upscaler with the route's model, the rest stay with the one the priority
// order chose.
func TestRoutesSendPagesElsewhere(t *testing.T) {
	pages, png, dir := routePages(t, 4, 4, 2, 2, 2)
	fast := &recordingEngine{models: []upscale.Model{{Name: "fast", Scales: []int{2, 4}}}, out: png}
	slow := &recordingEngine{models: []upscale.Model{{Name: "good", Scales: []int{4}}}, out: png}
	routes := []model.UpscaleRoute{{Match: model.RouteScale, Scales: []int{4}, Target: 7, Model: "good"}}
	p := &Processor{Fixed: fast, Routes: func(context.Context) []model.UpscaleRoute { return routes },
		Pick: func(ctx context.Context, r model.UpscaleRoute, chosen upscale.Module) (upscale.Module, func(context.Context) context.Context, error) {
			if r.Target != 7 {
				t.Fatalf("picked for %+v", r)
			}
			return slow, func(ctx context.Context) context.Context { return context.WithValue(ctx, markKey{}, true) }, nil
		}}
	out, changed, _, err := p.Process(context.Background(), model.UpscaleConfig{Enabled: true, MinWidth: 8, Model: "fast", Format: "png"}, pages, dir)
	if err != nil || !changed || len(out) != len(pages) {
		t.Fatalf("process: %v %v %d", err, changed, len(out))
	}
	if len(fast.runs) != 1 || fast.runs[0].Scale != 2 || fast.runs[0].Model != "fast" || fast.runs[0].Pinned || fast.pinned[0] {
		t.Fatalf("the ×2 pages ran as %+v (marked %v)", fast.runs, fast.pinned)
	}
	if len(slow.runs) != 1 || slow.runs[0].Scale != 4 || slow.runs[0].Model != "good" || !slow.runs[0].Pinned || !slow.pinned[0] {
		t.Fatalf("the ×4 pages ran as %+v (marked %v)", slow.runs, slow.pinned)
	}
}

// TestRouteByWidthKeepsTheMachine: a width route without a target or model
// of its own changes nothing but the model, and a model the upscaler
// doesn't have is dropped rather than failing the chapter.
func TestRouteByWidthKeepsTheMachine(t *testing.T) {
	pages, png, dir := routePages(t, 6, 3)
	eng := &recordingEngine{models: []upscale.Model{{Name: "m", Scales: []int{2, 4}}, {Name: "sharp", Scales: []int{2, 3}}}, out: png}
	cfg := model.UpscaleConfig{Enabled: true, MinWidth: 8, Model: "m", Format: "png"}

	routes := []model.UpscaleRoute{{Match: model.RouteWidth, BelowWidth: 5, Model: "sharp"}}
	p := &Processor{Fixed: eng, Routes: func(context.Context) []model.UpscaleRoute { return routes }}
	if _, _, _, err := p.Process(context.Background(), cfg, pages, dir); err != nil {
		t.Fatal(err)
	}
	byModel := map[string]upscale.Params{}
	for _, r := range eng.runs {
		byModel[r.Model] = r
	}
	if r, ok := byModel["sharp"]; !ok || r.Scale != 3 || !r.Pinned {
		t.Fatalf("the narrow page ran as %+v (want sharp at its nearest scale, 3)", eng.runs)
	}
	if r, ok := byModel["m"]; !ok || r.Scale != 2 || r.Pinned {
		t.Fatalf("the wider page ran as %+v", eng.runs)
	}

	eng.runs = nil
	routes = []model.UpscaleRoute{{Match: model.RouteWidth, BelowWidth: 5, Model: "missing"}}
	if _, _, _, err := p.Process(context.Background(), cfg, pages, dir); err != nil {
		t.Fatal(err)
	}
	for _, r := range eng.runs {
		if r.Model != "m" || r.Pinned {
			t.Fatalf("a model the upscaler lacks was used: %+v", eng.runs)
		}
	}
}
