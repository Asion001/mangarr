package processing

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/gen2brain/avif"
	"github.com/gen2brain/webp"
	"golang.org/x/image/bmp"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/imageenc"
)

// Webtoon strips (pages taller than the profile's split ratio times their
// width) are cut into balanced segments no taller than the segment ratio
// times their width. Ratios, not pixels, decide, so an upscaled manga page is
// never mistaken for a strip. A full-width light/dark quiet band near the
// balanced cut is preferred; when none exists the balanced cut is used.

// isStrip reports whether a page is taller than threshold times its width.
func isStrip(pg downloads.PageFile, threshold float64) bool {
	return pg.Width > 0 && float64(pg.Height) > float64(pg.Width)*threshold
}

func splitSupported(format string) bool {
	switch format {
	case "jpeg", "png", "webp", "bmp", "avif":
		return true
	}
	return false // animations and JPEG XL stay untouched
}

func splitTallPage(ctx context.Context, pg downloads.PageFile, limit int, toPNG bool, workDir string) ([]downloads.PageFile, error) {
	f, err := os.Open(pg.Path)
	if err != nil {
		return nil, err
	}
	var src image.Image
	if pg.Format == "avif" {
		src, err = avif.Decode(f)
	} else {
		src, _, err = image.Decode(f)
	}
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("split %s: %w", pg.Name, err)
	}
	b := src.Bounds()
	cuts := splitCuts(src, limit)
	if len(cuts) == 0 {
		return []downloads.PageFile{pg}, nil
	}
	dir := filepath.Join(workDir, "split")
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return nil, err
	}
	format := pg.Format
	if toPNG {
		format = "png"
	}
	points := append([]int{0}, cuts...)
	points = append(points, b.Dy())
	out := make([]downloads.PageFile, 0, len(points)-1)
	for i := 0; i+1 < len(points); i++ {
		h := points[i+1] - points[i]
		segment := image.NewNRGBA(image.Rect(0, 0, b.Dx(), h))
		draw.Draw(segment, segment.Bounds(), src, image.Pt(b.Min.X, b.Min.Y+points[i]), draw.Src)
		name := fmt.Sprintf("%s-%03d%s", trimImageExt(pg.Name), i+1, imagecheck.Ext(format))
		path := filepath.Join(dir, name)
		if err := encodeSplit(ctx, path, format, segment); err != nil {
			return nil, fmt.Errorf("split %s part %d: %w", pg.Name, i+1, err)
		}
		out = append(out, downloads.PageFile{Name: name, Path: path, Format: format, Width: b.Dx(), Height: h})
	}
	return out, nil
}

// encodeNative hands img to an external encoder through a temporary PNG.
func encodeNative(ctx context.Context, path string, img image.Image, encode func(src string) error) error {
	tmp := path + ".src.png"
	defer os.Remove(tmp)
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(f, img)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return encode(tmp)
}

func trimImageExt(name string) string { return name[:len(name)-len(filepath.Ext(name))] }

// The native encoders, when installed: the built-in WebP and AVIF encoders
// are WebAssembly builds that run on one core and several times slower.
var (
	cwebpBin   = sync.OnceValue(func() string { p, _ := exec.LookPath("cwebp"); return p })
	avifencBin = sync.OnceValue(imageenc.FindAvifenc)
)

func encodeSplit(ctx context.Context, path, format string, img image.Image) error {
	switch {
	case format == "webp" && cwebpBin() != "":
		return encodeNative(ctx, path, img, func(src string) error {
			out, err := exec.CommandContext(ctx, cwebpBin(), "-quiet", "-mt", "-q", "90", "-m", "4", src, "-o", path).CombinedOutput()
			if err != nil {
				return fmt.Errorf("cwebp: %w: %s", err, out)
			}
			return nil
		})
	case format == "avif" && avifencBin() != nil:
		return encodeNative(ctx, path, img, func(src string) error {
			o := imageenc.Options{Quality: 60, Speed: 8, Jobs: runtime.NumCPU()}
			return avifencBin().Encode(ctx, src, "png", path, o, false)
		})
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	switch format {
	case "jpeg":
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: 92})
	case "webp":
		err = webp.Encode(f, img, webp.Options{Quality: 90, Method: 4})
	case "bmp":
		err = bmp.Encode(f, img)
	case "avif":
		err = avif.Encode(f, img, avif.Options{Quality: 60, Speed: 8})
	default:
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(f, img)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// splitCuts returns cut positions relative to img.Bounds().Min.Y.
func splitCuts(img image.Image, limit int) []int {
	h := img.Bounds().Dy()
	if limit <= 0 || h <= limit {
		return nil
	}
	var cuts []int
	start := 0
	for h-start > limit {
		parts := (h - start + limit - 1) / limit
		target := start + (h-start+parts-1)/parts
		tolerance := max(limit/5, 24)
		low := max(start+1, target-tolerance, h-(parts-1)*limit)
		high := min(start+limit, target+tolerance)
		cut := quietCut(img, target, low, high)
		if cut == 0 {
			cut = target
		}
		cuts = append(cuts, cut)
		start = cut
	}
	return cuts
}

func quietCut(img image.Image, target, low, high int) int {
	for offset := 0; target-offset >= low || target+offset <= high; offset++ {
		for _, y := range []int{target - offset, target + offset} {
			if y < low || y > high || y < 2 || y+2 >= img.Bounds().Dy() {
				continue
			}
			quiet := true
			for dy := -2; dy <= 2; dy++ {
				if !quietRow(img, y+dy) {
					quiet = false
					break
				}
			}
			if quiet {
				return y
			}
		}
	}
	return 0
}

func quietRow(img image.Image, relativeY int) bool {
	b := img.Bounds()
	y := b.Min.Y + relativeY
	minL, maxL := 255, 0
	for x := b.Min.X; x < b.Max.X; x++ {
		r, g, bl, a := img.At(x, y).RGBA()
		if a == 0 {
			continue
		}
		l := (299*int(r>>8) + 587*int(g>>8) + 114*int(bl>>8)) / 1000
		minL, maxL = min(minL, l), max(maxL, l)
		if maxL-minL > 18 {
			return false
		}
	}
	return maxL <= 45 || minL >= 210
}
