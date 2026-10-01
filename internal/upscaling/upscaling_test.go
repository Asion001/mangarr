package upscaling

import (
	"fmt"
	"testing"

	"github.com/Asion001/mangarr/internal/downloads"
)

func TestChooseScale(t *testing.T) {
	cases := []struct {
		w, min int
		scales []int
		want   int
	}{
		{720, 1400, []int{2, 4, 8}, 2},
		{600, 1400, []int{2, 4, 8}, 4},
		{600, 1400, []int{2, 3, 4}, 3},
		{200, 1400, []int{2, 3}, 3},
		{700, 1400, []int{4}, 4},
		{700, 1400, []int{1}, 0},
	}
	for _, c := range cases {
		if got := ChooseScale(c.w, c.min, c.scales); got != c.want {
			t.Errorf("ChooseScale(%d,%d,%v)=%d want %d", c.w, c.min, c.scales, got, c.want)
		}
	}
}

func TestNeedsUpscale(t *testing.T) {
	if !NeedsUpscale(downloads.PageFile{Width: 800, Format: "jpeg"}, 1400) {
		t.Error("narrow jpeg should be upscaled")
	}
	if NeedsUpscale(downloads.PageFile{Width: 1600, Format: "jpeg"}, 1400) {
		t.Error("wide page must be left alone")
	}
	if NeedsUpscale(downloads.PageFile{Width: 500, Format: "gif"}, 1400) {
		t.Error("gif must be left alone")
	}
	if NeedsUpscale(downloads.PageFile{Width: 0, Format: "avif"}, 1400) {
		t.Error("unknown width must be left alone")
	}
}

func TestOutputFormat(t *testing.T) {
	for _, c := range []struct{ profile, page, want string }{
		{"", "jpeg", "webp"},
		{"jpeg", "png", "jpeg"},
		{SourceFormat, "jpeg", "jpeg"},
		{SourceFormat, "webp", "webp"},
		{SourceFormat, "png", "png"},
		{SourceFormat, "bmp", "png"},
	} {
		if got := OutputFormat(c.profile, c.page); got != c.want {
			t.Errorf("OutputFormat(%q, %q) = %q, want %q", c.profile, c.page, got, c.want)
		}
	}
}

// TestChunks: runs stop at ChunkPages pages or ChunkPixels upscaled
// pixels, whichever comes first, and a page bigger than the cap goes alone.
func TestChunks(t *testing.T) {
	page := func(w, h int) downloads.PageFile { return downloads.PageFile{Width: w, Height: h} }
	var pages []downloads.PageFile
	for range 10 {
		pages = append(pages, page(1000, 1500)) // 6 MP at 2x, 24 MP at 4x
	}
	pages = append(pages, page(720, 10000), page(720, 10000), page(720, 10000)) // 115 MP each at 4x
	all := func(from, to int) []int {
		var out []int
		for i := from; i < to; i++ {
			out = append(out, i)
		}
		return out
	}
	sizes := func(c [][]int) []int {
		var out []int
		for _, x := range c {
			out = append(out, len(x))
		}
		return out
	}
	if got := sizes(chunks(pages, all(0, 10), 4)); fmt.Sprint(got) != "[5 5]" {
		t.Fatalf("manga pages at 4x: %v", got)
	}
	if got := sizes(chunks(pages, all(0, 10), 2)); fmt.Sprint(got) != "[8 2]" {
		t.Fatalf("manga pages at 2x: %v", got)
	}
	if got := sizes(chunks(pages, all(10, 13), 4)); fmt.Sprint(got) != "[1 1 1]" {
		t.Fatalf("webtoon strips at 4x: %v", got)
	}
}
