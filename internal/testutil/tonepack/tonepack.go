// Package tonepack makes a small sound pack of generated tones for tests:
// no recorded or third-party audio, just sine waves written as WAV.
package tonepack

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

const rate = 8000

// WAV returns a mono 16-bit sine tone.
func WAV(freq float64, ms int) []byte {
	n := rate * ms / 1000
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w(uint32(36 + n*2))
	b.WriteString("WAVEfmt ")
	w(uint32(16))
	w(uint16(1))
	w(uint16(1))
	w(uint32(rate))
	w(uint32(rate * 2))
	w(uint16(2))
	w(uint16(16))
	b.WriteString("data")
	w(uint32(n * 2))
	for i := range n {
		w(int16(8000 * math.Sin(2*math.Pi*freq*float64(i)/rate)))
	}
	return b.Bytes()
}

// Manifest is the test pack's pack.json: two ambience loops, two effects
// and one ready-made scene.
func Manifest(id, version string) map[string]any {
	return map[string]any{
		"id": id, "version": version, "name": "Test tones", "license": "CC0-1.0",
		"tracks": []map[string]any{
			{"file": "loops/low-hum.wav", "kind": "ambience", "category": "weather", "title": "Low hum", "license": "CC0-1.0", "attribution": "Generated tone", "gain_db": -3},
			{"file": "loops/high-hum.wav", "kind": "ambience", "title": "High hum", "tags": []string{"wind"}},
			{"file": "fx/beep.wav", "kind": "sfx", "category": "objects", "icon": "bell", "title": "Beep"},
			{"file": "fx/boop.wav", "kind": "sfx", "category": "combat", "title": "Boop"},
		},
		"scenes": []map[string]any{{
			"name": "Humming room", "mood": "calm",
			"ambience": []map[string]any{{"file": "loops/low-hum.wav", "volume": 0.6}},
			"sfx":      []map[string]any{{"file": "fx/beep.wav", "label": "Beep"}},
		}},
	}
}

// Files are the test pack's sounds by their path in the zip.
func Files() map[string][]byte {
	return map[string][]byte{
		"loops/low-hum.wav":  WAV(110, 1000),
		"loops/high-hum.wav": WAV(440, 500),
		"fx/beep.wav":        WAV(880, 120),
		"fx/boop.wav":        WAV(330, 120),
	}
}

// Zip writes the test pack as a zip (in a folder named after the pack, as
// packs are often zipped) and returns its path.
func Zip(t testing.TB, id, version string) string {
	t.Helper()
	var b bytes.Buffer
	zw := zip.NewWriter(&b)
	add := func(name string, data []byte) {
		f, err := zw.Create(id + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f.Write(data)
	}
	m, _ := json.Marshal(Manifest(id, version))
	add("pack.json", m)
	for name, data := range Files() {
		add(name, data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), id+".zip")
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Folder writes loose sound files the way a downloaded pack might lie
// on disk, for "import folder".
func Folder(t testing.TB) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "My Sounds")
	for name, data := range map[string][]byte{
		"Ambience/Heavy Rain Loop.wav": WAV(100, 400),
		"Ambience/City Street.wav":     WAV(150, 400),
		"SFX/Door Slam.wav":            WAV(600, 100),
		"SFX/sword_clash_01.wav":       WAV(900, 100),
		"Music/Battle Theme.wav":       WAV(300, 400),
		"notes.txt":                    []byte("not a sound"),
	} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
