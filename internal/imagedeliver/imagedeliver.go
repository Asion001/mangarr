// Package imagedeliver makes display-sized copies of pages so a phone
// doesn't pull a 2400px scan for a 400px screen. Copies are cached on disk
// and, when a progressive JPEG encoder (cjpeg, mozjpeg) is on the PATH, they
// are progressive, so the page appears as it arrives instead of at the end.
package imagedeliver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"time"

	"github.com/Asion001/mangarr/internal/apitiming"
	"github.com/Asion001/mangarr/internal/diskcache"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/thumbs"
)

// Bucket holds display-sized copies in the image cache.
const Bucket = "variants"

// Widths are the sizes clients may ask for; anything else is rounded up, so
// a handful of copies covers every screen. The smallest is the reader's
// placeholder, shown blurred until the page itself arrives.
var Widths = []int{320, 720, 1080, 1440, 2160}

// keepFormats are already-small formats we never re-encode: decoding AVIF or
// JPEG XL costs more than the bytes it would save.
var keepFormats = map[string]bool{"avif": true, "jxl": true}

const variantTTL = 30 * 24 * time.Hour

// keepWithin is how much wider than asked (in percent) a page may be and
// still go out as it is. Shrinking a tall webtoon page takes the server a
// second or more, which a reader sees as a page loading, to save a few
// hundred pixels the screen hardly shows (a 1600px upscale for a 1440px
// column, say).
const keepWithin = 135

type Deliverer struct {
	Cache *diskcache.Store
	Log   *slog.Logger
	// Quality of the copies (JPEG).
	Quality int
	// progressive encodes progressive JPEG (nil when no encoder is installed).
	progressive *cjpeg
}

func New(cache *diskcache.Store, log *slog.Logger) *Deliverer {
	d := &Deliverer{Cache: cache, Log: log, Quality: 82, progressive: findCjpeg()}
	if d.progressive != nil && log != nil {
		log.Info("progressive JPEG encoder found", "bin", d.progressive.bin)
	}
	return d
}

// Progressive reports whether copies are progressive JPEGs.
func (d *Deliverer) Progressive() bool { return d != nil && d.progressive != nil }

// Width rounds a client's width up to the next size we keep, and returns 0
// when the page should be sent as it is.
func Width(want int) int {
	if want <= 0 {
		return 0
	}
	for _, w := range Widths {
		if want <= w {
			return w
		}
	}
	return 0
}

// Variant returns a copy of a page at most width pixels wide. It returns
// ok=false when the original should be sent instead (a format we keep, an
// image already small enough, or anything that won't decode). id must change
// with the page's bytes (a page ETag does).
func (d *Deliverer) Variant(ctx context.Context, id string, width int, load func(context.Context) ([]byte, error)) (data []byte, contentType string, ok bool) {
	if d == nil || d.Cache == nil || width <= 0 || id == "" {
		return nil, "", false
	}
	defer apitiming.Span(ctx, "variant")()
	key := fmt.Sprintf("%s|w%d|q%d|p%t", id, width, d.Quality, d.Progressive())
	out, ct, _, err := d.Cache.Get(ctx, Bucket, key, variantTTL, func(ctx context.Context) (io.ReadCloser, string, error) {
		src, err := load(ctx)
		if err != nil {
			return nil, "", err
		}
		small, ct, err := d.resize(ctx, src, width)
		if err != nil {
			return nil, "", err
		}
		return io.NopCloser(bytes.NewReader(small)), ct, nil
	})
	if err != nil {
		if d.Log != nil {
			d.Log.Debug("serving the page as it is", "reason", err)
		}
		return nil, "", false
	}
	return out, ct, true
}

// errKeepOriginal means the page is better sent unchanged.
var errKeepOriginal = fmt.Errorf("the original is the better copy")

func (d *Deliverer) resize(ctx context.Context, src []byte, width int) ([]byte, string, error) {
	info, err := imagecheck.Detect(src)
	if err != nil {
		return nil, "", err
	}
	if keepFormats[info.Format] || (info.Width > 0 && info.Width*100 <= width*keepWithin) {
		return nil, "", errKeepOriginal
	}
	out, ct, changed := thumbs.Normalize(src, width, d.Quality)
	if !changed {
		return nil, "", errKeepOriginal
	}
	if d.progressive != nil {
		if prog, err := d.progressive.encode(ctx, out, d.Quality); err == nil && len(prog) > 0 {
			return prog, "image/jpeg", nil
		} else if err != nil && d.Log != nil {
			d.Log.Debug("progressive JPEG failed; keeping the baseline one", "err", err)
		}
	}
	return out, ct, nil
}

// cjpeg is a progressive JPEG encoder from the PATH.
type cjpeg struct{ bin string }

func findCjpeg() *cjpeg {
	for _, name := range []string{"mozcjpeg", "cjpeg"} {
		if bin, err := exec.LookPath(name); err == nil {
			return &cjpeg{bin: bin}
		}
	}
	return nil
}

// encode re-encodes a JPEG as a progressive one (the encoder reads stdin).
func (c *cjpeg) encode(ctx context.Context, jpg []byte, quality int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.bin, "-progressive", "-optimize", "-quality", fmt.Sprint(quality))
	cmd.Stdin = bytes.NewReader(jpg)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", c.bin, err, errBuf.String())
	}
	if _, err := imagecheck.Detect(out.Bytes()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
