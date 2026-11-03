// Package pagemoods finds soundtrack moods in a chapter's pages: it has
// them scored (on this server when it can run the model, else on a worker
// with the moods role), keeps the scores by the chapter file's hash, and
// turns them into automatic cues where the mood changes.
package pagemoods

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/moodscore"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/soundtrack"
	"github.com/Asion001/mangarr/internal/worktasks"
)

// Service scores pages and places the cues.
type Service struct {
	DB       bun.IDB
	Settings *settings.Store
	Tasks    *worktasks.Ledger
	// ModelDir is where this server keeps the model (<data>/models/moods).
	ModelDir string
	Log      *slog.Logger
	// NewScorer makes this server's own scorer (tests replace it).
	NewScorer func(ctx context.Context, cfg moodscore.Config) (moodscore.Scorer, error)
	// Paths finds a chapter's CBZ (the server wires the library's).
	Paths func(ctx context.Context, chapterID int64) (string, error)

	mu     sync.Mutex
	scorer moodscore.Scorer
	busy   sync.Mutex
}

// New returns the service for a data folder.
func New(db bun.IDB, st *settings.Store, tasks *worktasks.Ledger, dataDir string, log *slog.Logger) *Service {
	return &Service{DB: db, Settings: st, Tasks: tasks, ModelDir: filepath.Join(dataDir, "models", "moods"), Log: log, NewScorer: moodscore.New}
}

// Prompts are the moods' prompts (a mood without any is described by its
// name), and a key that changes when they do.
func (s *Service) Prompts(ctx context.Context) ([]moodscore.Prompt, string, error) {
	moods, err := soundtrack.Moods(ctx, s.DB, s.Settings)
	if err != nil {
		return nil, "", err
	}
	var out []moodscore.Prompt
	h := sha256.New()
	for _, m := range moods {
		texts := m.Prompts
		if len(texts) == 0 && m.PackID != nil {
			continue // a place a pack brought ("Café"), not a mood to find
		}
		if len(texts) == 0 {
			texts = []string{"a manga page that feels " + strings.ToLower(m.Name)}
		}
		for _, t := range texts {
			out = append(out, moodscore.Prompt{MoodID: m.ID, Text: t})
			fmt.Fprintf(h, "%d\x00%s\x00", m.ID, t)
		}
	}
	return out, hex.EncodeToString(h.Sum(nil))[:16], nil
}

// FileKey names a chapter file's pages: its hash, else its id and size.
func FileKey(f *model.ChapterFile) string {
	if f.SHA256 != "" {
		return f.SHA256
	}
	return "file-" + strconv.FormatInt(f.ID, 10) + "-" + strconv.FormatInt(f.Size, 10)
}

// Chapter is a downloaded chapter whose pages can be scored.
type Chapter struct {
	ID       int64
	SeriesID int64
	Number   float64
	File     model.ChapterFile
}

// chapters lists a series' downloaded chapters by number (only ids when
// given).
func (s *Service) chapters(ctx context.Context, seriesID int64, ids []int64) ([]Chapter, error) {
	var rows []struct {
		model.ChapterFile `bun:",extend"`
		Number            float64 `bun:"number_sort"`
	}
	q := s.DB.NewSelect().Model(&rows).ColumnExpr("chapter_file.*, c.number_sort").
		Join("JOIN chapters AS c ON c.file_id = chapter_file.id").
		Where("c.series_id = ?", seriesID).OrderExpr("c.number_sort, c.id")
	if len(ids) > 0 {
		q = q.Where("c.id IN (?)", bun.In(ids))
	}
	if err := q.Scan(ctx); err != nil {
		return nil, err
	}
	out := make([]Chapter, len(rows))
	for i, r := range rows {
		out[i] = Chapter{ID: r.ChapterID, SeriesID: seriesID, Number: r.Number, File: r.ChapterFile}
	}
	return out, nil
}

// scored reports which file keys have scores made with these prompts.
func (s *Service) scored(ctx context.Context, keys []string, promptsKey string) (map[string]model.PageMoods, error) {
	out := map[string]model.PageMoods{}
	if len(keys) == 0 {
		return out, nil
	}
	var rows []model.PageMoods
	if err := s.DB.NewSelect().Model(&rows).Where("file_key IN (?) AND model_version = ? AND prompts_key = ?", bun.In(keys), moodscore.Version, promptsKey).Scan(ctx); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.FileKey] = r
	}
	return out, nil
}

// Status is where page scoring can happen.
type Status struct {
	Enabled bool `json:"enabled"`
	// Local: this server can run the model; LocalReason says why not.
	Local       bool   `json:"local"`
	LocalReason string `json:"localReason,omitempty"`
	// ModelReady: the model is downloaded here.
	ModelReady bool `json:"modelReady"`
	// Workers that can score pages are online.
	Workers bool `json:"workers"`
}

// Status says whether and where pages can be scored.
func (s *Service) Status(ctx context.Context) Status {
	cfg, _ := s.Settings.Sound(ctx)
	ok, why := moodscore.Available()
	st := Status{Enabled: cfg.PageMoods, Local: ok, LocalReason: why, ModelReady: moodscore.ModelReady(s.ModelDir)}
	if s.Tasks != nil {
		st.Workers, _ = s.Tasks.CanDo(ctx, model.TaskMoods)
	}
	return st
}

// local is this server's scorer, made (and the model downloaded) once.
func (s *Service) local(ctx context.Context) (moodscore.Scorer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scorer != nil {
		return s.scorer, nil
	}
	sc, err := s.NewScorer(ctx, moodscore.Config{ModelDir: s.ModelDir})
	if err != nil {
		return nil, err
	}
	s.scorer = sc
	return sc, nil
}

// Close lets the model go.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scorer != nil {
		s.scorer.Close()
		s.scorer = nil
	}
}

// Result says what a run did.
type Result struct {
	Scored int `json:"scored" doc:"Chapters scored on this server"`
	Queued int `json:"queued" doc:"Chapters handed to workers"`
}

// ErrOff: finding moods in pages is turned off.
var ErrOff = errors.New("finding moods in pages is off (Settings › Soundtracks)")

// Score has a series' chapters (all, or those given) scored that aren't
// yet: on workers with the moods role when one is online, else here. Then
// it places the cues from what is scored.
func (s *Service) Score(ctx context.Context, seriesID int64, chapterIDs []int64, progress func(string)) (*Result, error) {
	cfg, err := s.Settings.Sound(ctx)
	if err != nil {
		return nil, err
	}
	if !cfg.PageMoods {
		return nil, ErrOff
	}
	prompts, key, err := s.Prompts(ctx)
	if err != nil {
		return nil, err
	}
	chapters, err := s.chapters(ctx, seriesID, chapterIDs)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(chapters))
	for i, c := range chapters {
		keys[i] = FileKey(&c.File)
	}
	have, err := s.scored(ctx, keys, key)
	if err != nil {
		return nil, err
	}
	var need []Chapter
	for i, c := range chapters {
		if _, ok := have[keys[i]]; !ok && c.File.PageCount != 0 {
			need = append(need, c)
		}
	}
	res := &Result{}
	if len(need) > 0 {
		workers := false
		if s.Tasks != nil {
			workers, _ = s.Tasks.CanDo(ctx, model.TaskMoods)
		}
		if workers {
			for _, c := range need {
				queued, err := s.enqueue(ctx, c, prompts, key)
				if err != nil {
					return res, err
				}
				if queued {
					res.Queued++
				}
			}
		} else {
			sc, err := s.local(ctx)
			if err != nil {
				return res, err
			}
			// one chapter at a time on this server, whoever asked
			s.busy.Lock()
			defer s.busy.Unlock()
			for i, c := range need {
				if progress != nil {
					progress(fmt.Sprintf("chapter %d of %d", i+1, len(need)))
				}
				if err := s.scoreHere(ctx, sc, c, prompts, key); err != nil {
					s.Log.Warn("scoring a chapter's pages", "chapter", c.ID, "err", err)
					continue
				}
				res.Scored++
			}
		}
	}
	return res, s.Place(ctx, seriesID)
}

// pathOf is a chapter's CBZ.
func (s *Service) pathOf(ctx context.Context, c Chapter) (string, error) {
	if s.Paths == nil {
		return "", errors.New("the chapter's file can't be found")
	}
	return s.Paths(ctx, c.ID)
}

func (s *Service) scoreHere(ctx context.Context, sc moodscore.Scorer, c Chapter, prompts []moodscore.Prompt, key string) error {
	path, err := s.pathOf(ctx, c)
	if err != nil {
		return err
	}
	pages, err := moodscore.ReadPages(path)
	if err != nil {
		return err
	}
	scores, err := sc.Score(ctx, pages, prompts, nil)
	if err != nil {
		return err
	}
	return s.Store(ctx, FileKey(&c.File), key, scores)
}

// enqueue hands a chapter to the workers, unless one already has it.
func (s *Service) enqueue(ctx context.Context, c Chapter, prompts []moodscore.Prompt, key string) (bool, error) {
	path, err := s.pathOf(ctx, c)
	if err != nil {
		return false, err
	}
	n, err := s.DB.NewSelect().Model((*model.WorkerTask)(nil)).Where("kind = ? AND state IN (?)", model.TaskMoods, bun.In([]string{model.TaskPending, model.TaskLeased})).
		Where("CAST(spec AS TEXT) LIKE ?", "%"+FileKey(&c.File)+"%").Count(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	spec := map[string]any{"chapterId": c.ID, "seriesId": c.SeriesID, "fileKey": FileKey(&c.File), "promptsKey": key, "prompts": prompts, "input": path}
	return true, s.Tasks.Add(ctx, &model.WorkerTask{Kind: model.TaskMoods, Spec: spec, PagesTotal: c.File.PageCount})
}

// Store keeps a file's scores.
func (s *Service) Store(ctx context.Context, fileKey, promptsKey string, scores []moodscore.Scores) error {
	rows := make([]map[string]float64, len(scores))
	for i, sc := range scores {
		rows[i] = map[string]float64{}
		for m, v := range sc {
			rows[i][strconv.FormatInt(m, 10)] = v
		}
	}
	pm := model.PageMoods{FileKey: fileKey, ModelVersion: moodscore.Version, PromptsKey: promptsKey, Pages: len(scores), Scores: rows, ScannedAt: time.Now().UTC()}
	_, err := s.DB.NewInsert().Model(&pm).On("CONFLICT (file_key, model_version) DO UPDATE").
		Set("prompts_key = EXCLUDED.prompts_key, pages = EXCLUDED.pages, scores = EXCLUDED.scores, scanned_at = EXCLUDED.scanned_at").Exec(ctx)
	return err
}

// Completed takes a worker's scores for a task.
func (s *Service) Completed(ctx context.Context, t model.WorkerTask, scores []moodscore.Scores) error {
	fileKey, _ := t.Spec["fileKey"].(string)
	promptsKey, _ := t.Spec["promptsKey"].(string)
	if fileKey == "" {
		return errors.New("the task names no file")
	}
	if err := s.Store(ctx, fileKey, promptsKey, scores); err != nil {
		return err
	}
	seriesID := int64(0)
	switch v := t.Spec["seriesId"].(type) {
	case float64:
		seriesID = int64(v)
	case int64:
		seriesID = v
	}
	if seriesID == 0 {
		return nil
	}
	return s.Place(ctx, seriesID)
}

// Place turns a series' page scores into automatic cues: a chapter starts
// from the mood the one before ended on, a mood that leads for a few pages
// takes over there.
func (s *Service) Place(ctx context.Context, seriesID int64) error {
	_, key, err := s.Prompts(ctx)
	if err != nil {
		return err
	}
	chapters, err := s.chapters(ctx, seriesID, nil)
	if err != nil {
		return err
	}
	keys := make([]string, len(chapters))
	for i, c := range chapters {
		keys[i] = FileKey(&c.File)
	}
	have, err := s.scored(ctx, keys, key)
	if err != nil {
		return err
	}
	var cues []soundtrack.Cue
	var carry int64
	for i, c := range chapters {
		pm, ok := have[keys[i]]
		if !ok {
			continue
		}
		pages := make([]moodscore.Scores, len(pm.Scores))
		for j, row := range pm.Scores {
			pages[j] = moodscore.Scores{}
			for m, v := range row {
				if id, err := strconv.ParseInt(m, 10, 64); err == nil {
					pages[j][id] = v
				}
			}
		}
		for _, ch := range moodscore.Changes(pages, carry) {
			mood, conf, count := ch.Mood, ch.Confidence, len(pages)
			cue := soundtrack.Cue{ChapterNumber: c.Number, MoodID: &mood, Confidence: &conf}
			if ch.Page > 1 {
				page := ch.Page
				cue.Page, cue.PageCount = &page, &count
			}
			cues = append(cues, cue)
			carry = ch.Mood
		}
	}
	return soundtrack.ReplaceAutoCues(ctx, s.DB, seriesID, moodscore.Version, cues)
}
