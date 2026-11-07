package thumbs

import (
	"bytes"
	"net/http"
)

// SafeType names the raster image format of data from its bytes ("" when
// it isn't one). Whatever a remote server claims, only these are served
// back: HTML or SVG passed off as a cover would run script on mangarr's
// own origin when opened.
func SafeType(data []byte) string {
	switch {
	case len(data) >= 12 && string(data[4:8]) == "ftyp" && (string(data[8:12]) == "avif" || string(data[8:12]) == "avis"):
		return "image/avif"
	case bytes.HasPrefix(data, []byte{0xFF, 0x0A}), bytes.HasPrefix(data, []byte("\x00\x00\x00\x0cJXL \r\n\x87\n")):
		return "image/jxl"
	}
	switch ct := http.DetectContentType(data); ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/x-icon", "image/vnd.microsoft.icon", "image/avif":
		return ct
	}
	return ""
}
