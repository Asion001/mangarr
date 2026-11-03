package app_test

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/moodscore"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/soundtrack"
	"github.com/Asion001/mangarr/internal/worker"
)

// colourScorer stands in for the model: a red page is calm, a blue one
// action (it reads the page's first pixel).
type colourScorer struct{ calm, action int64 }

func (c colourScorer) Score(_ context.Context, pages [][]byte, _ []moodscore.Prompt, progress func(int, int)) ([]moodscore.Scores, error) {
	out := make([]moodscore.Scores, len(pages))
	for i, p := range pages {
		img, err := png.Decode(bytes.NewReader(p))
		if err != nil {
			out[i] = moodscore.Scores{}
			continue
		}
		r, _, b, _ := img.At(0, 0).RGBA()
		if r > b {
			out[i] = moodscore.Scores{c.calm: 0.9, c.action: 0.1}
		} else {
			out[i] = moodscore.Scores{c.calm: 0.1, c.action: 0.9}
		}
		if progress != nil {
			progress(i+1, len(pages))
		}
	}
	return out, nil
}

func (colourScorer) Close() {}

func colourPage(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 24))
	for y := range 24 {
		for x := range 16 {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// TestMoodsFromPages: with finding moods in pages on, a series' chapters
// are scored (here, or by a worker with the moods role over the protocol),
// the scores are kept by file hash, and a mood that takes over for a few
// pages becomes an automatic page cue, which a cue someone placed beats.
func TestMoodsFromPages(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			moods, err := soundtrack.Moods(e.Ctx, e.App.DB, e.App.Settings)
			if err != nil {
				t.Fatal(err)
			}
			calm, action := moods[0], moods[3]
			fake := colourScorer{calm.ID, action.ID}
			now := time.Now().UTC()

			addSeries := func(title string, shade uint8) *model.Series {
				red, blue := colourPage(color.RGBA{220, shade, 30, 255}), colourPage(color.RGBA{30, shade, 220, 255})
				ser := &model.Series{Title: title, SortTitle: title, Status: model.StatusOngoing, RootFolderID: e.RFID, Path: title, ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				dir, err := e.App.Library.SeriesDir(e.Ctx, ser)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				// chapter 1 calm throughout; chapter 2 turns to action at page 4
				for n, colours := range map[int][][]byte{1: {red, red, red, red, red, red}, 2: {red, red, red, blue, blue, blue, blue, blue, blue}} {
					pages := make([]cbz.Page, len(colours))
					for i, c := range colours {
						pages[i] = cbz.Page{Name: cbz.PageName(i, ".png"), Data: c}
					}
					rel := title + " Ch." + strconv.Itoa(n) + ".cbz"
					res, err := cbz.Write(filepath.Join(dir, rel), pages, nil, 0, now)
					if err != nil {
						t.Fatal(err)
					}
					ch := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(n), NumberSort: float64(n), State: model.ChapterImported, FirstSeenAt: now, UpdatedAt: now}
					if _, err := e.App.DB.NewInsert().Model(ch).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: ser.ID, RelativePath: rel, Size: res.Size, SHA256: res.SHA256, PageCount: len(colours), ImportedAt: now}
					if _, err := e.App.DB.NewInsert().Model(f).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					if _, err := e.App.DB.NewUpdate().Model(ch).Set("file_id = ?", f.ID).WherePK().Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
				}
				return ser
			}
			user := &model.User{Username: "reader", CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(user).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			check := func(t *testing.T, seriesID int64) {
				t.Helper()
				r, err := soundtrack.Resolve(e.Ctx, e.App.DB, e.App.Settings, seriesID, user.ID, nil, 2)
				if err != nil {
					t.Fatal(err)
				}
				if r.Start == nil || r.Start.Mood.ID != calm.ID || len(r.Pages) != 1 || r.Pages[0].Page != 4 ||
					r.Pages[0].Sound.Mood.ID != action.ID || r.Pages[0].Sound.Source != soundtrack.FromAuto || r.Pages[0].Sound.Confidence == nil {
					t.Fatalf("chapter 2: %+v", r)
				}
			}

			// off: nothing is scored
			local := addSeries("Here", 30)
			if _, err := e.App.PageMoods.Score(e.Ctx, local.ID, nil, nil); err == nil {
				t.Fatal("scored while off")
			}
			cfg, _ := e.App.Settings.Sound(e.Ctx)
			cfg.PageMoods = true
			if err := e.App.Settings.Set(e.Ctx, settings.KeySound, cfg); err != nil {
				t.Fatal(err)
			}

			// this server scores them itself (no worker is online)
			calls := 0
			e.App.PageMoods.NewScorer = func(context.Context, moodscore.Config) (moodscore.Scorer, error) {
				calls++
				return fake, nil
			}
			e.runCommand(t, "ScorePages", map[string]any{"seriesId": local.ID})
			check(t, local.ID)
			var kept int64
			if kept, err = e.App.DB.NewSelect().Model((*model.PageMoods)(nil)).Count(e.Ctx); err != nil || kept != 2 {
				t.Fatalf("kept scores %d %v", kept, err)
			}
			// scored files aren't scored again
			e.runCommand(t, "ScorePages", map[string]any{"seriesId": local.ID})
			if calls != 1 {
				t.Fatalf("the model was made %d times", calls)
			}
			// the review shows chapter 2's pages and where action takes over
			rev, err := e.App.PageMoods.Review(e.Ctx, local.ID, ptr(2.0))
			if err != nil || len(rev.Chapters) != 2 || !rev.Chapters[1].Scored || rev.Chapter == nil || rev.Chapter.Number != 2 ||
				len(rev.Chapter.Scores) != 9 || rev.Chapter.CarryIn != calm.ID || len(rev.Chapter.Changes) != 1 || rev.Chapter.Changes[0].Page != 4 {
				t.Fatalf("review %v %+v %+v", err, rev, rev.Chapter)
			}
			// "not here": the cue goes, and placing the moods again leaves it out
			if err := e.App.PageMoods.Dismiss(e.Ctx, local.ID, 2, 4); err != nil {
				t.Fatal(err)
			}
			if err := e.App.PageMoods.Place(e.Ctx, local.ID); err != nil {
				t.Fatal(err)
			}
			if r, _ := soundtrack.Resolve(e.Ctx, e.App.DB, e.App.Settings, local.ID, user.ID, nil, 2); len(r.Pages) != 0 {
				t.Fatalf("a dismissed cue came back: %+v", r.Pages)
			}
			if rev, _ := e.App.PageMoods.Review(e.Ctx, local.ID, ptr(2.0)); len(rev.Chapter.Dismissed) != 1 || rev.Chapter.Dismissed[0] != 4 {
				t.Fatalf("dismissed %+v", rev.Chapter.Dismissed)
			}
			// a cue someone placed at that page wins
			if _, err := soundtrack.SetCue(e.Ctx, e.App.DB, local.ID, nil, soundtrack.Cue{ChapterNumber: 2, Page: ptr(4), MoodID: &calm.ID}); err != nil {
				t.Fatal(err)
			}
			if err := e.App.PageMoods.Place(e.Ctx, local.ID); err != nil {
				t.Fatal(err)
			}
			if r, _ := soundtrack.Resolve(e.Ctx, e.App.DB, e.App.Settings, local.ID, user.ID, nil, 2); len(r.Pages) != 1 || r.Pages[0].Sound.Mood.ID != calm.ID || r.Pages[0].Sound.Source == soundtrack.FromAuto {
				t.Fatalf("a placed cue over a found one: %+v", r.Pages)
			}

			// with a worker that can do it online, the worker scores them
			srv := httptest.NewServer(api.New(e.App))
			defer srv.Close()
			key, _, err := e.App.Auth.CreateWorker(e.Ctx, "mood-worker", []string{model.RoleMoods}, 0)
			if err != nil {
				t.Fatal(err)
			}
			ctx, stop := context.WithCancel(e.Ctx)
			defer stop()
			wk, err := worker.New(worker.Config{ServerURL: srv.URL, Key: key, Roles: []string{model.RoleMoods}, Version: "test",
				ModelDir: t.TempDir(), NewScorer: func(context.Context, moodscore.Config) (moodscore.Scorer, error) { return fake, nil },
				Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Status: worker.NewStatus()})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				_ = wk.Run(ctx)
			}()
			waitFor(t, 10*time.Second, "the worker to say hello", func() bool {
				ok, _ := e.App.Tasks.CanDo(e.Ctx, model.TaskMoods)
				return ok
			})
			// other files: scores are kept by file hash, so the same pages
			// wouldn't be scored twice
			remote := addSeries("Remote", 60)
			e.runCommand(t, "ScorePages", map[string]any{"seriesId": remote.ID})
			waitFor(t, 30*time.Second, "the worker to score the chapters", func() bool {
				n, _ := e.App.DB.NewSelect().Model((*model.WorkerTask)(nil)).Where("kind = ? AND state = ?", model.TaskMoods, model.TaskDone).Count(e.Ctx)
				return n == 2
			})
			check(t, remote.ID)
			stop()
			<-done
		})
	}
}

func ptr[T any](v T) *T { return &v }
