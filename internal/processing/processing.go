// Package processing is the download pipeline's processing stage: it
// upscales pages narrower than the profile's minimum width (through an
// upscale module), splits tall strips and then re-encodes pages to save space.
// It runs either before import or later in the background (ProcessBacklog).
package processing

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/imagecheck"
	"github.com/Asion001/mangarr/internal/imageenc"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/progress"
	"github.com/Asion001/mangarr/internal/upscaling"
)

type Processor struct {
	Up  *upscaling.Processor
	Enc *imageenc.Encoder
	// Guard (optional) pauses encoding when a library server can't read it.
	Guard *Guard
}

func New(up *upscaling.Processor, enc *imageenc.Encoder) *Processor {
	return &Processor{Up: up, Enc: enc}
}

// Unavailable wraps errors that mean a processing engine isn't reachable or
// installed right now (retry later, keep the original meanwhile).
type Unavailable struct{ Err error }

func (u Unavailable) Error() string { return u.Err.Error() }
func (u Unavailable) Unwrap() error { return u.Err }

// Temporary tells the download manager to retry later.
func (u Unavailable) Temporary() bool { return true }

// Process runs the stages enabled in cfg: shrink pages wider than the profile
// allows, upscale the narrow ones, split tall strips, then re-encode. Junk
// images (under the profile's junk size) pass through untouched.
func (p *Processor) Process(ctx context.Context, cfg model.ProfileConfig, pages []downloads.PageFile, workDir string) (downloads.ProcessResult, error) {
	res := downloads.ProcessResult{Pages: pages, SourcePages: make([]int, len(pages))}
	for i := range res.SourcePages {
		res.SourcePages[i] = i
	}
	encoding := cfg.Encode.Format != "" && cfg.Encode.Format != "keep"
	if encoding && p.Guard != nil {
		if blocked, reason := p.Guard.Blocked(); blocked {
			return res, Unavailable{fmt.Errorf("re-encoding is paused: %s", reason)}
		}
	}
	cur := append([]downloads.PageFile(nil), pages...)
	processable := make([]bool, len(pages))
	var real []int // indexes of the pages that aren't junk
	for i, junk := range junkMask(pages, cfg.Pages) {
		if !junk {
			processable[i] = true
			real = append(real, i)
		}
	}
	maxWidth := cfg.Pages.MaxWidth
	for _, i := range real {
		if NeedsShrink(cur[i], maxWidth) {
			out, err := shrink(cur[i], maxWidth, encoding, workDir)
			if err != nil {
				return res, err
			}
			cur[i] = out
			res.Shrunk++
			res.Changed = true
		}
	}
	upscaling := cfg.Upscale.Enabled && p.Up != nil
	// src is the input page each entry of cur came from
	src := make([]int, len(cur))
	for i := range src {
		src[i] = i
	}
	if upscaling {
		// strips are cut before they are upscaled: an upscaled webtoon strip
		// can be taller than the engine can write (it leaves an empty file)
		parts, n, err := preSplit(ctx, cfg, cur, processable, encoding, workDir)
		if err != nil {
			return res, err
		}
		if n > 0 {
			var ncur []downloads.PageFile
			var nproc []bool
			var nsrc []int
			for i, pp := range parts {
				for _, pg := range pp {
					ncur, nproc, nsrc = append(ncur, pg), append(nproc, processable[i]), append(nsrc, i)
				}
			}
			cur, processable, src = ncur, nproc, nsrc
			real = real[:0]
			for i, ok := range processable {
				if ok {
					real = append(real, i)
				}
			}
		}
		res.Split = n
	}
	var enc *imageenc.Stream
	if encoding {
		if p.Enc == nil {
			return res, Unavailable{imageenc.ErrNoEngine}
		}
		s, err := p.Enc.Stream(cfg.Encode, workDir)
		if err != nil {
			if errors.Is(err, imageenc.ErrNoEngine) {
				return res, Unavailable{err}
			}
			return res, fmt.Errorf("encoding: %w", err)
		}
		enc = s
	}
	// Splitting and encoding run page by page as the upscaler finishes each
	// one, so the CPU works on a chapter's first pages while the GPU is on
	// the next ones; one after the other, the GPU sat idle for most of a
	// chapter.
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	t := &tails{cfg: cfg, toPNG: encoding, workDir: workDir, enc: enc, cancel: cancel,
		parts: make([][]downloads.PageFile, len(cur)), splits: make(chan struct{}, splitsAtOnce())}
	t.split = res.Split
	tctx := progress.With(ctx, nil) // the stages overlap: progress is reported here
	for i := range cur {
		if !processable[i] {
			t.parts[i] = []downloads.PageFile{cur[i]}
		} else if !upscaling {
			t.add(tctx, i, cur[i])
		}
	}
	if upscaling {
		ucfg := cfg.Upscale
		if maxWidth > 0 {
			ucfg.MaxWidth = maxWidth
		}
		if encoding {
			ucfg.Format = "png" // lossless hand-off to the encoder
		}
		sel := make([]downloads.PageFile, len(real))
		for k, i := range real {
			sel[k] = cur[i]
		}
		started := time.Now()
		_, applied, mdl, err := p.Up.ProcessEach(ctx, ucfg, sel, workDir, func(k int, pg downloads.PageFile) {
			t.add(tctx, real[k], pg)
		})
		if err != nil {
			cancel(err)
			t.wg.Wait()
			return res, Unavailable{fmt.Errorf("upscaling: %w", err)}
		}
		res.UpscaleSeconds = time.Since(started).Seconds()
		if applied {
			res.Upscaled, res.UpscaleModel, res.Changed = true, mdl, true
		}
	}
	t.upscaled(ctx)
	t.wg.Wait()
	if err := context.Cause(ctx); err != nil {
		return res, err
	}
	cur = make([]downloads.PageFile, 0, len(pages))
	sources := make([]int, 0, len(pages))
	for u, parts := range t.parts {
		for _, pg := range parts {
			cur = append(cur, pg)
			sources = append(sources, src[u])
		}
	}
	if t.split > 0 {
		for i := range cur {
			cur[i].Name = cbz.PageName(i, imagecheck.Ext(cur[i].Format))
		}
		res.Split, res.Changed = t.split, true
	}
	if enc != nil {
		res.EncodeSeconds = t.encodeSeconds()
		if st := enc.Stats(); st.Encoded > 0 {
			res.Encoded, res.Encoder, res.Changed = st.Encoded, st.Engine, true
		}
	}
	res.Pages = cur
	res.SourcePages = sources
	res.ProcessedPages = changedPageCount(pages, res.Pages)
	return res, nil
}

// tails splits and encodes a chapter's pages, each as soon as it is ready.
type tails struct {
	cfg     model.ProfileConfig
	toPNG   bool
	workDir string
	enc     *imageenc.Stream
	cancel  context.CancelCauseFunc
	// splits bounds the strips decoded at once: a whole upscaled strip is
	// held in memory while it is cut.
	splits chan struct{}
	wg     sync.WaitGroup

	mu    sync.Mutex
	parts [][]downloads.PageFile // what each input page became
	split int
	done  int
	total int
	// after upscaling, progress is the pages finished here
	reporting bool
	report    context.Context
	encFirst  time.Time
	encLast   time.Time
}

// splitsAtOnce is how many strips are cut at a time.
func splitsAtOnce() int { return max(min(runtime.NumCPU()/4, 2), 1) }

// add starts on page i, final from the upscaler's point of view.
func (t *tails) add(ctx context.Context, i int, pg downloads.PageFile) {
	t.mu.Lock()
	t.total++
	t.mu.Unlock()
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		parts, err := t.run(ctx, pg)
		if err != nil {
			t.cancel(err)
			return
		}
		t.mu.Lock()
		t.parts[i] = parts
		t.done++
		t.progress()
		t.mu.Unlock()
	}()
}

// upscaled switches progress over to this stage once the GPU is done.
func (t *tails) upscaled(ctx context.Context) {
	if t.enc == nil && !t.cfg.Pages.SplitTall {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reporting, t.report = true, ctx
	t.progress()
}

// progress reports the pages finished so far (t.mu held).
func (t *tails) progress() {
	if !t.reporting {
		return
	}
	stage := progress.StageEncode
	if t.enc == nil {
		stage = progress.StageSplit
	}
	ev := progress.Event{Stage: stage, Done: t.done, Total: t.total}
	if t.enc != nil {
		st := t.enc.Stats()
		ev.BytesIn, ev.BytesOut = st.Before, st.After
	}
	progress.Report(t.report, ev)
}

// run splits one page when it is a strip and encodes what comes out.
func (t *tails) run(ctx context.Context, pg downloads.PageFile) ([]downloads.PageFile, error) {
	parts := []downloads.PageFile{pg}
	if t.cfg.Pages.SplitTall {
		threshold, segment := t.cfg.Pages.SplitRatios()
		if threshold > 0 && isStrip(pg, threshold) && splitSupported(pg.Format) {
			select {
			case t.splits <- struct{}{}:
			case <-ctx.Done():
				return nil, context.Cause(ctx)
			}
			var err error
			parts, err = splitTallPage(ctx, pg, max(1, int(float64(pg.Width)*segment)), t.toPNG, t.workDir)
			<-t.splits
			if err != nil {
				return nil, err
			}
			if len(parts) > 1 {
				t.mu.Lock()
				t.split++
				t.mu.Unlock()
			}
		}
	}
	if t.enc == nil {
		return parts, nil
	}
	out := make([]downloads.PageFile, len(parts))
	errs := make([]error, len(parts))
	var wg sync.WaitGroup
	for k, part := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			started := time.Now()
			np, err := t.enc.Encode(ctx, imageenc.Page{Name: part.Name, Path: part.Path, Format: part.Format, Width: part.Width, Height: part.Height})
			t.encoded(started)
			if err != nil {
				errs[k] = fmt.Errorf("encoding: %s: %w", part.Name, err)
				return
			}
			out[k] = downloads.PageFile{Name: np.Name, Path: np.Path, Format: np.Format, Width: np.Width, Height: np.Height}
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}

// encoded notes an encode that started at started and ended now.
func (t *tails) encoded(started time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.encFirst.IsZero() || started.Before(t.encFirst) {
		t.encFirst = started
	}
	t.encLast = time.Now()
}

// encodeSeconds is the time from the first encode to the last one.
func (t *tails) encodeSeconds() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.encFirst.IsZero() {
		return 0
	}
	return t.encLast.Sub(t.encFirst).Seconds()
}

func changedPageCount(before, after []downloads.PageFile) int {
	n := min(len(before), len(after))
	changed := max(len(before), len(after)) - n
	for i := 0; i < n; i++ {
		if before[i] != after[i] {
			changed++
		}
	}
	return changed
}

var _ downloads.Processor = (*Processor)(nil)
