package imageenc

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gen2brain/avif"
)

// run executes a command (replaceable in tests).
var run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// niceArgs lowers the priority of encoders when `nice` is available, so
// encoding in the background doesn't slow down the server.
func niceArgs(bin string, args []string) (string, []string) {
	if n, err := exec.LookPath("nice"); err == nil {
		return n, append([]string{"-n", "10", bin}, args...)
	}
	return bin, args
}

// Avifenc runs libavif's avifenc (fast, supports grayscale and tune=iq).
type Avifenc struct {
	Bin         string
	Version     string
	Progressive bool
	// tuneIQ is cleared after the first failure (libaom older than 3.12).
	noTune atomic.Bool
}

// FindAvifenc returns avifenc from PATH (nil when missing).
func FindAvifenc() *Avifenc {
	bin, err := exec.LookPath("avifenc")
	if err != nil {
		return nil
	}
	out, err := run(context.Background(), bin, "--version")
	if err != nil {
		return nil // can't start (e.g. a missing runtime library)
	}
	help, _ := run(context.Background(), bin, "--help")
	return &Avifenc{Bin: bin, Version: firstLine(out), Progressive: bytes.Contains(help, []byte("--progressive"))}
}

func (a *Avifenc) Name() string               { return "avifenc" }
func (a *Avifenc) Format() string             { return "avif" }
func (a *Avifenc) Slow() bool                 { return false }
func (a *Avifenc) Accepts(format string) bool { return format == "jpeg" || format == "png" }
func (a *Avifenc) SupportsProgressive() bool  { return a.Progressive }
func (a *Avifenc) args(src, dst string, o Options, gray, tune bool) []string {
	args := []string{"-j", strconv.Itoa(max(o.Jobs, 1)), "-s", strconv.Itoa(o.Speed), "-q", strconv.Itoa(o.Quality), "-d", "8"}
	if gray {
		// avifenc won't write grayscale from a page with a color ICC profile
		args = append(args, "-y", "400", "--ignore-icc")
	} else {
		args = append(args, "-y", "420")
	}
	if tune {
		args = append(args, "-c", "aom", "-a", "tune=iq")
	}
	if o.Progressive {
		args = append(args, "--progressive")
	}
	return append(args, src, dst)
}

func (a *Avifenc) Encode(ctx context.Context, src, _ /*srcFormat*/, dst string, o Options, gray bool) error {
	tune := !a.noTune.Load()
	bin, args := niceArgs(a.Bin, a.args(src, dst, o, gray, tune))
	out, err := run(ctx, bin, args...)
	if err != nil && tune && ctx.Err() == nil {
		// tune=iq needs libaom 3.12+; retry without it and remember
		bin, args = niceArgs(a.Bin, a.args(src, dst, o, gray, false))
		if out2, err2 := run(ctx, bin, args...); err2 == nil {
			a.noTune.Store(true)
			return nil
		} else {
			out, err = out2, err2
		}
	}
	if err != nil {
		return fmt.Errorf("avifenc: %v: %s", err, lastLine(out))
	}
	return nil
}

// Cjxl runs libjxl's cjxl for lossless JPEG recompression.
type Cjxl struct {
	Bin     string
	Version string
}

// FindCjxl returns cjxl from PATH (nil when missing).
func FindCjxl() *Cjxl {
	bin, err := exec.LookPath("cjxl")
	if err != nil {
		return nil
	}
	out, err := run(context.Background(), bin, "--version")
	if err != nil {
		return nil
	}
	return &Cjxl{Bin: bin, Version: firstLine(out)}
}

func (c *Cjxl) Name() string               { return "cjxl" }
func (c *Cjxl) Format() string             { return "jxl" }
func (c *Cjxl) Slow() bool                 { return false }
func (c *Cjxl) Accepts(format string) bool { return format == "jpeg" || format == "png" }

func (c *Cjxl) Encode(ctx context.Context, src, srcFormat, dst string, o Options, _ bool) error {
	args := []string{src, dst, "-e", strconv.Itoa(min(max(o.Speed, 1), 9)), "--num_threads", strconv.Itoa(max(o.Jobs, 1)), "--quiet"}
	if srcFormat == "jpeg" {
		args = append(args, "--lossless_jpeg=1")
	} else {
		args = append(args, "-d", "0")
	}
	bin, args := niceArgs(c.Bin, args)
	if out, err := run(ctx, bin, args...); err != nil {
		return fmt.Errorf("cjxl: %v: %s", err, lastLine(out))
	}
	return nil
}

// WASMAvif is the built-in AVIF encoder (libavif compiled to WebAssembly):
// it works everywhere, including the slim image, but is several times
// slower than avifenc and can't write grayscale-only AVIF.
type WASMAvif struct{}

// wasmMu serializes the encoder: the WebAssembly module is single-threaded.
var wasmMu sync.Mutex

func (WASMAvif) Name() string        { return "built-in" }
func (WASMAvif) Format() string      { return "avif" }
func (WASMAvif) Slow() bool          { return true }
func (WASMAvif) Accepts(string) bool { return false } // always decodes in Go

func (WASMAvif) Encode(ctx context.Context, src, _, dst string, o Options, gray bool) error {
	img, err := decodeFile(src)
	if err != nil {
		return err
	}
	if gray {
		img = toGray(img)
	}
	var buf bytes.Buffer
	wasmMu.Lock()
	err = avif.Encode(&buf, img, avif.Options{Quality: o.Quality, Speed: min(max(o.Speed, 0), 10)})
	wasmMu.Unlock()
	if err != nil {
		return fmt.Errorf("avif: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o664)
}

func toGray(img image.Image) image.Image {
	if g, ok := img.(*image.Gray); ok {
		return g
	}
	b := img.Bounds()
	g := image.NewGray(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			g.Set(x, y, img.At(x, y))
		}
	}
	return g
}

func firstLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func lastLine(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
