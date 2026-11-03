package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Asion001/mangarr/internal/moodscore"
)

// moodScorer is this worker's model, loaded (and downloaded) the first
// time a moods task comes.
type moodScorer struct {
	mu     sync.Mutex
	scorer moodscore.Scorer
}

// scorer returns the model, loading it once.
func (w *Worker) scorer(ctx context.Context) (moodscore.Scorer, error) {
	w.moods.mu.Lock()
	defer w.moods.mu.Unlock()
	if w.moods.scorer != nil {
		return w.moods.scorer, nil
	}
	w.log.Info("loading the mood model (downloaded once, about 150 MB)", "dir", w.cfg.ModelDir)
	newScorer := w.cfg.NewScorer
	if newScorer == nil {
		newScorer = moodscore.New
	}
	sc, err := newScorer(ctx, moodscore.Config{ModelDir: w.cfg.ModelDir})
	if err != nil {
		return nil, err
	}
	w.moods.scorer = sc
	return sc, nil
}

// scoreMoods scores a chapter's pages against the moods the server sent.
func (w *Worker) scoreMoods(ctx context.Context, t Task) (result, error) {
	var prompts []moodscore.Prompt
	if raw, ok := t.Spec["prompts"]; ok {
		data, err := json.Marshal(raw)
		if err != nil {
			return result{}, err
		}
		if err := json.Unmarshal(data, &prompts); err != nil {
			return result{}, fmt.Errorf("the mood prompts make no sense: %w", err)
		}
	}
	if len(prompts) == 0 {
		return result{}, errors.New("the task has no mood prompts")
	}
	w.status.stage(t.ID, "fetching pages", -1, -1)
	path, _ := t.Spec["input"].(string)
	var pages [][]byte
	var err error
	var size int64
	if w.cfg.SharedStorage && path != "" {
		if pages, err = moodscore.ReadPages(path); err != nil {
			w.log.Info("shared storage: the chapter isn't here, fetching it over HTTP", "path", path)
			pages = nil
		}
	}
	if pages == nil {
		data, err := w.input(ctx, t.ID)
		if err != nil {
			return result{}, err
		}
		size = int64(len(data))
		tmp, err := os.CreateTemp("", "mangarr-moods-*.cbz")
		if err != nil {
			return result{}, err
		}
		defer os.Remove(tmp.Name())
		_, err = tmp.Write(data)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return result{}, err
		}
		if pages, err = moodscore.ReadPages(tmp.Name()); err != nil {
			return result{}, fmt.Errorf("the chapter: %w", err)
		}
	}
	ctx, abort := context.WithCancel(ctx)
	defer abort()
	beat := w.beating(ctx, t, len(pages), nil, abort)
	defer beat()
	w.status.stage(t.ID, "loading the mood model", -1, -1)
	sc, err := w.scorer(ctx)
	if err != nil {
		return result{}, err
	}
	w.status.stage(t.ID, "finding moods", 0, len(pages))
	scores, err := sc.Score(ctx, pages, prompts, func(done, total int) { w.status.stage(t.ID, "finding moods", done, total) })
	if err != nil {
		return result{}, err
	}
	return result{Pages: len(pages), BytesIn: size, Moods: scores}, nil
}

// defaultModelDir is where a worker keeps the mood model: its data folder
// when it has one, else the user's cache.
func defaultModelDir() string {
	if d := os.Getenv("MANGARR_DATA_DIR"); d != "" {
		return filepath.Join(d, "models", "moods")
	}
	if d, err := os.UserCacheDir(); err == nil {
		return filepath.Join(d, "mangarr", "models", "moods")
	}
	return filepath.Join(os.TempDir(), "mangarr-models", "moods")
}
