// Package moodscore scores manga pages against moods with a zero-shot
// image model: each mood has a few text prompts ("a manga page of a tense
// standoff"), and a page scores highest on the mood whose prompts it looks
// most like. The model runs in ONNX Runtime, which builds with the onnx
// tag load at run time (the full image and the worker zips ship the
// library); other builds report that page scoring is unavailable.
//
// The model files aren't shipped: they are downloaded, pinned and checked,
// the first time a scorer is made.
package moodscore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"
)

// ErrUnavailable: this build or machine can't score pages.
var ErrUnavailable = errors.New("page scoring unavailable")

// Version names the model and how its scores are made, kept with every
// score so a better model can tell its own from older ones.
const Version = "clip-b32-q8-1"

// Prompt is one text a mood is described with.
type Prompt struct {
	MoodID int64  `json:"moodId"`
	Text   string `json:"text"`
}

// Scores are a page's probabilities by mood id (they sum to 1).
type Scores map[int64]float64

// Scorer scores pages (encoded images) against moods.
type Scorer interface {
	// Score returns one Scores per page, in order. progress, if set, is
	// called after each page.
	Score(ctx context.Context, pages [][]byte, prompts []Prompt, progress func(done, total int)) ([]Scores, error)
	Close()
}

// Config says where the runtime library and the model are.
type Config struct {
	// Library is ONNX Runtime's shared library ("" finds it).
	Library string
	// ModelDir holds the downloaded model files.
	ModelDir string
	// Threads the model may use (0: half the cores).
	Threads int
	// HTTP downloads the model (nil: a default client).
	HTTP *http.Client
}

func (c Config) threads() int {
	if c.Threads > 0 {
		return c.Threads
	}
	return max(1, runtime.NumCPU()/2)
}

// File is one model file, pinned by its hash.
type File struct {
	Name   string
	URL    string
	SHA256 string
	Size   int64
}

const modelBase = "https://huggingface.co/Xenova/clip-vit-base-patch32/resolve/6b7c523703ae26e39be4b03774966f8571c7f523/"

// ModelFiles are the image and text encoders (8-bit) and the tokenizer of
// CLIP ViT-B/32 (MIT licence), about 150 MB.
var ModelFiles = []File{
	{"vision.onnx", modelBase + "onnx/vision_model_quantized.onnx", "583fd1110a514667812fee7d684952aaf82a99b959760c8d7dca7e0ab9839299", 89117001},
	{"text.onnx", modelBase + "onnx/text_model_quantized.onnx", "73baab855d406190da9faa498cfedf65f15cf309f4cc7385b7b032e6d08e5c3a", 64504507},
	{"tokenizer.json", modelBase + "tokenizer.json", "f7f3b7af117d467b58374797691a6438d3e6b9e9cef800dfd5dced7f697a90cd", 2224119},
}

// ModelReady reports whether the model files are all in dir.
func ModelReady(dir string) bool {
	for _, f := range ModelFiles {
		if st, err := os.Stat(filepath.Join(dir, f.Name)); err != nil || st.Size() != f.Size {
			return false
		}
	}
	return true
}

// EnsureModel downloads the model files dir lacks, checking each one's
// hash before keeping it.
func EnsureModel(ctx context.Context, dir string, client *http.Client) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	for _, f := range ModelFiles {
		dst := filepath.Join(dir, f.Name)
		if st, err := os.Stat(dst); err == nil && st.Size() == f.Size {
			continue
		}
		if err := download(ctx, client, f, dst); err != nil {
			return fmt.Errorf("downloading the mood model (%s): %w", f.Name, err)
		}
	}
	return nil
}

func download(ctx context.Context, client *http.Client, f File, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, f.Size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != f.SHA256 {
		return fmt.Errorf("checksum mismatch (got %s)", got)
	}
	return os.Rename(tmp.Name(), dst)
}

// libraryName is ONNX Runtime's library file on this system.
func libraryName() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		return "libonnxruntime.dylib"
	}
	return "libonnxruntime.so"
}

// FindLibrary looks for ONNX Runtime: MANGARR_ONNXRUNTIME, an onnxruntime
// folder next to the program (the worker zips), then the usual library
// folders (the full image). "" when it isn't there.
func FindLibrary() string {
	if p := os.Getenv("MANGARR_ONNXRUNTIME"); p != "" {
		return p
	}
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "onnxruntime"))
	}
	dirs = append(dirs, "/usr/local/lib", "/usr/lib", "/opt/homebrew/lib", "/usr/lib/x86_64-linux-gnu", "/usr/lib/aarch64-linux-gnu")
	for _, d := range dirs {
		p := filepath.Join(d, libraryName())
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// probabilities turns a page's similarity to each prompt into mood
// probabilities: a softmax over the prompts (scaled as CLIP is trained),
// summed by mood.
func probabilities(sims []float32, prompts []Prompt) Scores {
	const scale = 100
	maxv := float64(math.Inf(-1))
	for _, s := range sims {
		maxv = math.Max(maxv, float64(s)*scale)
	}
	exp := make([]float64, len(sims))
	sum := 0.0
	for i, s := range sims {
		exp[i] = math.Exp(float64(s)*scale - maxv)
		sum += exp[i]
	}
	out := Scores{}
	for i, p := range prompts {
		out[p.MoodID] += exp[i] / sum
	}
	return out
}

// Moods lists the mood ids of prompts, in order.
func Moods(prompts []Prompt) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, p := range prompts {
		if !seen[p.MoodID] {
			seen[p.MoodID] = true
			out = append(out, p.MoodID)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
