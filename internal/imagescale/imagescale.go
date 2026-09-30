// Package imagescale resizes pages without the memory a one-call resize
// takes. x/image's kernel scaler keeps a float64 copy of every pixel of a
// (destination width × source height) image — 1.6 GB for a 4x-upscaled
// webtoon strip brought down to 2500 wide — which is what a worker with a
// memory limit dies of.
package imagescale

import (
	"image"

	"golang.org/x/image/draw"
)

// band is how many rows (then columns) are scaled at once. It bounds the
// scaler's scratch memory and nothing else: the result doesn't depend on it.
const band = 64

// CatmullRom returns src scaled to w×h with the Catmull-Rom kernel. It is
// meant for making pages smaller: the 8-bit step between its two passes
// clips the overshoot a kernel adds when enlarging.
//
// It scales one axis at a time. Along the axis that isn't being scaled the
// kernel is the identity, so rows (and then columns) are independent and
// can go through in bands with no seams between them.
func CatmullRom(src image.Image, w, h int) *image.RGBA {
	b := src.Bounds()
	// horizontal pass: w wide, still as tall as the source
	mid := image.NewRGBA(image.Rect(0, 0, w, b.Dy()))
	for y := 0; y < b.Dy(); y += band {
		y1 := min(y+band, b.Dy())
		draw.CatmullRom.Scale(mid, image.Rect(0, y, w, y1), src, image.Rect(b.Min.X, b.Min.Y+y, b.Max.X, b.Min.Y+y1), draw.Src, nil)
	}
	if b.Dy() == h {
		return mid
	}
	// vertical pass
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x += band {
		x1 := min(x+band, w)
		draw.CatmullRom.Scale(dst, image.Rect(x, 0, x1, h), mid, image.Rect(x, 0, x1, b.Dy()), draw.Src, nil)
	}
	return dst
}
