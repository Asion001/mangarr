// Package eink lays manga pages out for small e-ink screens and writes
// CrossPoint Reader's XTCH format (pre-rendered 2-bit grayscale pages).
package eink

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"image"
	"io"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// Screen is a portrait page size in pixels.
type Screen struct{ W, H int }

// X4 is the XTeink X4's panel, CrossPoint's reference device.
var X4 = Screen{W: 480, H: 800}

// ParseScreen reads "480x800"; empty means X4.
func ParseScreen(s string) (Screen, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return X4, nil
	}
	w, h, ok := strings.Cut(s, "x")
	if !ok {
		return Screen{}, fmt.Errorf("screen size %q is not WIDTHxHEIGHT", s)
	}
	sw, err1 := strconv.Atoi(w)
	sh, err2 := strconv.Atoi(h)
	if err1 != nil || err2 != nil || sw < 100 || sh < 100 || sw > 2000 || sh > 2000 {
		return Screen{}, fmt.Errorf("screen size %q is out of range (100 to 2000 pixels a side)", s)
	}
	return Screen{W: sw, H: sh}, nil
}

// tallRatio is how much taller than the screen a width-fitted page may be
// before it is cut into screen-high strips instead of shrunk to fit.
const tallRatio = 1.3

// overlap is how many pixels consecutive strips of a tall page share, so a
// line of text cut at a strip edge shows whole on one of them.
const overlap = 32

// Layout turns one page into screen-sized grayscale pages: spreads are
// turned sideways, ordinary pages are shrunk to fit and centred on white,
// and tall webtoon pages are fitted to the width and cut into strips.
func Layout(img image.Image, s Screen) []*image.Gray {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	// Flatten onto white first, so transparent pixels don't turn black.
	var src image.Image = flatten(img)
	if w > h && s.H > s.W {
		src, w, h = rotate(src.(*image.Gray)), h, w
	}
	b = src.Bounds()
	scaledH := float64(h) * float64(s.W) / float64(w)
	if scaledH <= float64(s.H)*tallRatio {
		scale := min(float64(s.W)/float64(w), float64(s.H)/float64(h))
		dw, dh := max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
		page := blank(s)
		x0, y0 := (s.W-dw)/2, (s.H-dh)/2
		draw.CatmullRom.Scale(page, image.Rect(x0, y0, x0+dw, y0+dh), src, b, draw.Src, nil)
		return []*image.Gray{page}
	}
	full := image.NewGray(image.Rect(0, 0, s.W, int(scaledH+0.5)))
	draw.CatmullRom.Scale(full, full.Bounds(), src, b, draw.Src, nil)
	var out []*image.Gray
	step := s.H - overlap
	for y := 0; ; y += step {
		if y+s.H >= full.Rect.Dy() {
			y = full.Rect.Dy() - s.H // last strip ends at the bottom edge
		}
		page := blank(s)
		draw.Draw(page, page.Bounds(), full, image.Pt(0, y), draw.Src)
		out = append(out, page)
		if y+s.H >= full.Rect.Dy() {
			return out
		}
	}
}

func blank(s Screen) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, s.W, s.H))
	for i := range g.Pix {
		g.Pix[i] = 0xff
	}
	return g
}

func flatten(img image.Image) *image.Gray {
	b := img.Bounds()
	g := blank(Screen{W: b.Dx(), H: b.Dy()})
	draw.Draw(g, g.Rect, img, b.Min, draw.Over)
	return g
}

// rotate turns a page a quarter clockwise.
func rotate(g *image.Gray) *image.Gray {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	out := image.NewGray(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.Pix[x*out.Stride+h-1-y] = g.Pix[y*g.Stride+x]
		}
	}
	return out
}

// levels maps a 2-bit shade (0 black .. 3 white) to XTH's pixel values,
// where 0 is white, 1 dark grey, 2 light grey and 3 black.
var levels = [4]byte{3, 1, 2, 0}

// quantize dithers a page to four shades (Floyd–Steinberg), returning one
// shade (0 black .. 3 white) per pixel, row by row.
func quantize(g *image.Gray) []byte {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	cur, next := make([]int, w+2), make([]int, w+2)
	out := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := int(g.Pix[y*g.Stride+x])*16 + cur[x+1]
			shade := (v + 16*85/2) / (16 * 85)
			shade = max(0, min(3, shade))
			out[y*w+x] = byte(shade)
			e := v - shade*85*16
			cur[x+2] += e * 7 / 16
			next[x] += e * 3 / 16
			next[x+1] += e * 5 / 16
			next[x+2] += e / 16
		}
		cur, next = next, cur
		clear(next)
	}
	return out
}

// xth packs a page as XTH bitmap data: two bit planes, columns from right
// to left, 8 vertical pixels per byte with the top pixel in the high bit.
func xth(g *image.Gray) []byte {
	w, h := g.Rect.Dx(), g.Rect.Dy()
	shades := quantize(g)
	colBytes := (h + 7) / 8
	plane := w * colBytes
	out := make([]byte, plane*2)
	for x := 0; x < w; x++ {
		col := (w - 1 - x) * colBytes
		for y := 0; y < h; y++ {
			v := levels[shades[y*w+x]]
			bit := byte(1) << (7 - y%8)
			if v&2 != 0 {
				out[col+y/8] |= bit
			}
			if v&1 != 0 {
				out[plane+col+y/8] |= bit
			}
		}
	}
	return out
}

const (
	headerSize   = 56
	titleOffset  = 0x38
	authorOffset = 0xB8
	metaEnd      = authorOffset + 64
	pageHeader   = 22
	tableEntry   = 16
)

// WriteXTCH writes pages (all the same size) as an XTCH book.
func WriteXTCH(w io.Writer, title, author string, pages []*image.Gray) error {
	if len(pages) == 0 {
		return fmt.Errorf("no pages")
	}
	if len(pages) > 0xffff {
		return fmt.Errorf("%d pages is more than XTCH holds", len(pages))
	}
	pw, ph := pages[0].Rect.Dx(), pages[0].Rect.Dy()
	dataSize := pw * ((ph + 7) / 8) * 2
	tableOffset := uint64(metaEnd)
	dataOffset := tableOffset + uint64(len(pages))*tableEntry

	head := make([]byte, metaEnd)
	le := binary.LittleEndian
	copy(head[0:4], "XTCH")
	head[4] = 1 // version 1.0
	le.PutUint16(head[6:], uint16(len(pages)))
	head[9] = 1 // has metadata
	le.PutUint32(head[0x0C:], 1)
	le.PutUint64(head[0x18:], tableOffset)
	le.PutUint64(head[0x20:], dataOffset)
	copy(head[titleOffset:authorOffset-1], truncate(title, 127))
	copy(head[authorOffset:metaEnd-1], truncate(author, 63))

	bw := bufio.NewWriter(w)
	if _, err := bw.Write(head); err != nil {
		return err
	}
	entry := make([]byte, tableEntry)
	for i := range pages {
		if pages[i].Rect.Dx() != pw || pages[i].Rect.Dy() != ph {
			return fmt.Errorf("page %d is %dx%d, not %dx%d", i+1, pages[i].Rect.Dx(), pages[i].Rect.Dy(), pw, ph)
		}
		le.PutUint64(entry[0:], dataOffset+uint64(i)*uint64(pageHeader+dataSize))
		le.PutUint32(entry[8:], uint32(pageHeader+dataSize))
		le.PutUint16(entry[12:], uint16(pw))
		le.PutUint16(entry[14:], uint16(ph))
		if _, err := bw.Write(entry); err != nil {
			return err
		}
	}
	ph22 := make([]byte, pageHeader)
	copy(ph22[0:4], "XTH\x00")
	le.PutUint16(ph22[4:], uint16(pw))
	le.PutUint16(ph22[6:], uint16(ph))
	le.PutUint32(ph22[10:], uint32(dataSize))
	for _, p := range pages {
		if _, err := bw.Write(ph22); err != nil {
			return err
		}
		if _, err := bw.Write(xth(p)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// truncate cuts s to at most n bytes without splitting a UTF-8 character.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xc0 == 0x80 {
		n--
	}
	return s[:n]
}
