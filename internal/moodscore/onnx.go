//go:build onnx

package moodscore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	ort "github.com/shota3506/onnxruntime-purego/onnxruntime"
)

// Built reports whether this build can run the model at all.
const Built = true

// apiVersion is the ONNX Runtime C API the library must offer (1.23).
const apiVersion = 23

// Available reports whether pages can be scored here, and why not.
func Available() (bool, string) {
	if FindLibrary() == "" {
		return false, "ONNX Runtime isn't installed next to mangarr (" + libraryName() + ")"
	}
	return true, ""
}

type onnxScorer struct {
	rt     *ort.Runtime
	env    *ort.Env
	vision *ort.Session
	text   *ort.Session
	tok    *Tokenizer

	mu      sync.Mutex
	prompts string
	textEmb [][]float32
}

// New loads ONNX Runtime and the model, downloading the model first if
// it's missing.
func New(ctx context.Context, cfg Config) (Scorer, error) {
	lib := cfg.Library
	if lib == "" {
		lib = FindLibrary()
	}
	if lib == "" {
		_, why := Available()
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, why)
	}
	if err := EnsureModel(ctx, cfg.ModelDir, cfg.HTTP); err != nil {
		return nil, err
	}
	rt, err := ort.NewRuntime(lib, apiVersion)
	if err != nil {
		return nil, fmt.Errorf("%w: loading ONNX Runtime: %v", ErrUnavailable, err)
	}
	s := &onnxScorer{rt: rt}
	fail := func(err error) (Scorer, error) {
		s.Close()
		return nil, err
	}
	if s.env, err = rt.NewEnv("mangarr", ort.LoggingLevelWarning); err != nil {
		return fail(err)
	}
	opts := &ort.SessionOptions{IntraOpNumThreads: cfg.threads()}
	if s.vision, err = rt.NewSession(s.env, filepath.Join(cfg.ModelDir, "vision.onnx"), opts); err != nil {
		return fail(fmt.Errorf("loading the image model: %w", err))
	}
	if s.text, err = rt.NewSession(s.env, filepath.Join(cfg.ModelDir, "text.onnx"), opts); err != nil {
		return fail(fmt.Errorf("loading the text model: %w", err))
	}
	f, err := os.Open(filepath.Join(cfg.ModelDir, "tokenizer.json"))
	if err != nil {
		return fail(err)
	}
	defer f.Close()
	if s.tok, err = LoadTokenizer(f); err != nil {
		return fail(err)
	}
	return s, nil
}

func (s *onnxScorer) Close() {
	if s.text != nil {
		s.text.Close()
	}
	if s.vision != nil {
		s.vision.Close()
	}
	if s.env != nil {
		s.env.Close()
	}
	if s.rt != nil {
		_ = s.rt.Close()
	}
}

// output finds a session's first output whose name has want in it.
func output(sess *ort.Session, want string) string {
	for _, n := range sess.OutputNames() {
		if strings.Contains(n, want) {
			return n
		}
	}
	return sess.OutputNames()[0]
}

// textEmbeddings encodes the prompts, once for each set of them.
func (s *onnxScorer) textEmbeddings(ctx context.Context, prompts []Prompt) ([][]float32, error) {
	var key strings.Builder
	for _, p := range prompts {
		fmt.Fprintf(&key, "%d\x00%s\x00", p.MoodID, p.Text)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if key.String() == s.prompts && s.textEmb != nil {
		return s.textEmb, nil
	}
	out := make([][]float32, len(prompts))
	for i, p := range prompts {
		ids := s.tok.Encode(p.Text)
		in, err := ort.NewTensorValue(s.rt, ids, []int64{1, int64(len(ids))})
		if err != nil {
			return nil, err
		}
		inputs := map[string]*ort.Value{"input_ids": in}
		var mask *ort.Value
		if slices.Contains(s.text.InputNames(), "attention_mask") {
			ones := make([]int64, len(ids))
			for j := range ones {
				ones[j] = 1
			}
			if mask, err = ort.NewTensorValue(s.rt, ones, []int64{1, int64(len(ids))}); err != nil {
				in.Close()
				return nil, err
			}
			inputs["attention_mask"] = mask
		}
		name := output(s.text, "text_embeds")
		res, err := s.text.Run(ctx, inputs, ort.WithOutputNames(name))
		in.Close()
		if mask != nil {
			mask.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("encoding the mood prompts: %w", err)
		}
		data, _, err := ort.GetTensorData[float32](res[name])
		res[name].Close()
		if err != nil {
			return nil, err
		}
		out[i] = normalize(slices.Clone(data))
	}
	s.prompts, s.textEmb = key.String(), out
	return out, nil
}

// imageEmbedding encodes a page's crops and averages them.
func (s *onnxScorer) imageEmbedding(ctx context.Context, page []byte) ([]float32, error) {
	crops, err := Crops(page)
	if err != nil {
		return nil, err
	}
	pixels := make([]float32, 0, len(crops)*3*side*side)
	for _, c := range crops {
		pixels = append(pixels, c...)
	}
	in, err := ort.NewTensorValue(s.rt, pixels, []int64{int64(len(crops)), 3, side, side})
	if err != nil {
		return nil, err
	}
	defer in.Close()
	name := output(s.vision, "image_embeds")
	res, err := s.vision.Run(ctx, map[string]*ort.Value{s.vision.InputNames()[0]: in}, ort.WithOutputNames(name))
	if err != nil {
		return nil, err
	}
	data, shape, err := ort.GetTensorData[float32](res[name])
	res[name].Close()
	if err != nil {
		return nil, err
	}
	if len(shape) != 2 || int(shape[0]) != len(crops) {
		return nil, errors.New("the image model answered in an unexpected shape")
	}
	dim := int(shape[1])
	vs := make([][]float32, len(crops))
	for i := range crops {
		vs[i] = normalize(slices.Clone(data[i*dim : (i+1)*dim]))
	}
	return average(vs), nil
}

func (s *onnxScorer) Score(ctx context.Context, pages [][]byte, prompts []Prompt, progress func(done, total int)) ([]Scores, error) {
	if len(prompts) == 0 {
		return nil, errors.New("no mood prompts to score against")
	}
	text, err := s.textEmbeddings(ctx, prompts)
	if err != nil {
		return nil, err
	}
	out := make([]Scores, len(pages))
	for i, page := range pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		emb, err := s.imageEmbedding(ctx, page)
		if err != nil {
			// an unreadable page scores nothing; the rest still count
			out[i] = Scores{}
		} else {
			sims := make([]float32, len(text))
			for j, t := range text {
				sims[j] = dot(emb, t)
			}
			out[i] = probabilities(sims, prompts)
		}
		if progress != nil {
			progress(i+1, len(pages))
		}
	}
	return out, nil
}
