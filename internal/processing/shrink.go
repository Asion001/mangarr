package processing

import (
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/Asion001/mangarr/internal/imagescale"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
)

// shrinkLimit is the widest a page may be: twice the limit for a landscape
// two-page spread.
func shrinkLimit(pg downloads.PageFile, maxWidth int) int {
	if pg.Width > pg.Height && pg.Height > 0 {
		return maxWidth * 2
	}
	return maxWidth
}

// NeedsShrink reports whether a page is wider than the profile allows and
// can be resized here (animations, AVIF and JPEG XL are left alone).
func NeedsShrink(pg downloads.PageFile, maxWidth int) bool {
	if maxWidth <= 0 || pg.Width <= shrinkLimit(pg, maxWidth) {
		return false
	}
	switch pg.Format {
	case "jpeg", "png", "webp", "bmp":
		return true
	}
	return false
}

// shrink writes a page at the profile's maximum width. It goes on as PNG
// when an encoder follows (lossless hand-off); otherwise JPEG stays JPEG and
// everything else becomes PNG (Go has no WebP encoder).
func shrink(pg downloads.PageFile, maxWidth int, toPNG bool, workDir string) (downloads.PageFile, error) {
	f, err := os.Open(pg.Path)
	if err != nil {
		return pg, err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return pg, fmt.Errorf("shrink %s: %w", pg.Name, err)
	}
	w := shrinkLimit(pg, maxWidth)
	b := src.Bounds()
	h := b.Dy() * w / b.Dx()
	dst := imagescale.CatmullRom(src, w, h)

	format := "png"
	if pg.Format == "jpeg" && !toPNG {
		format = "jpeg"
	}
	dir := filepath.Join(workDir, "shrunk")
	if err := os.MkdirAll(dir, 0o775); err != nil {
		return pg, err
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png"}[format]
	name := strings.TrimSuffix(pg.Name, filepath.Ext(pg.Name)) + ext
	path := filepath.Join(dir, name)
	out, err := os.Create(path)
	if err != nil {
		return pg, err
	}
	if format == "jpeg" {
		err = jpeg.Encode(out, dst, &jpeg.Options{Quality: 90})
	} else {
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(out, dst)
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return pg, err
	}
	return downloads.PageFile{Name: name, Path: path, Format: format, Width: w, Height: h}, nil
}

// junkMask marks the pages under the profile's junk threshold.
func junkMask(pages []downloads.PageFile, rules model.PageRules) []bool {
	junk := rules.JunkSize()
	mask := make([]bool, len(pages))
	for i, pg := range pages {
		mask[i] = model.IsJunk(pg.Width, pg.Height, junk)
	}
	return mask
}
