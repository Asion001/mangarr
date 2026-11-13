package eink

import (
	"image"
	"image/color"
	"strings"
	"sync"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// CoverInfo is what a chapter's cover page shows.
type CoverInfo struct {
	// Art is the title's cover (nil: a plain frame).
	Art image.Image
	// Title is the title's name.
	Title string
	// Chapter is the chapter's number ("45", "12.5").
	Chapter string
	// Subtitle is the chapter's own title, when it has one.
	Subtitle string
	// Footer is the small print on the left of the bottom line ("Vol. 6 · 22 pages").
	Footer string
}

var (
	fontsOnce     sync.Once
	boldFont      *opentype.Font
	regularFont   *opentype.Font
	errFontsParse error
)

func fonts() (*opentype.Font, *opentype.Font, error) {
	fontsOnce.Do(func() {
		if boldFont, errFontsParse = opentype.Parse(gobold.TTF); errFontsParse != nil {
			return
		}
		regularFont, errFontsParse = opentype.Parse(goregular.TTF)
	})
	return boldFont, regularFont, errFontsParse
}

func face(f *opentype.Font, size float64) font.Face {
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil
	}
	return fc
}

// CoverPage draws a chapter's cover page: the title's art in a frame, a black
// chapter badge beside the title, and a footer line. CrossPoint cuts each
// book's Home tile from its first page, so this page is what the reader sees
// on the Home screen.
func CoverPage(c CoverInfo, s Screen) *image.Gray {
	page := blank(s)
	bold, regular, err := fonts()
	if err != nil {
		return page
	}
	u := float64(s.W) / 480 // the design is drawn at 480 wide
	px := func(v float64) int { return max(1, int(v*u+0.5)) }
	pad := px(28)

	// Footer: a rule with the small print under it, at the bottom.
	small := face(regular, 15*u)
	smallBold := face(bold, 15*u)
	footY := s.H - pad - px(18)
	fill(page, image.Rect(pad, footY-px(12)-px(2), s.W-pad, footY-px(12)), 0)
	if small != nil && smallBold != nil {
		text(page, small, pad, footY+px(12), clip(small, c.Footer, s.W-2*pad-px(90)), 0)
		mark := "mangarr"
		text(page, smallBold, s.W-pad-font.MeasureString(smallBold, mark).Ceil(), footY+px(12), mark, 0)
	}

	// Badge and title, above the footer.
	numFace := face(bold, 48*u)
	capFace := face(bold, 12*u)
	titleFace := face(bold, 30*u)
	subFace := face(regular, 16*u)
	if numFace == nil || capFace == nil || titleFace == nil || subFace == nil {
		return page
	}
	badgeW := max(px(76), font.MeasureString(numFace, c.Chapter).Ceil()+px(24))
	badgeH := px(84)
	textX := pad + badgeW + px(18)
	textW := s.W - pad - textX
	titleLines := wrap(titleFace, c.Title, textW, 3)
	subLines := wrap(subFace, c.Subtitle, textW, 2)
	titleLH, subLH := px(33), px(21)
	blockH := max(badgeH, len(titleLines)*titleLH+len(subLines)*subLH+px(6))
	blockTop := footY - px(12) - px(2) - px(20) - blockH

	fill(page, image.Rect(pad, blockTop, pad+badgeW, blockTop+badgeH), 0)
	ch := "CH"
	text(page, capFace, pad+(badgeW-font.MeasureString(capFace, ch).Ceil())/2, blockTop+px(22), ch, 0xff)
	text(page, numFace, pad+(badgeW-font.MeasureString(numFace, c.Chapter).Ceil())/2, blockTop+px(72), c.Chapter, 0xff)
	y := blockTop + px(26)
	for _, l := range titleLines {
		text(page, titleFace, textX, y, l, 0)
		y += titleLH
	}
	y += px(4)
	for _, l := range subLines {
		text(page, subFace, textX, y, l, 0x55)
		y += subLH
	}

	// The art fills the frame above.
	frame := image.Rect(pad, pad, s.W-pad, blockTop-px(20))
	if frame.Dy() > px(40) {
		border := px(3)
		fill(page, frame, 0)
		inner := frame.Inset(border)
		if c.Art != nil && c.Art.Bounds().Dx() > 0 && c.Art.Bounds().Dy() > 0 {
			art := flatten(c.Art)
			draw.CatmullRom.Scale(page, inner, art, coverCrop(art.Bounds(), inner), draw.Src, nil)
		} else {
			fill(page, inner, 0xaa)
		}
	}
	return Tone(page)
}

// coverCrop is the part of src that fills dst without distortion, cut from
// the middle across and from the top down (faces sit high on covers).
func coverCrop(src, dst image.Rectangle) image.Rectangle {
	sw, sh := src.Dx(), src.Dy()
	if sw*dst.Dy() > sh*dst.Dx() { // wider than the frame: trim the sides
		w := sh * dst.Dx() / dst.Dy()
		x := src.Min.X + (sw-w)/2
		return image.Rect(x, src.Min.Y, x+w, src.Max.Y)
	}
	h := sw * dst.Dy() / dst.Dx()
	return image.Rect(src.Min.X, src.Min.Y, src.Max.X, src.Min.Y+h)
}

func fill(g *image.Gray, r image.Rectangle, v uint8) {
	draw.Draw(g, r.Intersect(g.Rect), image.NewUniform(color.Gray{v}), image.Point{}, draw.Src)
}

func text(g *image.Gray, f font.Face, x, y int, s string, v uint8) {
	d := font.Drawer{Dst: g, Src: image.NewUniform(color.Gray{v}), Face: f, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

// wrap breaks s into at most n lines of width w, ending the last with "…"
// when it doesn't fit.
func wrap(f font.Face, s string, w, n int) []string {
	words := strings.Fields(s)
	var lines []string
	cur := ""
	for i, word := range words {
		try := strings.TrimSpace(cur + " " + word)
		if font.MeasureString(f, try).Ceil() <= w || cur == "" {
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = word
		if len(lines) == n-1 {
			cur = strings.Join(words[i:], " ")
			break
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	for i, l := range lines {
		lines[i] = clip(f, l, w)
	}
	return lines
}

// clip shortens s with "…" to fit width w.
func clip(f font.Face, s string, w int) string {
	if font.MeasureString(f, s).Ceil() <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && font.MeasureString(f, string(r)+"…").Ceil() > w {
		r = r[:len(r)-1]
	}
	return strings.TrimSpace(string(r)) + "…"
}
