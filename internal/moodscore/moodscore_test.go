package moodscore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// a tiny CLIP-style tokenizer: enough vocabulary and merges to spell a few words
const tinyTokenizer = `{"model":{"vocab":{"<|startoftext|>":0,"<|endoftext|>":1,"a</w>":2,"c":3,"a":4,"t</w>":5,"ca":6,"cat</w>":7,"!</w>":8,"r":9,"n":10},
"merges":["c a","ca t</w>"]}}`

func TestTokenizer(t *testing.T) {
	tk, err := LoadTokenizer(strings.NewReader(tinyTokenizer))
	if err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string][]int64{
		"a cat":     {0, 2, 7, 1},
		"  A  CAT!": {0, 2, 7, 8, 1},
		"ran":       {0, 9, 4, 1, 1}, // "n</w>" isn't in the vocabulary: the unknown token
	} {
		if got := tk.Encode(text); !equal(got, want) {
			t.Errorf("Encode(%q) = %v, want %v", text, got, want)
		}
	}
	long := strings.Repeat("a ", 200)
	if got := tk.Encode(long); len(got) != maxTokens || got[len(got)-1] != 1 {
		t.Errorf("a long text: %d tokens", len(got))
	}
	if _, err := LoadTokenizer(strings.NewReader(`{"model":{"vocab":{},"merges":[]}}`)); err == nil {
		t.Error("loaded a tokenizer without CLIP's special tokens")
	}
	// newer files keep merges as pairs
	pairs := strings.Replace(tinyTokenizer, `["c a","ca t</w>"]`, `[["c","a"],["ca","t</w>"]]`, 1)
	if tk, err := LoadTokenizer(strings.NewReader(pairs)); err != nil || !equal(tk.Encode("cat"), []int64{0, 7, 1}) {
		t.Errorf("pair merges: %v", err)
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func pagePNG(w, h int, c color.Color) []byte {
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

func TestCrops(t *testing.T) {
	for _, c := range []struct{ w, h, crops int }{{800, 800, 1}, {800, 1200, 2}, {700, 3000, 3}, {2000, 900, 2}} {
		got, err := Crops(pagePNG(c.w, c.h, color.White))
		if err != nil || len(got) != c.crops || len(got[0]) != 3*side*side {
			t.Errorf("%dx%d: %d crops, %v", c.w, c.h, len(got), err)
		}
	}
	white, _ := Crops(pagePNG(300, 300, color.White))
	if v := white[0][0]; math.Abs(float64(v-(1-mean[0])/std[0])) > 1e-4 {
		t.Errorf("white normalises to %v", v)
	}
	if _, err := Crops([]byte("not an image")); err == nil {
		t.Error("cropped a broken page")
	}
}

func TestProbabilities(t *testing.T) {
	prompts := []Prompt{{1, "a"}, {1, "b"}, {2, "c"}}
	p := probabilities([]float32{0.30, 0.29, 0.20}, prompts)
	if math.Abs(p[1]+p[2]-1) > 1e-9 || p[1] < 0.99 {
		t.Errorf("probabilities %v", p)
	}
	if got := Moods(prompts); len(got) != 2 || got[0] != 1 {
		t.Errorf("moods %v", got)
	}
}

func TestEnsureModel(t *testing.T) {
	body := []byte("model bytes")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	t.Cleanup(srv.Close)
	saved := ModelFiles
	t.Cleanup(func() { ModelFiles = saved })
	ModelFiles = []File{{"m.onnx", srv.URL + "/m", hex.EncodeToString(sum[:]), int64(len(body))}}
	dir := t.TempDir()
	if ModelReady(dir) {
		t.Fatal("ready before downloading")
	}
	if err := EnsureModel(context.Background(), dir, nil); err != nil || !ModelReady(dir) {
		t.Fatalf("download: %v", err)
	}
	ModelFiles = []File{{"bad.onnx", srv.URL + "/m", strings.Repeat("0", 64), int64(len(body))}}
	if err := EnsureModel(context.Background(), dir, nil); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("a file that doesn't match its hash: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.onnx")); err == nil {
		t.Fatal("kept a file that didn't match")
	}
}

func TestUnavailableWithoutRuntime(t *testing.T) {
	t.Setenv("MANGARR_ONNXRUNTIME", "")
	if Built {
		t.Skip("this build has ONNX Runtime support")
	}
	if ok, why := Available(); ok || why == "" {
		t.Fatal("a build without ONNX says it can score pages")
	}
	if _, err := New(context.Background(), Config{ModelDir: t.TempDir()}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("New: %v", err)
	}
}
