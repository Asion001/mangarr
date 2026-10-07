// Package imagecheck validates downloaded page images: it detects the format
// from magic bytes, rejects HTML/empty responses and reads dimensions.
package imagecheck

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

type Info struct {
	Format string // jpeg, png, gif, webp, avif, jxl, bmp
	Width  int
	Height int
}

var (
	ErrEmpty   = errors.New("empty image")
	ErrHTML    = errors.New("response is HTML, not an image")
	ErrUnknown = errors.New("unknown image format")
)

// Ext returns the file extension for a format.
func Ext(format string) string {
	switch format {
	case "jpeg":
		return ".jpg"
	case "":
		return ".bin"
	default:
		return "." + format
	}
}

// Detect identifies the format and dimensions of data.
func Detect(data []byte) (Info, error) {
	if len(data) < 12 {
		return Info{}, ErrEmpty
	}
	format := sniff(data)
	if format == "" {
		trimmed := bytes.TrimSpace(data[:min(len(data), 512)])
		if bytes.HasPrefix(trimmed, []byte("<")) || bytes.Contains(bytes.ToLower(trimmed), []byte("<html")) {
			return Info{}, ErrHTML
		}
		return Info{}, ErrUnknown
	}
	info := Info{Format: format}
	switch format {
	case "jpeg", "png", "gif", "webp":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return info, fmt.Errorf("corrupt %s: %w", format, err)
		}
		info.Width, info.Height = cfg.Width, cfg.Height
		if info.Width == 0 || info.Height == 0 {
			return info, fmt.Errorf("invalid %s dimensions", format)
		}
	case "avif":
		info.Width, info.Height = avifSize(data)
	case "jxl":
		info.Width, info.Height = jxlSize(data)
	case "bmp":
		if len(data) >= 26 {
			info.Width = int(int32(binary.LittleEndian.Uint32(data[18:22])))
			info.Height = abs(int(int32(binary.LittleEndian.Uint32(data[22:26]))))
		}
	}
	return info, nil
}

// Truncated reports whether data in format stops before the image ends: a
// download cut short. Decoders that read what is there show the top of
// such a page sharp and the rest blurred or gray, with a stepped edge
// between them, and upscaling makes it plain. Formats it can't judge pass.
func Truncated(format string, data []byte) bool {
	switch format {
	case "jpeg":
		// the image ends with EOI after its last scan; an EOI before that
		// belongs to an embedded thumbnail
		sos := bytes.LastIndex(data, []byte{0xFF, 0xDA})
		return sos >= 0 && bytes.LastIndex(data, []byte{0xFF, 0xD9}) < sos
	case "png":
		return !bytes.Contains(data[max(0, len(data)-64):], []byte("IEND"))
	case "webp":
		return len(data) >= 8 && int64(len(data)) < int64(binary.LittleEndian.Uint32(data[4:8]))+8
	}
	return false
}

func sniff(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "jpeg"
	case bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "png"
	case bytes.HasPrefix(b, []byte("GIF87a")), bytes.HasPrefix(b, []byte("GIF89a")):
		return "gif"
	case bytes.HasPrefix(b, []byte("RIFF")) && string(b[8:12]) == "WEBP":
		return "webp"
	case string(b[4:8]) == "ftyp" && (string(b[8:12]) == "avif" || string(b[8:12]) == "avis"):
		return "avif"
	case bytes.HasPrefix(b, []byte{0xFF, 0x0A}), bytes.HasPrefix(b, []byte{0, 0, 0, 0x0C, 'J', 'X', 'L', ' '}):
		return "jxl"
	case bytes.HasPrefix(b, []byte("BM")):
		return "bmp"
	}
	return ""
}

// avifSize finds the first 'ispe' box (image spatial extents).
func avifSize(b []byte) (int, int) {
	i := bytes.Index(b, []byte("ispe"))
	if i < 0 || i+16 > len(b) {
		return 0, 0
	}
	// ispe: size(4) 'ispe'(4) version/flags(4) width(4) height(4)
	w := binary.BigEndian.Uint32(b[i+8 : i+12])
	h := binary.BigEndian.Uint32(b[i+12 : i+16])
	return int(w), int(h)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
