package moodscore

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // page formats
	_ "image/png"
	"math"

	_ "github.com/gen2brain/avif"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// side is the model's image size.
const side = 224

// CLIP's per-channel mean and spread.
var (
	mean = [3]float32{0.48145466, 0.4578275, 0.40821073}
	std  = [3]float32{0.26862954, 0.26130258, 0.27577711}
)

// maxCrops is how many squares a tall page (or a webtoon strip) is looked
// at as: the model sees squares, and one middle square of a tall page
// misses most of it.
const maxCrops = 3

// Crops decodes a page and cuts it into up to maxCrops squares along its
// long side, each resized to side×side and normalised as CLIP expects
// (channels first). It returns them one after another.
func Crops(data []byte) ([][]float32, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding the page: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w < 8 || h < 8 {
		return nil, fmt.Errorf("the page is too small (%d×%d)", w, h)
	}
	short, long := min(w, h), max(w, h)
	n := int(math.Round(float64(long) / float64(short)))
	n = max(1, min(maxCrops, n))
	out := make([][]float32, 0, n)
	for i := range n {
		// squares spread evenly along the long side
		off := 0
		if n > 1 {
			off = (long - short) * i / (n - 1)
		} else {
			off = (long - short) / 2
		}
		var r image.Rectangle
		if h >= w {
			r = image.Rect(b.Min.X, b.Min.Y+off, b.Min.X+short, b.Min.Y+off+short)
		} else {
			r = image.Rect(b.Min.X+off, b.Min.Y, b.Min.X+off+short, b.Min.Y+short)
		}
		dst := image.NewRGBA(image.Rect(0, 0, side, side))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, r, draw.Src, nil)
		out = append(out, tensor(dst))
	}
	return out, nil
}

// tensor lays an RGBA square out as normalised CHW floats.
func tensor(img *image.RGBA) []float32 {
	out := make([]float32, 3*side*side)
	for y := range side {
		for x := range side {
			c := img.RGBAAt(x, y)
			// transparent pixels count as white paper
			px := color.RGBA{c.R + (255 - c.A), c.G + (255 - c.A), c.B + (255 - c.A), 255}
			i := y*side + x
			out[i] = (float32(px.R)/255 - mean[0]) / std[0]
			out[side*side+i] = (float32(px.G)/255 - mean[1]) / std[1]
			out[2*side*side+i] = (float32(px.B)/255 - mean[2]) / std[2]
		}
	}
	return out
}

// normalize scales a vector to length 1 (in place).
func normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	n := float32(1 / math.Sqrt(sum))
	for i := range v {
		v[i] *= n
	}
	return v
}

// mean of vectors, normalised.
func average(vs [][]float32) []float32 {
	if len(vs) == 0 {
		return nil
	}
	out := make([]float32, len(vs[0]))
	for _, v := range vs {
		for i, x := range v {
			out[i] += x
		}
	}
	return normalize(out)
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
