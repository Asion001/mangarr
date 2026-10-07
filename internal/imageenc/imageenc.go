// Package imageenc re-encodes page images to save storage: lossy AVIF
// (avifenc from libavif, or a slower built-in WebAssembly encoder) and
// JPEG XL (cjxl), lossless recompression or lossy. Each page is kept only when the new
// file is meaningfully smaller, black-and-white pages are encoded without
// color, and pages that are already AVIF/JXL (or animated GIFs) are left alone.
package imageenc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
)

// Options are resolved encoder parameters.
type Options struct {
	Format      string // avif or jxl
	Quality     int    // AVIF and lossy JPEG XL 1-100
	Lossy       bool   // lossy JPEG XL at Quality
	Speed       int    // avifenc -s (0-10) / cjxl -e (1-9)
	Progressive bool   // layered AVIF for incremental display
	// Jobs is the encoder threads for one page (0 or 1 = single-threaded).
	// Normal pages are encoded one per core; a tall strip that has the
	// memory budget to itself gets the cores the other pages would use.
	Jobs int
}

// Resolve applies the preset of cfg and its overrides.
func Resolve(cfg model.EncodeConfig) Options {
	o := Options{Format: cfg.Format}
	switch cfg.Format {
	case "jxl":
		o.Speed = map[string]int{"max": 9, "balanced": 7, "fast": 4}[cfg.Preset]
		if o.Speed == 0 {
			o.Speed = 7
		}
		if o.Lossy = cfg.Lossy; o.Lossy {
			// cjxl -q 90 is about visually lossless; line art holds up lower
			o.Quality = map[string]int{"max": 75, "balanced": 80, "fast": 85}[cfg.Preset]
			if o.Quality == 0 {
				o.Quality = 80
			}
		}
	default:
		q := map[string][2]int{"max": {48, 3}, "balanced": {55, 6}, "fast": {60, 8}}[cfg.Preset]
		if q == [2]int{} {
			q = [2]int{55, 6}
		}
		o.Quality, o.Speed = q[0], q[1]
	}
	if cfg.Quality > 0 && (cfg.Format != "jxl" || cfg.Lossy) {
		o.Quality = min(cfg.Quality, 100)
	}
	if cfg.Speed > 0 {
		o.Speed = cfg.Speed
	}
	// layered AVIF costs ~6% in size and lets browsers show a page early;
	// readers without progressive decoding still show the full image, so it
	// is always on when the engine can write it (see EncodePages)
	o.Progressive = cfg.Format == "avif"
	return o
}

// Engine encodes one image file.
type Engine interface {
	Name() string
	// Format is the output format (avif or jxl).
	Format() string
	// Accepts reports whether the engine reads srcFormat directly.
	Accepts(srcFormat string) bool
	// Encode writes src (in srcFormat) to dst. gray requests a grayscale encode.
	Encode(ctx context.Context, src, srcFormat, dst string, o Options, gray bool) error
	// Slow marks engines that are much slower than native ones.
	Slow() bool
}

// Page is an image file on disk.
type Page struct {
	Name   string
	Path   string
	Format string
	Width  int
	Height int
}

// Stats summarizes an encode run.
type Stats struct {
	Engine  string `json:"engine"`
	Encoded int    `json:"encoded"`
	Kept    int    `json:"kept"`
	Skipped int    `json:"skipped"`
	Before  int64  `json:"before"`
	After   int64  `json:"after"`
}

// Encoder picks an engine per format and encodes pages in parallel.
type Encoder struct {
	engines []Engine
	// Threads is the number of pages encoded at once.
	Threads int
	// MaxPixels bounds the pixels of the pages encoded at once (0 = no
	// bound). A page is decoded whole (grayscale check) and the encoder holds
	// it whole too, so one page per core of upscaled webtoon strips (100+
	// megapixels each) is more memory than a small server has. A page larger
	// than the bound is encoded alone.
	MaxPixels int64
}

// DefaultMaxPixels is about 256 MB of pages decoded at once.
const DefaultMaxPixels = 64 << 20

// New returns an encoder using the given engines (earlier = preferred).
func New(engines ...Engine) *Encoder {
	return &Encoder{engines: engines, Threads: max(runtime.NumCPU()-1, 1), MaxPixels: DefaultMaxPixels}
}

// jobs is the encoder threads for p: its share of Threads by pixels, since
// a page using 1/k of the budget runs alongside about k-1 others.
func (e *Encoder) jobs(p Page) int {
	if e.MaxPixels <= 0 {
		return 1
	}
	t := int64(max(e.Threads, 1))
	return int(min(max((t*e.pixels(p)+e.MaxPixels-1)/e.MaxPixels, 1), t))
}

// pixels is what a page weighs against MaxPixels.
func (e *Encoder) pixels(p Page) int64 {
	return min(max(int64(p.Width)*int64(p.Height), 1), e.MaxPixels)
}

// Detect finds the installed engines: avifenc and cjxl on PATH, plus the
// built-in AVIF encoder as a fallback.
func Detect() *Encoder {
	var list []Engine
	if e := FindAvifenc(); e != nil {
		list = append(list, e)
	}
	if e := FindCjxl(); e != nil {
		list = append(list, e)
	}
	list = append(list, WASMAvif{})
	return New(list...)
}

// Engine returns the preferred engine for format.
func (e *Encoder) Engine(format string) (Engine, bool) {
	for _, en := range e.engines {
		if en.Format() == format {
			return en, true
		}
	}
	return nil, false
}

// Engines lists the available engines.
func (e *Encoder) Engines() []Engine { return append([]Engine(nil), e.engines...) }

// ErrNoEngine means no engine can produce the requested format.
var ErrNoEngine = errors.New("no encoder for this format is installed")

// skip reports pages that are never re-encoded.
func skip(format string, cfg model.EncodeConfig) bool {
	switch format {
	case "avif", "jxl", "gif", "":
		return true
	}
	// lossless JPEG XL only makes sense for JPEG (reversible) and PNG
	return cfg.Format == "jxl" && !cfg.Lossy && format != "jpeg" && format != "png"
}

// EncodePages re-encodes pages into workDir and returns the pages to keep:
// the new file when it's at least minSavingsPct smaller, else the original.
func (e *Encoder) EncodePages(ctx context.Context, pages []Page, cfg model.EncodeConfig, workDir string) ([]Page, Stats, error) {
	if cfg.Format == "" || cfg.Format == "keep" {
		return pages, Stats{}, nil
	}
	s, err := e.Stream(cfg, workDir)
	if err != nil {
		return nil, Stats{}, err
	}
	out := append([]Page(nil), pages...)
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	skipped := 0
	for _, p := range pages {
		if skip(p.Format, cfg) {
			skipped++
		}
	}
	done := skipped
	progress.Report(ctx, progress.Event{Stage: progress.StageEncode, Done: done, Total: len(pages)})
	for i, p := range pages {
		if skip(p.Format, cfg) {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			np, err := s.Encode(ctx, p)
			mu.Lock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s: %w", p.Name, err)
				}
				mu.Unlock()
				return
			}
			out[i] = np
			done++
			st := s.Stats()
			ev := progress.Event{Stage: progress.StageEncode, Done: done, Total: len(pages), BytesIn: st.Before, BytesOut: st.After}
			mu.Unlock()
			progress.Report(ctx, ev)
		}()
	}
	wg.Wait()
	st := s.Stats()
	st.Skipped = skipped
	if firstErr != nil {
		return nil, st, firstErr
	}
	return out, st, ctx.Err()
}

// Stream encodes pages one at a time as they become ready, all of them
// sharing the encoder's threads and memory budget: the processing stage
// hands it each page the moment the upscaler is done with it, so encoding
// runs while the GPU works on the next pages instead of after all of them.
type Stream struct {
	e      *Encoder
	eng    Engine
	cfg    model.EncodeConfig
	o      Options
	outDir string
	sem    chan struct{}
	budget *semaphore.Weighted

	mu sync.Mutex
	st Stats
}

// Stream starts encoding into workDir with cfg (which must name a format).
func (e *Encoder) Stream(cfg model.EncodeConfig, workDir string) (*Stream, error) {
	eng, ok := e.Engine(cfg.Format)
	if !ok {
		return nil, fmt.Errorf("%w (%s)", ErrNoEngine, cfg.Format)
	}
	o := Resolve(cfg)
	if o.Progressive {
		// the slim image's built-in encoder can't write layers: plain AVIF
		capable, ok := eng.(interface{ SupportsProgressive() bool })
		o.Progressive = ok && capable.SupportsProgressive()
	}
	outDir := filepath.Join(workDir, "encoded")
	if err := os.MkdirAll(outDir, 0o775); err != nil {
		return nil, err
	}
	s := &Stream{e: e, eng: eng, cfg: cfg, o: o, outDir: outDir, sem: make(chan struct{}, max(e.Threads, 1))}
	s.st.Engine = eng.Name()
	if e.MaxPixels > 0 {
		s.budget = semaphore.NewWeighted(e.MaxPixels)
	}
	return s, nil
}

// Encode re-encodes p and returns the page to keep: the new file, or p when
// it isn't worth it or p is never re-encoded (AVIF, JXL, animations).
// It is safe to call from many goroutines at once.
func (s *Stream) Encode(ctx context.Context, p Page) (Page, error) {
	if skip(p.Format, s.cfg) {
		s.mu.Lock()
		s.st.Skipped++
		s.mu.Unlock()
		return p, nil
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return p, ctx.Err()
	}
	defer func() { <-s.sem }()
	np, before, after, err := s.e.encodeWithin(ctx, s.budget, s.eng, p, s.cfg, s.o, s.outDir)
	if err != nil {
		return p, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.Before += before
	if np == nil {
		s.st.Kept++
		s.st.After += before
		return p, nil
	}
	s.st.Encoded++
	s.st.After += after
	return *np, nil
}

// Stats is what the stream has done so far.
func (s *Stream) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// encodeWithin encodes p once its pixels fit in budget (nil = no bound).
func (e *Encoder) encodeWithin(ctx context.Context, budget *semaphore.Weighted, eng Engine, p Page, cfg model.EncodeConfig, o Options, outDir string) (*Page, int64, int64, error) {
	if budget != nil {
		n := e.pixels(p)
		if err := budget.Acquire(ctx, n); err != nil {
			return nil, 0, 0, err
		}
		defer budget.Release(n)
	}
	o.Jobs = e.jobs(p)
	return e.encodeOne(ctx, eng, p, cfg, o, outDir)
}

func (e *Encoder) encodeOne(ctx context.Context, eng Engine, p Page, cfg model.EncodeConfig, o Options, outDir string) (*Page, int64, int64, error) {
	fi, err := os.Stat(p.Path)
	if err != nil {
		return nil, 0, 0, err
	}
	before := fi.Size()
	src, srcFormat := p.Path, p.Format
	gray := false
	if cfg.Grayscale || !eng.Accepts(srcFormat) {
		img, err := decodeFile(p.Path)
		if err != nil {
			return nil, before, 0, err
		}
		gray = cfg.Grayscale && IsGrayscale(img)
		if !eng.Accepts(srcFormat) { // e.g. WebP into avifenc: go through PNG
			tmp := filepath.Join(outDir, strings.TrimSuffix(p.Name, filepath.Ext(p.Name))+".src.png")
			if err := writePNG(tmp, img); err != nil {
				return nil, before, 0, err
			}
			defer os.Remove(tmp)
			src, srcFormat = tmp, "png"
		}
	}
	base := strings.TrimSuffix(p.Name, filepath.Ext(p.Name))
	dst := filepath.Join(outDir, base+"."+eng.Format())
	if err := eng.Encode(ctx, src, srcFormat, dst, o, gray); err != nil {
		return nil, before, 0, err
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		return nil, before, 0, err
	}
	if info, err := imagecheck.Detect(data); err != nil || info.Format != eng.Format() {
		return nil, before, 0, fmt.Errorf("encoder produced an invalid %s file", eng.Format())
	}
	after := int64(len(data))
	if after >= before*int64(100-max(cfg.MinSavingsPct, 0))/100 {
		os.Remove(dst)
		return nil, before, after, nil // not worth it: keep the original
	}
	np := p
	np.Name, np.Path, np.Format = base+"."+eng.Format(), dst, eng.Format()
	return &np, before, after, nil
}

func decodeFile(path string) (image.Image, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// IsGrayscale reports whether (nearly) every sampled pixel is gray. Scans
// with slightly tinted paper still count; color panels don't.
func IsGrayscale(img image.Image) bool {
	switch img.(type) {
	case *image.Gray, *image.Gray16:
		return true
	}
	b := img.Bounds()
	step := max(1, min(b.Dx(), b.Dy())/200)
	total, colored := 0, 0
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(r>>8), int(g>>8), int(bl>>8)
			if max(abs(r8-g8), abs(g8-b8), abs(r8-b8)) > 24 {
				colored++
			}
			total++
		}
	}
	return total > 0 && colored*1000 <= total*3 // at most 0.3% colored samples
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
