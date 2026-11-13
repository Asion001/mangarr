package eink

import (
	"image"
	"math"
)

// inkLevel is the lightest value that still counts as ink when finding a
// page's margins; paper in scans is rarely pure white.
const inkLevel = 0xe0

// Trim cuts a page's white margins (keeping a thin border) so the art uses
// more of a small screen. A page that is mostly margin, or has none, is
// returned as it is.
func Trim(g *image.Gray) *image.Gray {
	b := g.Rect
	w, h := b.Dx(), b.Dy()
	if w < 16 || h < 16 {
		return g
	}
	// A row or column holds content when more than a few pixels are ink,
	// so specks and scanner dust don't count.
	rowInk := make([]int, h)
	colInk := make([]int, w)
	for y := 0; y < h; y++ {
		row := g.Pix[y*g.Stride : y*g.Stride+w]
		for x, v := range row {
			if v < inkLevel {
				rowInk[y]++
				colInk[x]++
			}
		}
	}
	first := func(counts []int, need int) int {
		for i, c := range counts {
			if c > need {
				return i
			}
		}
		return -1
	}
	last := func(counts []int, need int) int {
		for i := len(counts) - 1; i >= 0; i-- {
			if counts[i] > need {
				return i
			}
		}
		return -1
	}
	rowNeed, colNeed := max(2, w/200), max(2, h/200)
	top, bottom := first(rowInk, rowNeed), last(rowInk, rowNeed)
	left, right := first(colInk, colNeed), last(colInk, colNeed)
	if top < 0 || left < 0 {
		return g // a blank page
	}
	padX, padY := w*15/1000+1, h*15/1000+1
	r := image.Rect(left-padX, top-padY, right+1+padX, bottom+1+padY).Add(b.Min).Intersect(b)
	// Keep the page when trimming would take little, or most of it: a
	// mostly empty page (a title, a sound effect) reads better whole.
	if r.Dx()*r.Dy() >= w*h*97/100 || r.Dx()*r.Dy() < w*h*40/100 {
		return g
	}
	return g.SubImage(r).(*image.Gray)
}

// Tone fits a page's levels to e-ink: a washed-out scan is stretched so its
// darkest ink is black and its paper white, and midtones are darkened a
// little, since the panel's four shades and dithering lighten thin lines.
func Tone(g *image.Gray) *image.Gray {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	if w == 0 || h == 0 {
		return g
	}
	var hist [256]int
	for y := 0; y < h; y++ {
		for _, v := range g.Pix[y*g.Stride : y*g.Stride+w] {
			hist[v]++
		}
	}
	n := w * h
	lo, hi := 0, 255
	for acc := 0; lo < 255; lo++ {
		if acc += hist[lo]; acc > n/200 {
			break
		}
	}
	for acc := 0; hi > 0; hi-- {
		if acc += hist[hi]; acc > n/200 {
			break
		}
	}
	// Stretch only so far: a dark page stays dark, a grey cover stays grey.
	lo, hi = min(lo, 64), max(hi, 192)
	if hi <= lo {
		return g
	}
	var lut [256]uint8
	for v := range lut {
		t := float64(v-lo) / float64(hi-lo)
		t = math.Max(0, math.Min(1, t))
		lut[v] = uint8(math.Pow(t, 1.15)*255 + 0.5)
	}
	out := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := g.Pix[y*g.Stride : y*g.Stride+w]
		dst := out.Pix[y*out.Stride : y*out.Stride+w]
		for x, v := range src {
			dst[x] = lut[v]
		}
	}
	return out
}
