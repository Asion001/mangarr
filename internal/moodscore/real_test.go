//go:build onnx

package moodscore

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"testing"
)

// TestRealModel runs the real model when MANGARR_MOODSCORE_MODEL names a
// folder holding it (it's too large to download in CI).
func TestRealModel(t *testing.T) {
	dir := os.Getenv("MANGARR_MOODSCORE_MODEL")
	if dir == "" || !ModelReady(dir) {
		t.Skip("MANGARR_MOODSCORE_MODEL isn't set to a folder with the model")
	}
	if ok, why := Available(); !ok {
		t.Skip(why)
	}
	ctx := context.Background()
	s, err := New(ctx, Config{ModelDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	o := s.(*onnxScorer)
	emb, err := o.textEmbeddings(ctx, []Prompt{{1, "heavy rain and a storm"}, {2, "a thunderstorm at night"}, {3, "a romantic kiss"}})
	if err != nil {
		t.Fatal(err)
	}
	if near, far := dot(emb[0], emb[1]), dot(emb[0], emb[2]); near <= far {
		t.Fatalf("rain should be nearer a storm than a kiss: %v vs %v", near, far)
	}
	// a dark page and a bright pink one
	page := func(c color.RGBA, w, h int) []byte {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			for x := range w {
				img.Set(x, y, c)
			}
		}
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		return b.Bytes()
	}
	prompts := []Prompt{{1, "a dark, scary scene at night"}, {2, "a bright pink cheerful picture"}}
	done := 0
	scores, err := s.Score(ctx, [][]byte{page(color.RGBA{5, 5, 10, 255}, 600, 1800), page(color.RGBA{255, 140, 200, 255}, 800, 800)}, prompts, func(d, _ int) { done = d })
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("dark page %v, pink page %v", scores[0], scores[1])
	if done != 2 || scores[0][1] <= scores[0][2] || scores[1][2] <= scores[1][1] {
		t.Fatalf("scores %v", scores)
	}
}
