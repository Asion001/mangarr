package upscaler

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/image/draw"
)

// fakeRunner scales images with nearest-neighbour instead of a GPU model.
type fakeRunner struct{}

func (fakeRunner) Available(e Engine) bool { return e.Name == "waifu2x-cunet" }

func (fakeRunner) Run(ctx context.Context, e Engine, in, out string, scale, noise int) error {
	entries, err := os.ReadDir(in)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		f, err := os.Open(filepath.Join(in, ent.Name()))
		if err != nil {
			return err
		}
		img, _, err := image.Decode(f)
		f.Close()
		if err != nil {
			return err
		}
		b := img.Bounds()
		dst := image.NewRGBA(image.Rect(0, 0, b.Dx()*scale, b.Dy()*scale))
		draw.NearestNeighbor.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
		o, err := os.Create(filepath.Join(out, strings.TrimSuffix(ent.Name(), filepath.Ext(ent.Name()))+".png"))
		if err != nil {
			return err
		}
		if err := png.Encode(o, dst); err != nil {
			o.Close()
			return err
		}
		o.Close()
	}
	return nil
}

func jpegPage(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, x%h, color.White)
	}
	var b bytes.Buffer
	_ = jpeg.Encode(&b, img, nil)
	return b.Bytes()
}

// TestProcess: a batch is upscaled by the engine, capped at the requested
// width, and an engine this machine doesn't have is refused.
func TestProcess(t *testing.T) {
	s := NewServer(Config{TmpDir: t.TempDir(), CWebP: "-", Version: "test"}, fakeRunner{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.cfg.CWebP = "" // force JPEG fallback when webp is requested
	in := []Image{{Name: "0001.jpg", Data: jpegPage(300, 450)}, {Name: "0002.jpg", Data: jpegPage(400, 600)}}

	out, err := s.Process(context.Background(), Params{Model: "waifu2x-cunet", Scale: 4, Format: "webp", MaxWidth: 1400}, in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]image.Config{}
	for _, img := range out {
		cfg, _, err := image.DecodeConfig(bytes.NewReader(img.Data))
		if err != nil {
			t.Fatal(err)
		}
		got[img.Name] = cfg
	}
	// 300*4 = 1200 (under cap), 400*4 = 1600 -> capped to 1400
	if got["0001.jpg"].Width != 1200 || got["0002.jpg"].Width != 1400 || got["0002.jpg"].Height != 2100 {
		t.Fatalf("unexpected outputs: %+v", got)
	}

	if _, err := s.Process(context.Background(), Params{Model: "realcugan", Scale: 2}, in); err == nil {
		t.Fatal("a model this machine doesn't have should be refused")
	}
}

// fixedRunner writes the same PNG for every input, so a test can tell the
// engine's own file from one decoded and encoded again.
type fixedRunner struct{ png []byte }

func (fixedRunner) Available(e Engine) bool { return e.Name == "waifu2x-cunet" }

func (r fixedRunner) Run(ctx context.Context, e Engine, in, out string, scale, noise int) error {
	entries, err := os.ReadDir(in)
	if err != nil {
		return err
	}
	for _, ent := range entries {
		name := strings.TrimSuffix(ent.Name(), filepath.Ext(ent.Name())) + ".png"
		if err := os.WriteFile(filepath.Join(out, name), r.png, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// TestProcessKeepsEnginePNG: a PNG under the width cap is handed back as the
// engine wrote it, without decoding it (tall strips are hundreds of MB
// decoded); a wider one is still scaled down.
func TestProcessKeepsEnginePNG(t *testing.T) {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.NoCompression}
	if err := enc.Encode(&b, image.NewGray(image.Rect(0, 0, 120, 900))); err != nil {
		t.Fatal(err)
	}
	s := NewServer(Config{TmpDir: t.TempDir(), Version: "test"}, fixedRunner{png: b.Bytes()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	in := []Image{{Name: "0001.jpg", Data: jpegPage(40, 300)}}

	out, err := s.Process(context.Background(), Params{Model: "waifu2x-cunet", Scale: 2, Format: "png", MaxWidth: 1400}, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Name != "0001.png" || !bytes.Equal(out[0].Data, b.Bytes()) {
		t.Fatalf("the engine's PNG should come back untouched: %d pages, %q, %d bytes", len(out), out[0].Name, len(out[0].Data))
	}

	out, err = s.Process(context.Background(), Params{Model: "waifu2x-cunet", Scale: 2, Format: "png", MaxWidth: 60}, in)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out[0].Data))
	if err != nil || cfg.Width != 60 || cfg.Height != 450 {
		t.Fatalf("a PNG over the cap should be scaled down: %+v %v", cfg, err)
	}
}

type deviceRunner struct {
	fakeRunner
	mu        sync.Mutex
	devices   []string
	active    int
	maxActive int
	barrier   chan struct{}
}

func (r *deviceRunner) RunDevice(ctx context.Context, e Engine, in, out string, scale, noise int, device string) error {
	r.mu.Lock()
	r.devices = append(r.devices, device)
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	if r.active == 2 {
		close(r.barrier)
	}
	r.mu.Unlock()
	select {
	case <-r.barrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { r.mu.Lock(); r.active--; r.mu.Unlock() }()
	return r.fakeRunner.Run(ctx, e, in, out, scale, noise)
}

func TestProcessUsesOnePinnedSlotPerGPU(t *testing.T) {
	r := &deviceRunner{barrier: make(chan struct{})}
	s := NewServer(Config{TmpDir: t.TempDir(), GPU: "0,1"}, r, slog.New(slog.NewTextHandler(io.Discard, nil)))
	params := Params{Model: "waifu2x-cunet", Scale: 2, Format: "png"}
	images := []Image{{Name: "page.jpg", Data: jpegPage(8, 8)}}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := s.ProcessDevice(context.Background(), params, images)
			errs <- err
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("two configured GPUs did not run concurrently")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.maxActive != 2 || len(r.devices) != 2 || r.devices[0] == r.devices[1] {
		t.Fatalf("active=%d devices=%v", r.maxActive, r.devices)
	}
}

func TestParseGPUs(t *testing.T) {
	for input, want := range map[string][]string{"auto": {"auto"}, "0, 1": {"0", "1"}, "": {""}} {
		got, err := parseGPUs(input)
		if err != nil || strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("parseGPUs(%q) = %v, %v", input, got, err)
		}
	}
	for _, input := range []string{"-1", "0,", "1,1", "one"} {
		if _, err := parseGPUs(input); err == nil {
			t.Errorf("parseGPUs(%q) succeeded", input)
		}
	}
}

// emptyRunner upscales like fakeRunner but leaves an empty file for the
// pages in empty, the way the engine does when it can't write a result.
type emptyRunner struct {
	fakeRunner
	empty map[string]bool
}

func (r emptyRunner) Run(ctx context.Context, e Engine, in, out string, scale, noise int) error {
	if err := r.fakeRunner.Run(ctx, e, in, out, scale, noise); err != nil {
		return err
	}
	for base := range r.empty {
		if err := os.WriteFile(filepath.Join(out, base+".png"), nil, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// TestProcessKeepsPagesTheEngineCouldNotWrite: a page the engine left an
// empty file for comes back as it was instead of failing the batch, unless
// every page of the batch came back empty.
func TestProcessKeepsPagesTheEngineCouldNotWrite(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	in := []Image{{Name: "0001.jpg", Data: jpegPage(30, 40)}, {Name: "0002.jpg", Data: jpegPage(30, 40)}, {Name: "0003.jpg", Data: jpegPage(30, 40)}}
	p := Params{Model: "waifu2x-cunet", Scale: 2, Format: "png"}

	s := NewServer(Config{TmpDir: t.TempDir(), Version: "test"}, emptyRunner{empty: map[string]bool{"0002": true, "0003": true}}, log)
	out, err := s.Process(context.Background(), p, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 || out[0].Name != "0001.png" || out[1].Name != "0002.jpg" || !bytes.Equal(out[1].Data, in[1].Data) || out[2].Name != "0003.jpg" {
		t.Fatalf("the unwritten pages should come back as they were: %+v", out)
	}

	s = NewServer(Config{TmpDir: t.TempDir(), Version: "test"}, emptyRunner{empty: map[string]bool{"0001": true, "0002": true, "0003": true}}, log)
	if _, err := s.Process(context.Background(), p, in); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("a batch the engine wrote nothing for should fail: %v", err)
	}
}
