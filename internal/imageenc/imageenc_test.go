package imageenc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
)

func TestResolve(t *testing.T) {
	if o := Resolve(model.EncodeConfig{Format: "avif", Preset: "max"}); o.Quality != 48 || o.Speed != 3 {
		t.Fatalf("max: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "avif", Preset: "fast", Quality: 70, Speed: 9}); o.Quality != 70 || o.Speed != 9 {
		t.Fatalf("overrides: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "jxl", Preset: "balanced"}); o.Speed != 7 {
		t.Fatalf("jxl: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "jxl", Preset: "balanced", Quality: 60}); o.Lossy || o.Quality != 0 {
		t.Fatalf("lossless jxl ignores quality: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "jxl", Preset: "max", Lossy: true}); !o.Lossy || o.Quality != 75 || o.Speed != 9 {
		t.Fatalf("lossy jxl: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "jxl", Lossy: true, Quality: 90}); o.Quality != 90 {
		t.Fatalf("lossy jxl override: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "avif"}); !o.Progressive {
		t.Fatalf("AVIF is always progressive: %+v", o)
	}
	if o := Resolve(model.EncodeConfig{Format: "jxl", Progressive: true}); o.Progressive {
		t.Fatalf("jxl cannot be progressive: %+v", o)
	}
}

func grayImg() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			v := uint8((x + y) % 250)
			img.Set(x, y, color.RGBA{v, v, v + 3, 255}) // slightly tinted paper still counts as gray
		}
	}
	return img
}

func TestIsGrayscale(t *testing.T) {
	g := grayImg()
	if !IsGrayscale(g) {
		t.Fatal("tinted gray scan should be grayscale")
	}
	for y := 50; y < 150; y++ {
		for x := 50; x < 150; x++ {
			g.Set(x, y, color.RGBA{220, 30, 30, 255})
		}
	}
	if IsGrayscale(g) {
		t.Fatal("a red panel is color")
	}
}

func TestIsGrayscaleIgnoresChromaNoise(t *testing.T) {
	// a black-and-white page whose lines carry colored JPEG/upscaler
	// fringes: about 3% of its pixels are strongly colored
	img := image.NewRGBA(image.Rect(0, 0, 1000, 1500))
	rng := rand.New(rand.NewPCG(1, 2))
	for y := 0; y < 1500; y++ {
		for x := 0; x < 1000; x++ {
			v := uint8(255)
			if x%40 < 3 || y%60 < 2 {
				v = 20 // line art
			}
			c := color.RGBA{v, v, v, 255}
			if rng.IntN(100) < 3 {
				d := uint8(60)
				if v > 128 {
					c.R, c.B = v-d, v
				} else {
					c.R, c.B = v+d, v
				}
			}
			img.Set(x, y, c)
		}
	}
	if !IsGrayscale(img) {
		t.Fatal("chroma noise on a black-and-white page should still be grayscale")
	}
	for y := 600; y < 700; y++ { // a small color panel, about 0.7% of the page
		for x := 400; x < 500; x++ {
			img.Set(x, y, color.RGBA{240, 200, 120, 255})
		}
	}
	if IsGrayscale(img) {
		t.Fatal("a color panel is color")
	}
}

// fakeEngine "encodes" by writing a file of a chosen size.
type fakeEngine struct {
	mu      sync.Mutex
	accepts []string
	outSize int // bytes written per page
	calls   []string
	gray    []bool
	opts    []Options
}

func (f *fakeEngine) Name() string          { return "fake" }
func (f *fakeEngine) Format() string        { return "avif" }
func (f *fakeEngine) Slow() bool            { return false }
func (f *fakeEngine) Accepts(s string) bool { return slices.Contains(f.accepts, s) }
func (f *fakeEngine) Encode(_ context.Context, src, srcFormat, dst string, o Options, gray bool) error {
	f.mu.Lock()
	f.calls = append(f.calls, srcFormat)
	f.opts = append(f.opts, o)
	f.gray = append(f.gray, gray)
	f.mu.Unlock()
	// a valid AVIF header followed by padding
	hdr, _ := os.ReadFile("../imagecheck/testdata/gray.avif")
	return os.WriteFile(dst, append(hdr, make([]byte, max(f.outSize-len(hdr), 0))...), 0o644)
}

func writePage(t *testing.T, dir, name string, img image.Image, format string) Page {
	t.Helper()
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95})
	case "png":
		_ = png.Encode(&buf, img)
	default:
		buf.WriteString("GIF89a....")
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return Page{Name: name, Path: p, Format: format, Width: 200, Height: 300}
}

func TestEncodePages(t *testing.T) {
	dir := t.TempDir()
	colored := image.NewRGBA(image.Rect(0, 0, 200, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 200; x++ {
			colored.Set(x, y, color.RGBA{uint8(x), 40, uint8(255 - y%256), 255})
		}
	}
	pages := []Page{
		writePage(t, dir, "0001.jpg", grayImg(), "jpeg"),
		writePage(t, dir, "0002.png", colored, "png"),
		writePage(t, dir, "0003.gif", nil, "gif"),
		{Name: "0004.avif", Path: "x", Format: "avif"},
	}
	eng := &fakeEngine{accepts: []string{"jpeg"}, outSize: 600}
	enc := New(eng)
	var evMu sync.Mutex
	var evs []progress.Event
	ctx := progress.With(context.Background(), func(ev progress.Event) { evMu.Lock(); evs = append(evs, ev); evMu.Unlock() })
	out, st, err := enc.EncodePages(ctx, pages, model.EncodeConfig{Format: "avif", Preset: "balanced", Grayscale: true, MinSavingsPct: 10}, dir)
	if err != nil {
		t.Fatal(err)
	}
	// skipped pages count as done up front, then one event per encoded page
	if len(evs) != 3 || evs[0].Done != 2 || evs[2].Done != 4 || evs[2].Total != 4 || evs[2].Stage != progress.StageEncode || evs[2].BytesIn != st.Before {
		t.Fatalf("progress events %+v", evs)
	}
	if st.Encoded != 2 || st.Skipped != 2 || out[0].Format != "avif" || out[0].Name != "0001.avif" || out[2].Format != "gif" || out[3].Format != "avif" {
		t.Fatalf("stats %+v out %+v", st, out)
	}
	if out[0].Width != 200 {
		t.Fatal("dimensions are kept")
	}
	// the PNG went through Go (engine doesn't accept it) and only the gray page is gray
	slices.Sort(eng.calls)
	if !slices.Equal(eng.calls, []string{"jpeg", "png"}) {
		t.Fatalf("calls %v", eng.calls)
	}
	grays := 0
	for _, g := range eng.gray {
		if g {
			grays++
		}
	}
	if grays != 1 {
		t.Fatalf("want exactly one grayscale encode, got %v", eng.gray)
	}

	// too little savings: keep the originals
	eng2 := &fakeEngine{accepts: []string{"jpeg", "png"}, outSize: 1 << 20}
	out, st, err = New(eng2).EncodePages(context.Background(), pages[:2], model.EncodeConfig{Format: "avif", MinSavingsPct: 10}, t.TempDir())
	if err != nil || st.Kept != 2 || out[0].Format != "jpeg" || out[1].Format != "png" {
		t.Fatalf("keep originals: %+v %+v %v", st, out, err)
	}

	// no engine for the format
	if _, _, err := New(eng).EncodePages(context.Background(), pages, model.EncodeConfig{Format: "jxl"}, dir); !errors.Is(err, ErrNoEngine) {
		t.Fatalf("want ErrNoEngine, got %v", err)
	}
}

func TestAvifencArgsAndTuneFallback(t *testing.T) {
	var calls [][]string
	orig := run
	defer func() { run = orig }()
	run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		all := append([]string{name}, args...)
		calls = append(calls, all)
		if slices.Contains(args, "tune=iq") {
			return []byte("Invalid codec-specific option: tune=iq"), errors.New("exit status 1")
		}
		return nil, nil
	}
	a := &Avifenc{Bin: "/usr/bin/avifenc", Progressive: true}
	if err := a.Encode(context.Background(), "in.png", "png", "out.avif", Options{Quality: 55, Speed: 6, Progressive: true}, true); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("want a retry without tune, got %v", calls)
	}
	got := strings.Join(calls[1], " ")
	for _, want := range []string{"-j 1", "-s 6", "-q 55", "-d 8", "-y 400 --ignore-icc", "--progressive", "in.png out.avif"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q missing %q", got, want)
		}
	}
	calls = nil
	_ = a.Encode(context.Background(), "in.png", "png", "out.avif", Options{Quality: 55, Speed: 6}, false)
	if len(calls) != 1 || !strings.Contains(strings.Join(calls[0], " "), "-y 420") || slices.Contains(calls[0], "--ignore-icc") {
		t.Fatalf("tune=iq should not be retried once unsupported: %v", calls)
	}
}

type layeredEngine struct{ fakeEngine }

func (*layeredEngine) SupportsProgressive() bool { return true }

func TestProgressiveOnlyWhenEngineCan(t *testing.T) {
	dir := t.TempDir()
	p := writePage(t, dir, "0001.jpg", grayImg(), "jpeg")
	plain := &fakeEngine{accepts: []string{"jpeg"}, outSize: 600}
	if _, _, err := New(plain).EncodePages(context.Background(), []Page{p}, model.EncodeConfig{Format: "avif"}, dir); err != nil {
		t.Fatal(err)
	}
	if len(plain.opts) != 1 || plain.opts[0].Progressive {
		t.Fatalf("an engine without layers writes plain AVIF: %+v", plain.opts)
	}
	layered := &layeredEngine{fakeEngine{accepts: []string{"jpeg"}, outSize: 600}}
	if _, _, err := New(layered).EncodePages(context.Background(), []Page{p}, model.EncodeConfig{Format: "avif"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if len(layered.opts) != 1 || !layered.opts[0].Progressive {
		t.Fatalf("a capable engine writes layered AVIF: %+v", layered.opts)
	}
}

func TestWASMAvif(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	dir := t.TempDir()
	p := writePage(t, dir, "0001.png", grayImg(), "png")
	dst := filepath.Join(dir, "0001.avif")
	if err := (WASMAvif{}).Encode(context.Background(), p.Path, "png", dst, Options{Quality: 55, Speed: 8}, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(dst)
	info, err := imagecheck.Detect(data)
	if err != nil || info.Format != "avif" || info.Width != 200 || info.Height != 300 {
		t.Fatalf("wasm output: %+v %v", info, err)
	}
}

// busyEngine counts how many pages it encodes at once.
type busyEngine struct {
	fakeEngine
	now, peak atomic.Int32
}

func (b *busyEngine) Encode(ctx context.Context, src, srcFormat, dst string, o Options, gray bool) error {
	n := b.now.Add(1)
	defer b.now.Add(-1)
	for p := b.peak.Load(); n > p && !b.peak.CompareAndSwap(p, n); p = b.peak.Load() {
	}
	time.Sleep(20 * time.Millisecond)
	return b.fakeEngine.Encode(ctx, src, srcFormat, dst, o, gray)
}

// TestEncodePagesPixelBudget: big pages are not all decoded at once, one per
// core, and a page bigger than the whole budget still gets encoded.
func TestEncodePagesPixelBudget(t *testing.T) {
	dir := t.TempDir()
	var pages []Page
	for i := range 6 {
		pages = append(pages, writePage(t, dir, fmt.Sprintf("%04d.jpg", i+1), grayImg(), "jpeg"))
	}
	pages[5].Height = 3000 // ten times the others: more than the whole budget
	eng := &busyEngine{fakeEngine: fakeEngine{accepts: []string{"jpeg"}, outSize: 600}}
	enc := New(eng)
	enc.Threads, enc.MaxPixels = 6, 2*200*300
	_, st, err := enc.EncodePages(context.Background(), pages, model.EncodeConfig{Format: "avif", Grayscale: true, MinSavingsPct: 10}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Encoded != 6 {
		t.Fatalf("every page should be encoded: %+v", st)
	}
	if peak := eng.peak.Load(); peak > 2 {
		t.Fatalf("%d pages encoded at once within a budget of two", peak)
	}
}

func TestJobsSplitCoresByPageSize(t *testing.T) {
	e := &Encoder{Threads: 8, MaxPixels: 64 << 20}
	for _, c := range []struct {
		w, h, want int
	}{
		{0, 0, 1},         // unknown size
		{1400, 2000, 1},   // normal page: one core each
		{1400, 23000, 4},  // ~half the budget: half the cores
		{2048, 60000, 8},  // fills the budget alone: every core
		{2048, 200000, 8}, // larger than the budget
	} {
		if got := e.jobs(Page{Width: c.w, Height: c.h}); got != c.want {
			t.Errorf("%dx%d: jobs %d, want %d", c.w, c.h, got, c.want)
		}
	}
	if got := (&Encoder{Threads: 8}).jobs(Page{Width: 2048, Height: 60000}); got != 1 {
		t.Errorf("no budget: jobs %d, want 1", got)
	}
}

func TestSkipLossyJXL(t *testing.T) {
	lossless, lossy := model.EncodeConfig{Format: "jxl"}, model.EncodeConfig{Format: "jxl", Lossy: true}
	if !skip("webp", lossless) || skip("jpeg", lossless) {
		t.Fatal("lossless JPEG XL takes JPEG and PNG only")
	}
	if skip("webp", lossy) || !skip("jxl", lossy) {
		t.Fatal("lossy JPEG XL takes WebP too, but not JPEG XL")
	}
}

func TestCjxlArgs(t *testing.T) {
	var got []string
	orig := run
	defer func() { run = orig }()
	run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return nil, nil
	}
	c := &Cjxl{Bin: "/usr/bin/cjxl"}
	for _, tc := range []struct {
		src  string
		o    Options
		want string
	}{
		{"jpeg", Options{Speed: 7}, "--lossless_jpeg=1"},
		{"png", Options{Speed: 7}, "-d 0"},
		{"jpeg", Options{Speed: 7, Lossy: true, Quality: 80}, "-q 80 --lossless_jpeg=0"},
		{"png", Options{Speed: 7, Lossy: true, Quality: 85}, "-q 85"},
	} {
		if err := c.Encode(context.Background(), "in", tc.src, "out.jxl", tc.o, false); err != nil {
			t.Fatal(err)
		}
		if a := strings.Join(got, " "); !strings.Contains(a, tc.want) {
			t.Errorf("%s %+v: args %q missing %q", tc.src, tc.o, a, tc.want)
		}
	}
}
