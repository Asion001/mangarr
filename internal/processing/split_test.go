package processing

import (
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/gen2brain/webp"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/model"
)

func patternedStrip(w, h int, quiet ...int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8((x*7 + y) % 180), G: uint8(40 + (x*3+y*2)%160), B: uint8(30 + y%190), A: 255})
		}
	}
	for _, center := range quiet {
		for y := max(0, center-4); y <= min(h-1, center+4); y++ {
			for x := 0; x < w; x++ {
				img.SetNRGBA(x, y, color.NRGBA{R: 245, G: 245, B: 245, A: 255})
			}
		}
	}
	return img
}

func writeTestImage(t *testing.T, dir, name, format string, img image.Image) downloads.PageFile {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if format == "webp" {
		err = webp.Encode(f, img, webp.Options{Quality: 90})
	} else {
		err = png.Encode(f, img)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	return downloads.PageFile{Name: name, Path: path, Format: format, Width: b.Dx(), Height: b.Dy()}
}

func TestSplitTallPagesUsesQuietRowsAndRenumbers(t *testing.T) {
	dir := t.TempDir()
	tall := writeTestImage(t, dir, "source.png", "png", patternedStrip(80, 620, 202, 411))
	normal := writeTestImage(t, dir, "other.png", "png", patternedStrip(80, 180))
	res, err := New(nil, nil).Process(context.Background(), model.ProfileConfig{
		Pages: model.PageRules{SplitTall: true, SplitRatio: 3, SegmentRatio: 3.125}, // segments up to 250 px
	}, []downloads.PageFile{tall, normal}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Split != 1 || len(res.Pages) != 4 || !res.Changed {
		t.Fatalf("result: split=%d pages=%d changed=%v", res.Split, len(res.Pages), res.Changed)
	}
	wantMap := []int{0, 0, 0, 1}
	total := 0
	for i, pg := range res.Pages {
		if pg.Name != []string{"0001.png", "0002.png", "0003.png", "0004.png"}[i] {
			t.Errorf("page %d name = %q", i, pg.Name)
		}
		if pg.Height > 250 {
			t.Errorf("page %d height = %d", i, pg.Height)
		}
		if res.SourcePages[i] != wantMap[i] {
			t.Errorf("page %d source = %d, want %d", i, res.SourcePages[i], wantMap[i])
		}
		if i < 3 {
			total += pg.Height
		}
	}
	if total != 620 {
		t.Fatalf("split heights total %d", total)
	}
	if res.Pages[0].Height < 195 || res.Pages[0].Height > 209 || res.Pages[1].Height < 195 || res.Pages[1].Height > 215 {
		t.Fatalf("quiet rows were not used: heights %d, %d", res.Pages[0].Height, res.Pages[1].Height)
	}
	if res.Pages[3].Path != normal.Path {
		t.Fatal("normal page bytes were rewritten")
	}
}

func TestSplitTallPagePreservesWebPWithoutEncoder(t *testing.T) {
	dir := t.TempDir()
	page := writeTestImage(t, dir, "strip.webp", "webp", patternedStrip(64, 360, 180))
	parts, err := splitTallPage(context.Background(), page, 200, false, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("parts = %d", len(parts))
	}
	for _, part := range parts {
		if part.Format != "webp" || filepath.Ext(part.Path) != ".webp" {
			t.Fatalf("format not preserved: %+v", part)
		}
	}
}

func TestSplitTallPageFallsBackToBalancedHardCuts(t *testing.T) {
	img := patternedStrip(40, 501)
	cuts := splitCuts(img, 200)
	points := append([]int{0}, cuts...)
	points = append(points, 501)
	if len(points) != 4 {
		t.Fatalf("cuts = %v", cuts)
	}
	for i := 0; i+1 < len(points); i++ {
		if h := points[i+1] - points[i]; h > 200 || h < 160 {
			t.Errorf("segment %d height = %d", i, h)
		}
	}
}

// An upscaled manga page is tall in pixels but not a strip: splitting goes by
// shape, so it stays whole with the default ratios.
func TestSplitKeepsUpscaledMangaPagesWhole(t *testing.T) {
	dir := t.TempDir()
	page := writeTestImage(t, dir, "page.png", "png", patternedStrip(1950, 2799))
	strip := writeTestImage(t, dir, "strip.png", "png", patternedStrip(300, 3000))
	res, err := New(nil, nil).Process(context.Background(), model.ProfileConfig{
		Pages: model.PageRules{JunkUnder: -1, SplitTall: true},
	}, []downloads.PageFile{page, strip}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Split != 1 || res.Pages[0].Path != page.Path {
		t.Fatalf("manga page was split: split=%d pages=%+v", res.Split, res.Pages)
	}
	if len(res.Pages) != 1+5 { // 3000 / (300 * 2) = 5 segments
		t.Fatalf("strip segments = %d", len(res.Pages)-1)
	}
	for _, pg := range res.Pages[1:] {
		if pg.Width != 300 || pg.Height > 600 {
			t.Fatalf("segment %+v is taller than twice its width", pg)
		}
	}
}
