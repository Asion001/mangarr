// Package thumbs turns cover images into small, widely supported JPEGs for
// the web UI: sources often serve multi-megapixel originals or AVIF/WebP
// files that older Safari versions can't show (or can't hold in memory).
package thumbs

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif" // decoders
	"image/jpeg"
	_ "image/png"

	_ "github.com/gen2brain/avif"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Defaults for cached thumbnails and covers.
const (
	MaxWidth = 768
	Quality  = 85
	// KeepBelow keeps small, web-safe images that fit as they are.
	KeepBelow = 150 << 10
	// maxPixels skips absurd images (decompression bombs).
	maxPixels = 80_000_000
)

var webSafe = map[string]bool{"jpeg": true, "png": true, "gif": true}

// Normalize returns data as a JPEG at most maxWidth wide (and at most twice
// as tall). Small web-safe images that fit are returned unchanged, and so is
// anything that can't be decoded. changed reports whether data was replaced.
func Normalize(data []byte, maxWidth, quality int) (out []byte, contentType string, changed bool) {
	return Fit(data, maxWidth, maxWidth*2, quality)
}

// Fit is Normalize with its own height limit; 0 leaves the height free, so a
// tall page (a webtoon strip) keeps the width it was asked for.
func Fit(data []byte, maxWidth, maxHeight, quality int) (out []byte, contentType string, changed bool) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return data, "", false
	}
	if maxHeight <= 0 {
		maxHeight = cfg.Height
	}
	fits := cfg.Width <= maxWidth && cfg.Height <= maxHeight
	if fits && webSafe[format] && len(data) <= KeepBelow {
		return data, "image/" + format, false
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, "", false
	}
	w, h := cfg.Width, cfg.Height
	if w > maxWidth {
		h, w = h*maxWidth/w, maxWidth
	}
	if h > maxHeight {
		w, h = w*maxHeight/h, maxHeight
	}
	w, h = max(w, 1), max(h, 1)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// transparent areas become white instead of black
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	if w == cfg.Width && h == cfg.Height {
		draw.Draw(dst, dst.Bounds(), src, src.Bounds().Min, draw.Over)
	} else {
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return data, "", false
	}
	if fits && webSafe[format] && buf.Len() >= len(data) {
		return data, "image/" + format, false
	}
	return buf.Bytes(), "image/jpeg", true
}
