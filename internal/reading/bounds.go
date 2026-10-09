package reading

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/apitiming"
	"github.com/Asion001/mangarr/internal/model"
)

// Bounds is a page's size and the box inside its uniform borders (white or
// black margins and scan edges), for the web reader's "crop borders".
type Bounds struct {
	Width  int `json:"width"`
	Height int `json:"height"`
	X      int `json:"x"`
	Y      int `json:"y"`
	W      int `json:"w"`
	H      int `json:"h"`
}

// boundsCache remembers computed bounds (keyed by file or release and page).
type boundsCache struct {
	mu sync.Mutex
	m  map[string]Bounds
}

func (c *boundsCache) get(k string) (Bounds, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.m[k]
	return b, ok
}

func (c *boundsCache) put(k string, b Bounds) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) > 20000 {
		c.m = map[string]Bounds{}
	}
	c.m[k] = b
}

// boundsKey identifies a page's bounds: its file (and when it was imported)
// or, for a chapter that isn't downloaded, the chapter itself.
func boundsKey(b *BookInfo, n int) string {
	if b.File != nil {
		return fmt.Sprintf("f%d|%d|%d", b.File.ID, b.File.ImportedAt.Unix(), n)
	}
	return fmt.Sprintf("ch%d|%d", b.Chapter.ID, n)
}

// PageBounds returns page n's size and content box.
func (s *Service) PageBounds(ctx context.Context, b *BookInfo, n int) (Bounds, error) {
	key := boundsKey(b, n)
	if bd, ok := s.bounds.get(key); ok {
		return bd, nil
	}
	if stored, ok := s.storedBounds(ctx, b)[n]; ok {
		s.bounds.put(key, stored)
		return stored, nil
	}
	bd, err := s.measure(ctx, b, n)
	if err != nil {
		return Bounds{}, err
	}
	s.bounds.put(key, bd)
	s.saveBounds(ctx, b, n, bd)
	return bd, nil
}

// storedBounds are the boxes measured before for a downloaded file, by
// page (none for a chapter that isn't downloaded, or a file rewritten since).
func (s *Service) storedBounds(ctx context.Context, b *BookInfo) map[int]Bounds {
	if b.File == nil || b.File.SHA256 == "" {
		return nil
	}
	var rows []model.PageBounds
	if err := s.DB.NewSelect().Model(&rows).Where("file_id = ? AND sha256 = ?", b.File.ID, b.File.SHA256).Scan(ctx); err != nil {
		return nil
	}
	out := make(map[int]Bounds, len(rows))
	for _, r := range rows {
		out[r.Page] = Bounds{Width: r.Width, Height: r.Height, X: r.X, Y: r.Y, W: r.W, H: r.H}
	}
	return out
}

// saveBounds keeps a downloaded page's box with the file, so it is never
// measured again (not after a restart either).
func (s *Service) saveBounds(ctx context.Context, b *BookInfo, n int, bd Bounds) {
	if b.File == nil || b.File.SHA256 == "" {
		return
	}
	row := &model.PageBounds{FileID: b.File.ID, Page: n, SHA256: b.File.SHA256, Width: bd.Width, Height: bd.Height, X: bd.X, Y: bd.Y, W: bd.W, H: bd.H}
	_, _ = s.DB.NewInsert().Model(row).On("CONFLICT (file_id, page) DO UPDATE").
		Set("sha256 = EXCLUDED.sha256").Set("width = EXCLUDED.width").Set("height = EXCLUDED.height").
		Set("x = EXCLUDED.x").Set("y = EXCLUDED.y").Set("w = EXCLUDED.w").Set("h = EXCLUDED.h").Exec(ctx)
}

// MeasureFile measures every page of a downloaded file that isn't stored
// yet, one page at a time and gently (avifdec on one thread), for the
// background: the reader then finds every box ready.
func (s *Service) MeasureFile(ctx context.Context, fileID int64) (int, error) {
	b, err := s.FileBook(ctx, fileID)
	if err != nil {
		return 0, err
	}
	pages, err := s.Pages(ctx, b)
	if err != nil {
		return 0, err
	}
	have := s.storedBounds(ctx, b)
	ctx = context.WithValue(ctx, gentleKey{}, true)
	measured := 0
	for _, p := range pages {
		if _, ok := have[p.Number]; ok {
			continue
		}
		if ctx.Err() != nil {
			return measured, ctx.Err()
		}
		bd, err := s.measure(ctx, b, p.Number)
		if err != nil {
			continue // a page that can't be decoded is measured if a reader asks
		}
		s.bounds.put(boundsKey(b, p.Number), bd)
		s.saveBounds(ctx, b, p.Number, bd)
		measured++
	}
	return measured, nil
}

// gentleKey marks background work: decoders keep to one thread.
type gentleKey struct{}

// measure decodes page n and finds its content box.
func (s *Service) measure(ctx context.Context, b *BookInfo, n int) (Bounds, error) {
	data, _, err := s.Page(ctx, b, n)
	if err != nil {
		return Bounds{}, err
	}
	defer apitiming.Span(ctx, "decode")()
	img, err := decodePage(data, ctx.Value(gentleKey{}) != nil)
	if err != nil {
		return Bounds{}, fmt.Errorf("decode page %d: %w", n, err)
	}
	return ContentBox(img), nil
}

// ContentBox finds the box inside an image's uniform borders. A side is
// trimmed while its rows (or columns) are nearly all white or nearly all
// black; the box keeps a little margin, and pages that would lose most of
// themselves (blank or very light pages) aren't cropped.
func ContentBox(img image.Image) Bounds {
	r := img.Bounds()
	w, h := r.Dx(), r.Dy()
	full := Bounds{Width: w, Height: h, W: w, H: h}
	if w < 16 || h < 16 {
		return full
	}
	luma := func(x, y int) int {
		cr, cg, cb, _ := img.At(r.Min.X+x, r.Min.Y+y).RGBA()
		return int((299*cr + 587*cg + 114*cb) / 1000 >> 8)
	}
	// decoded pages are mostly YCbCr or grey: read their luma directly
	switch m := img.(type) {
	case *image.YCbCr:
		luma = func(x, y int) int { return int(m.Y[m.YOffset(r.Min.X+x, r.Min.Y+y)]) }
	case *image.Gray:
		luma = func(x, y int) int { return int(m.Pix[m.PixOffset(r.Min.X+x, r.Min.Y+y)]) }
	}
	// Sample densely enough that a thin stroke (a speech bubble's outline)
	// shows up on neighbouring lines at the same place.
	stepX, stepY := max(1, w/1200), max(1, h/1200)
	// Border kinds: a line where nearly every sample is light or dark. Scan
	// margins are often grey after JPEG compression, and a page number can
	// occupy a small part of an otherwise empty row, so use broad tones and
	// tolerate up to 8% outliers.
	const (
		none = iota
		light
		dark
	)
	off := func(v, k int) bool {
		if k == light {
			return v < 180
		}
		return v > 75
	}
	kind := func(vals []int) int {
		n, l, d := len(vals), 0, 0
		for _, v := range vals {
			if !off(v, light) {
				l++
			} else if !off(v, dark) {
				d++
			}
		}
		if n == 0 {
			return none
		}
		limit := n - max(1, (n*8+99)/100)
		switch {
		case l >= limit:
			return light
		case d >= limit:
			return dark
		}
		return none
	}
	row := func(y int, vals []int) []int {
		for x := 0; x < w; x += stepX {
			vals = append(vals, luma(x, y))
		}
		return vals
	}
	col := func(y0, y1 int) func(int, []int) []int {
		return func(x int, vals []int) []int {
			for y := y0; y < y1; y += stepY {
				vals = append(vals, luma(x, y))
			}
			return vals
		}
	}
	// Each edge trims only the colour it starts with, so a white margin
	// stops at a black frame instead of eating into it.
	//
	// The 8% tolerance also passes lines that cut through real content: a
	// column along the edge of a speech bubble, lettering or bleeding art is
	// mostly paper with a few strokes. Dust and noise land in different
	// places on neighbouring lines while ink carries on from one line to the
	// next, so a line whose off-tone samples repeat the previous line's is
	// inked. A small inked island followed by a wide clean gap (a page
	// number) is still margin; any other ink is content, and the edge stays
	// in front of it.
	trim := func(from, to, step int, line func(int, []int) []int) int {
		cur := line(from, nil)
		k := kind(cur)
		if k == none {
			return from
		}
		size := (to - from) * step
		maxIsland, minGap := max(2, size*3/100), max(2, size*3/100)
		var prev []int
		end := from
		island, gap := 0, 0 // island: lines since the current island's first ink
		for i := from; i != to; i += step {
			if i != from {
				prev, cur = cur, line(i, prev[:0])
				if kind(cur) != k {
					break
				}
			}
			inked := false
			for j, v := range cur {
				if off(v, k) && (prev == nil || off(prev[j], k)) {
					inked = true
					break
				}
			}
			switch {
			case inked:
				island += gap + 1
				gap = 0
				if island > maxIsland {
					return end
				}
			case island == 0:
				end = i + step
			default:
				if gap++; gap >= minGap {
					end, island, gap = i+step, 0, 0
				}
			}
		}
		return end
	}
	top := trim(0, h, 1, row)
	bottom := trim(h-1, top-1, -1, row) + 1
	left := trim(0, w, 1, col(top, bottom))
	right := trim(w-1, left-1, -1, col(top, bottom)) + 1
	cw, ch := right-left, bottom-top
	if cw < w*3/10 || ch < h*3/10 { // blank or nearly blank: leave it
		return full
	}
	// keep a small margin around the content
	mx, my := w/100, h/100
	left, top = max(0, left-mx), max(0, top-my)
	right, bottom = min(w, right+mx), min(h, bottom+my)
	return Bounds{Width: w, Height: h, X: left, Y: top, W: right - left, H: bottom - top}
}

// PageBoundsMany returns the bounds of several pages at once, so the reader
// asks once per chapter instead of once per page. Pages already measured come
// back immediately; the rest are decoded in parallel until budget runs out,
// and whatever is missing can still be asked for one page at a time.
func (s *Service) PageBoundsMany(ctx context.Context, b *BookInfo, pages []int, budget time.Duration) map[int]Bounds {
	out := make(map[int]Bounds, len(pages))
	var todo []int
	var stored map[int]Bounds
	for _, n := range pages {
		if bd, ok := s.bounds.get(boundsKey(b, n)); ok {
			out[n] = bd
			continue
		}
		if stored == nil {
			stored = s.storedBounds(ctx, b) // one query for the whole file
			if stored == nil {
				stored = map[int]Bounds{}
			}
		}
		if bd, ok := stored[n]; ok {
			s.bounds.put(boundsKey(b, n), bd)
			out[n] = bd
		} else {
			todo = append(todo, n)
		}
	}
	if len(todo) == 0 || budget <= 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	workers := max(1, min(runtime.NumCPU()/2, 4)) // decoding is CPU heavy; leave room for the rest
	var mu sync.Mutex
	var wg sync.WaitGroup
	work := make(chan int)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range work {
				bd, err := s.PageBounds(ctx, b, n)
				if err != nil {
					continue // out of budget, or a page that can't be decoded
				}
				mu.Lock()
				out[n] = bd
				mu.Unlock()
			}
		}()
	}
	for _, n := range todo {
		select {
		case work <- n:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(work)
	wg.Wait()
	return out
}

// FileAt finds the downloaded file of seriesID at path (0 when none is).
func (s *Service) FileAt(ctx context.Context, seriesID int64, path string) int64 {
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Column("id", "root_folder_id", "path").Where("id = ?", seriesID).Scan(ctx); err != nil {
		return 0
	}
	dir, err := s.Library.SeriesDir(ctx, &ser)
	if err != nil || dir == "" {
		return 0
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return 0
	}
	var f model.ChapterFile
	if err := s.DB.NewSelect().Model(&f).Column("id").Where("series_id = ? AND relative_path = ?", seriesID, filepath.ToSlash(rel)).Limit(1).Scan(ctx); err != nil {
		return 0
	}
	return f.ID
}

// ForgetBounds empties the in-memory boxes (the stored ones stay).
func (s *Service) ForgetBounds() {
	s.bounds.mu.Lock()
	s.bounds.m = nil
	s.bounds.mu.Unlock()
}
