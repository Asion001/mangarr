package processing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/imageenc"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/upscale"
	"github.com/Asion001/mangarr/internal/upscaling"
)

// gatedUpscaler lets its first run through and holds every later one until
// a page has been encoded: with encoding after all the upscaling, it never
// lets go.
type gatedUpscaler struct {
	runs    atomic.Int32
	encoded <-chan struct{}
	out     []byte
}

func (g *gatedUpscaler) Test(context.Context) error { return nil }

func (g *gatedUpscaler) Info(context.Context) (*upscale.Info, error) {
	return &upscale.Info{Models: []upscale.Model{{Name: "m", Scales: []int{2}}}}, nil
}

func (g *gatedUpscaler) Upscale(ctx context.Context, images []upscale.Image, p upscale.Params) ([]upscale.Image, error) {
	if g.runs.Add(1) > 1 {
		select {
		case <-g.encoded:
		case <-time.After(5 * time.Second):
			return nil, errors.New("no page was encoded while the chapter was upscaling")
		}
	}
	out := make([]upscale.Image, len(images))
	for i, img := range images {
		out[i] = upscale.Image{Name: strings.TrimSuffix(img.Name, filepath.Ext(img.Name)) + ".png", Data: g.out}
	}
	return out, nil
}

// countingEncoder writes a small AVIF for every page and says when the
// first one is done.
type countingEncoder struct {
	calls atomic.Int32
	first chan struct{}
}

func (c *countingEncoder) Name() string        { return "fake" }
func (c *countingEncoder) Format() string      { return "avif" }
func (c *countingEncoder) Slow() bool          { return false }
func (c *countingEncoder) Accepts(string) bool { return true }
func (c *countingEncoder) Encode(_ context.Context, src, srcFormat, dst string, o imageenc.Options, gray bool) error {
	hdr, err := os.ReadFile("../imagecheck/testdata/gray.avif")
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, hdr, 0o644); err != nil {
		return err
	}
	if c.calls.Add(1) == 1 {
		close(c.first)
	}
	return nil
}

// TestEncodesWhileUpscaling: a chapter's first pages are encoded while the
// GPU is still on the rest, instead of the GPU sitting idle through a
// chapter's whole encode.
func TestEncodesWhileUpscaling(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 800, 1200))); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	var pages []downloads.PageFile
	for i := range 3 * upscaling.ChunkPages {
		path := filepath.Join(dir, fmt.Sprintf("%04d.png", i+1))
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, downloads.PageFile{Name: filepath.Base(path), Path: path, Format: "png", Width: 400, Height: 600})
	}
	enc := &countingEncoder{first: make(chan struct{})}
	up := &gatedUpscaler{encoded: enc.first, out: buf.Bytes()}
	proc := New(upscaling.NewFixed(up), imageenc.New(enc))
	cfg := model.ProfileConfig{
		Upscale: model.UpscaleConfig{Enabled: true, MinWidth: 800, Model: "m"},
		Encode:  model.EncodeConfig{Format: "avif", MinSavingsPct: 0},
	}
	res, err := proc.Process(context.Background(), cfg, pages, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Upscaled || res.Encoded != len(pages) || len(res.Pages) != len(pages) {
		t.Fatalf("upscaled %v, encoded %d, pages %d", res.Upscaled, res.Encoded, len(res.Pages))
	}
	for i, pg := range res.Pages {
		if want := fmt.Sprintf("%04d.avif", i+1); pg.Name != want || pg.Format != "avif" || res.SourcePages[i] != i {
			t.Fatalf("page %d: %s %s from %d, want %s", i, pg.Name, pg.Format, res.SourcePages[i], want)
		}
	}
}
