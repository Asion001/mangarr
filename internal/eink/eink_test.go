package eink

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"
)

func solid(w, h int, c uint8) *image.Gray {
	g := image.NewGray(image.Rect(0, 0, w, h))
	for i := range g.Pix {
		g.Pix[i] = c
	}
	return g
}

func TestParseScreen(t *testing.T) {
	if s, err := ParseScreen(""); err != nil || s != X4 {
		t.Fatalf("default: %v %v", s, err)
	}
	if s, err := ParseScreen("528x792"); err != nil || s != (Screen{528, 792}) {
		t.Fatalf("custom: %v %v", s, err)
	}
	for _, bad := range []string{"480", "axb", "10x10", "5000x800"} {
		if _, err := ParseScreen(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func TestLayout(t *testing.T) {
	// An ordinary page is shrunk to fit and centred, one screen per page.
	pages := Layout(solid(1000, 1500, 0), X4)
	if len(pages) != 1 || pages[0].Rect.Dx() != 480 || pages[0].Rect.Dy() != 800 {
		t.Fatalf("page: %d pages", len(pages))
	}
	// 1000x1500 fits as 480x720: white bands top and bottom, black in the middle.
	if pages[0].GrayAt(240, 10).Y != 0xff || pages[0].GrayAt(240, 400).Y != 0 {
		t.Fatalf("page not centred")
	}
	// A spread is turned sideways to use the whole portrait screen.
	spread := solid(1600, 1000, 0xff)
	// Its top-left corner ends up top-right after a clockwise turn.
	for y := 0; y < 40; y++ {
		for x := 0; x < 40; x++ {
			spread.SetGray(x, y, color.Gray{0})
		}
	}
	pages = Layout(spread, X4)
	if len(pages) != 1 || pages[0].GrayAt(474, 24).Y > 0x40 || pages[0].GrayAt(10, 24).Y < 0xc0 {
		t.Fatalf("spread not rotated clockwise")
	}
	// A tall webtoon strip is fitted to the width and cut into screens.
	tall := solid(800, 8000, 0x80) // 480x4800 at screen width
	pages = Layout(tall, X4)
	if len(pages) != 7 { // strips of 800 stepping 768: 0, 768, …, 4000 (last aligned to the bottom)
		t.Fatalf("tall page: %d strips", len(pages))
	}
	for _, p := range pages {
		if p.Rect.Dx() != 480 || p.Rect.Dy() != 800 {
			t.Fatalf("strip size %v", p.Rect)
		}
	}
}

func TestWriteXTCH(t *testing.T) {
	page := solid(480, 800, 0xff)
	page.SetGray(0, 0, color.Gray{0}) // top-left black
	var buf bytes.Buffer
	if err := WriteXTCH(&buf, "Title", "Author", []*image.Gray{page, solid(480, 800, 0xff)}); err != nil {
		t.Fatal(err)
	}
	d := buf.Bytes()
	le := binary.LittleEndian
	if string(d[0:4]) != "XTCH" || d[4] != 1 || d[5] != 0 || le.Uint16(d[6:]) != 2 || d[9] != 1 {
		t.Fatalf("header % x", d[:16])
	}
	if string(d[titleOffset:titleOffset+5]) != "Title" || string(d[authorOffset:authorOffset+6]) != "Author" {
		t.Fatal("metadata")
	}
	table := le.Uint64(d[0x18:])
	dataSize := 480 * 100 * 2
	for i := 0; i < 2; i++ {
		e := d[table+uint64(i*16):]
		off, size := le.Uint64(e), le.Uint32(e[8:])
		if size != uint32(22+dataSize) || le.Uint16(e[12:]) != 480 || le.Uint16(e[14:]) != 800 {
			t.Fatalf("entry %d", i)
		}
		ph := d[off:]
		if string(ph[0:4]) != "XTH\x00" || le.Uint32(ph[10:]) != uint32(dataSize) {
			t.Fatalf("page header %d", i)
		}
		if i == 0 {
			bits := ph[22:]
			col := (480 - 1) * 100 // x = 0 is stored last (columns run right to left)
			if bits[col]&0x80 == 0 || bits[480*100+col]&0x80 == 0 {
				t.Fatal("black top-left pixel is not 3 in both planes")
			}
			if bits[0] != 0 || bits[480*100] != 0 {
				t.Fatal("white pixels must be 0")
			}
		}
	}
	if len(d) != int(le.Uint64(d[0x20:]))+2*(22+dataSize) {
		t.Fatalf("file size %d", len(d))
	}
}

func TestTrim(t *testing.T) {
	// A drawing in the middle of a wide white margin is cut out, with a thin border.
	g := solid(1000, 1500, 0xff)
	for y := 200; y < 1300; y++ {
		for x := 150; x < 850; x++ {
			g.SetGray(x, y, color.Gray{0x30})
		}
	}
	got := Trim(g)
	if r := got.Rect; r.Dx() < 700 || r.Dx() > 740 || r.Dy() < 1100 || r.Dy() > 1150 {
		t.Fatalf("trimmed to %v", r)
	}
	// Specks of dust don't count as content.
	g.SetGray(5, 5, color.Gray{0})
	if r := Trim(g).Rect; r.Dx() > 740 {
		t.Fatalf("a speck kept the margin: %v", r)
	}
	// A page that is mostly empty stays whole.
	small := solid(1000, 1500, 0xff)
	for y := 700; y < 800; y++ {
		for x := 450; x < 550; x++ {
			small.SetGray(x, y, color.Gray{0})
		}
	}
	if r := Trim(small).Rect; r.Dx() != 1000 || r.Dy() != 1500 {
		t.Fatalf("mostly empty page trimmed: %v", r)
	}
	// A blank page too.
	if r := Trim(solid(100, 100, 0xff)).Rect; r.Dx() != 100 {
		t.Fatalf("blank page trimmed: %v", r)
	}
}

func TestTone(t *testing.T) {
	// A washed-out scan (ink at 0x50, paper at 0xd0) gets black ink and white paper.
	g := solid(100, 100, 0xd0)
	for y := 0; y < 50; y++ {
		for x := 0; x < 100; x++ {
			g.SetGray(x, y, color.Gray{0x50})
		}
	}
	out := Tone(g)
	if ink, paper := out.GrayAt(10, 10).Y, out.GrayAt(10, 90).Y; ink > 0x20 || paper < 0xf0 {
		t.Fatalf("ink %#x paper %#x", ink, paper)
	}
	// Black and white stay as they are.
	bw := solid(10, 10, 0xff)
	bw.SetGray(0, 0, color.Gray{0})
	if o := Tone(bw); o.GrayAt(0, 0).Y != 0 || o.GrayAt(5, 5).Y != 0xff {
		t.Fatal("black or white moved")
	}
}

func TestCoverPage(t *testing.T) {
	art := solid(300, 450, 0x40)
	c := CoverPage(CoverInfo{Art: art, Title: "A rather long title that has to wrap onto more lines than fit", Chapter: "112.5", Subtitle: "The chapter's own name", Footer: "Vol. 12 · 22 pages"}, X4)
	if c.Rect.Dx() != 480 || c.Rect.Dy() != 800 {
		t.Fatalf("size %v", c.Rect)
	}
	// The art sits in the frame at the top, the margin around it is white.
	if c.GrayAt(240, 200).Y > 0x80 || c.GrayAt(5, 5).Y != 0xff {
		t.Fatalf("art %#x margin %#x", c.GrayAt(240, 200).Y, c.GrayAt(5, 5).Y)
	}
	// The black chapter badge sits on the left under the art.
	dark := 0
	for y := 540; y < 740; y++ {
		if c.GrayAt(40, y).Y < 0x20 {
			dark++
		}
	}
	if dark < 60 {
		t.Fatalf("no chapter badge (%d dark rows)", dark)
	}
	// Without art the frame is plain grey, and other screens scale.
	if p := CoverPage(CoverInfo{Title: "T", Chapter: "1"}, Screen{W: 528, H: 792}); p.Rect.Dx() != 528 || p.GrayAt(264, 200).Y == 0xff {
		t.Fatal("no-art cover")
	}
}
