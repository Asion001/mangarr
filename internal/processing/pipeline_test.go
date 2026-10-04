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

// sizedUpscaler doubles every page and fails on any it is sent taller than
// maxHeight, as the engines do when the upscaled strip is too tall to write.
type sizedUpscaler struct {
	maxHeight int
	seen      atomic.Int32
}

func (s *sizedUpscaler) Test(context.Context) error { return nil }

func (s *sizedUpscaler) Info(context.Context) (*upscale.Info, error) {
	return &upscale.Info{Models: []upscale.Model{{Name: "m", Scales: []int{2}}}}, nil
}

func (s *sizedUpscaler) Upscale(_ context.Context, images []upscale.Image, _ upscale.Params) ([]upscale.Image, error) {
	out := make([]upscale.Image, len(images))
	for i, img := range images {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil {
			return nil, err
		}
		if cfg.Height > s.maxHeight {
			return nil, fmt.Errorf("%s is %d tall", img.Name, cfg.Height)
		}
		s.seen.Add(1)
		var buf bytes.Buffer
		if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, cfg.Width*2, cfg.Height*2))); err != nil {
			return nil, err
		}
		out[i] = upscale.Image{Name: strings.TrimSuffix(img.Name, filepath.Ext(img.Name)) + ".png", Data: buf.Bytes()}
	}
	return out, nil
}

// TestSplitsStripsBeforeUpscaling: a narrow webtoon strip is cut before it
// goes to the upscaler, which would otherwise have to write a strip too
// tall for it.
func TestSplitsStripsBeforeUpscaling(t *testing.T) {
	dir := t.TempDir()
	strip := writeTestImage(t, dir, "0001.png", "png", patternedStrip(80, 620, 202, 411))
	page := writeTestImage(t, dir, "0002.png", "png", patternedStrip(80, 220))
	up := &sizedUpscaler{maxHeight: 240}
	proc := New(upscaling.NewFixed(up), nil)
	cfg := model.ProfileConfig{
		Upscale: model.UpscaleConfig{Enabled: true, MinWidth: 160, Model: "m"},
		Pages:   model.PageRules{JunkUnder: -1, SplitTall: true, SplitRatio: 3, SegmentRatio: 3},
	}
	res, err := proc.Process(context.Background(), cfg, []downloads.PageFile{strip, page}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Split != 1 || !res.Upscaled || len(res.Pages) != 4 {
		t.Fatalf("split %d, upscaled %v, %d pages", res.Split, res.Upscaled, len(res.Pages))
	}
	if want := []int{0, 0, 0, 1}; fmt.Sprint(res.SourcePages) != fmt.Sprint(want) {
		t.Fatalf("source pages %v, want %v", res.SourcePages, want)
	}
	for i, pg := range res.Pages {
		if want := fmt.Sprintf("%04d.png", i+1); pg.Name != want || pg.Width != 160 {
			t.Fatalf("page %d: %s %dx%d, want %s 160 wide", i, pg.Name, pg.Width, pg.Height, want)
		}
	}
	if up.seen.Load() != 4 {
		t.Fatalf("upscaled %d pages, want 4", up.seen.Load())
	}
}
