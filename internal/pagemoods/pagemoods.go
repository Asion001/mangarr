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
	"sort"
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
	// placing makes one placement at a time: two workers finishing chapters
	// together would otherwise each write the cues they saw, and the one
	// that saw fewer scored chapters could win
	placing sync.Mutex
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
		q = q.Where("c.id IN (?)", bun.List(ids))
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
	if err := s.DB.NewSelect().Model(&rows).Where("file_key IN (?) AND model_version = ? AND prompts_key = ?", bun.List(keys), moodscore.Version, promptsKey).Scan(ctx); err != nil {
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
	n, err := s.DB.NewSelect().Model((*model.WorkerTask)(nil)).Where("kind = ? AND state IN (?)", model.TaskMoods, bun.List([]string{model.TaskPending, model.TaskLeased})).
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

// planned is a chapter's page scores (nil: not scored yet), the mood it
// starts with from the chapter before, and where a mood takes over.
type planned struct {
	Chapter
	Scores  []moodscore.Scores
	CarryIn int64
	Changes []moodscore.Change
}

// plan finds where moods take over in a series' scored chapters: a chapter
// starts from the mood the one before ended on.
func (s *Service) plan(ctx context.Context, seriesID int64) ([]planned, error) {
	_, key, err := s.Prompts(ctx)
	if err != nil {
		return nil, err
	}
	chapters, err := s.chapters(ctx, seriesID, nil)
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
	out := make([]planned, len(chapters))
	var carry int64
	for i, c := range chapters {
		out[i] = planned{Chapter: c, CarryIn: carry}
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
		out[i].Scores = pages
		out[i].Changes = moodscore.Changes(pages, carry)
		for _, ch := range out[i].Changes {
			carry = ch.Mood
		}
	}
	return out, nil
}

// dismissed are the places admins said no mood found in the pages starts.
func (s *Service) dismissed(ctx context.Context, seriesID int64) (map[[2]float64]bool, error) {
	var rows []model.SoundtrackDismissal
	if err := s.DB.NewSelect().Model(&rows).Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return nil, err
	}
	out := make(map[[2]float64]bool, len(rows))
	for _, r := range rows {
		out[[2]float64{r.ChapterNumber, float64(r.Page)}] = true
	}
	return out, nil
}

// cuePage is where a change's cue sits: a page, or 0 for the chapter's start.
func cuePage(ch moodscore.Change) int {
	if ch.Page > 1 {
		return ch.Page
	}
	return 0
}

// Place turns a series' page scores into automatic cues: a chapter starts
// from the mood the one before ended on, a mood that leads for a few pages
// takes over there (except where an admin said not here).
func (s *Service) Place(ctx context.Context, seriesID int64) error {
	s.placing.Lock()
	defer s.placing.Unlock()
	plan, err := s.plan(ctx, seriesID)
	if err != nil {
		return err
	}
	skip, err := s.dismissed(ctx, seriesID)
	if err != nil {
		return err
	}
	var cues []soundtrack.Cue
	for _, c := range plan {
		count := len(c.Scores)
		for _, ch := range c.Changes {
			page := cuePage(ch)
			if skip[[2]float64{c.Number, float64(page)}] {
				continue
			}
			mood, conf := ch.Mood, ch.Confidence
			cue := soundtrack.Cue{ChapterNumber: c.Number, MoodID: &mood, Confidence: &conf}
			if page > 0 {
				cue.Page, cue.PageCount = &page, &count
			}
			cues = append(cues, cue)
		}
	}
	return soundtrack.ReplaceAutoCues(ctx, s.DB, seriesID, moodscore.Version, cues)
}

// ReviewChapter is a downloaded chapter in the review.
type ReviewChapter struct {
	ID     int64   `json:"id"`
	Number float64 `json:"number"`
	Pages  int     `json:"pages"`
	Scored bool    `json:"scored"`
}

// ReviewDetail is one chapter's page scores and where moods take over.
type ReviewDetail struct {
	ReviewChapter
	// CarryIn is the mood the chapter starts with, from the one before (0:
	// none).
	CarryIn int64 `json:"carryIn"`
	// Scores are each page's mood probabilities (by mood id), page 1 first.
	Scores []map[string]float64 `json:"scores"`
	// Changes are where a mood takes over, before dismissals.
	Changes []moodscore.Change `json:"changes"`
	// Dismissed are the pages (0: the chapter's start) an admin said no
	// found mood starts at.
	Dismissed []int `json:"dismissed"`
}

// Review is a series' chapters for reviewing the moods found in pages,
// with one of them in detail.
type Review struct {
	Chapters []ReviewChapter `json:"chapters"`
	Chapter  *ReviewDetail   `json:"chapter,omitempty"`
}

// Review lists a series' chapters and details one (by number; the first
// scored one when nil).
func (s *Service) Review(ctx context.Context, seriesID int64, number *float64) (*Review, error) {
	plan, err := s.plan(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	skip, err := s.dismissed(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	out := &Review{Chapters: make([]ReviewChapter, len(plan))}
	pick := -1
	for i, c := range plan {
		out.Chapters[i] = ReviewChapter{ID: c.ID, Number: c.Number, Pages: c.File.PageCount, Scored: c.Scores != nil}
		if number != nil && c.Number == *number || number == nil && pick < 0 && c.Scores != nil {
			pick = i
		}
	}
	if pick < 0 {
		return out, nil
	}
	c := plan[pick]
	d := &ReviewDetail{ReviewChapter: out.Chapters[pick], CarryIn: c.CarryIn, Scores: make([]map[string]float64, len(c.Scores)), Changes: c.Changes, Dismissed: []int{}}
	if d.Changes == nil {
		d.Changes = []moodscore.Change{}
	}
	for j, p := range c.Scores {
		d.Scores[j] = make(map[string]float64, len(p))
		for id, v := range p {
			d.Scores[j][strconv.FormatInt(id, 10)] = v
		}
	}
	for k := range skip {
		if k[0] == c.Number {
			d.Dismissed = append(d.Dismissed, int(k[1]))
		}
	}
	sort.Ints(d.Dismissed)
	out.Chapter = d
	return out, nil
}

// Dismiss says no mood found in the pages starts at a place (page 0: the
// chapter's start), and places the found moods again without it.
func (s *Service) Dismiss(ctx context.Context, seriesID int64, chapter float64, page int) error {
	if page < 0 {
		page = 0
	}
	if page == 1 {
		page = 0 // the first page is the chapter's start
	}
	row := &model.SoundtrackDismissal{SeriesID: seriesID, ChapterNumber: chapter, Page: page, CreatedAt: time.Now().UTC()}
	if _, err := s.DB.NewInsert().Model(row).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
		return err
	}
	return s.Place(ctx, seriesID)
}
