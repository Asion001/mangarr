package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// maxPageBytes is the largest page a worker will accept from a site.
const maxPageBytes = 64 << 20

// prefetchBytes caps what the fetch-ahead cache holds, whatever the page
// count says: fifty pages of a colour webtoon is not the same as fifty
// pages of a black-and-white chapter.
const prefetchBytes = 192 << 20

// pageRetries per page before the whole chapter is given up on.
const pageRetries = 3

// fetched is one page, waiting to be uploaded.
type fetched struct {
	spec PageSpec
	data []byte
}

// download fetches a chapter's pages and uploads them to the server. Pages
// are fetched ahead of the uploads (the prefetch cache), so a slow site and
// a slow upload don't wait for each other.
func (w *Worker) download(ctx context.Context, t Task) (result, error) {
	pages, err := pageSpecs(t)
	if err != nil {
		return result{}, err
	}
	prefetch := w.cfg.Prefetch
	if prefetch <= 0 {
		prefetch = w.welcome.Prefetch
	}
	if prefetch <= 0 {
		prefetch = 50
	}
	conc := w.pageLimit()
	// the share of the catalog's budget the server handed over: it paces the
	// site for everyone, and a worker keeps to its part of that
	pace := rateFrom(t.Spec)
	if pace.maxConcurrent > 0 {
		conc = min(conc, pace.maxConcurrent)
	}
	w.status.stage(t.ID, "downloading", 0, len(pages))
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		bytesIn  atomic.Int64
		bytesOut atomic.Int64
		done     atomic.Int64
		budget   = newBudget(prefetchBytes)
		queue    = make(chan fetched, prefetch)
		errOnce  sync.Once
		failure  error
	)
	fail := func(err error) {
		errOnce.Do(func() {
			failure = err
			cancel()
		})
	}

	// the heartbeat keeps the lease and carries progress; it also brings back
	// the server's answer when a job is cancelled
	stopBeat := make(chan struct{})
	var beat sync.WaitGroup
	beat.Add(1)
	go func() {
		defer beat.Done()
		every := time.Duration(max(w.welcome.LeaseSeconds/3, 10)) * time.Second
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			select {
			case <-stopBeat:
				return
			case <-tick.C:
				var out struct {
					Cancel bool `json:"cancel"`
				}
				err := w.call(ctx, http.MethodPost, fmt.Sprintf("/api/v1/worker/tasks/%d/heartbeat", t.ID), map[string]any{
					"pagesDone": int(done.Load()), "pagesTotal": len(pages),
					"bytesIn": bytesIn.Load(), "bytesOut": bytesOut.Load(),
				}, &out)
				switch {
				case err != nil && gone(err):
					fail(errors.New("the server gave this task to someone else"))
				case err != nil:
					w.log.Warn("heartbeat didn't reach the server", "task", t.ID, "err", err)
				case out.Cancel:
					fail(errors.New("cancelled"))
				}
			}
		}
	}()

	// fetchers fill the cache, in order
	var fetchers sync.WaitGroup
	specs := make(chan PageSpec)
	for range conc {
		fetchers.Add(1)
		go func() {
			defer fetchers.Done()
			for spec := range specs {
				if err := pace.wait(ctx); err != nil {
					return
				}
				data, err := w.fetchPage(ctx, spec)
				if err != nil {
					fail(fmt.Errorf("page %d: %w", spec.Index+1, err))
					return
				}
				bytesIn.Add(int64(len(data)))
				if !budget.take(ctx, len(data)) {
					return
				}
				select {
				case queue <- fetched{spec: spec, data: data}:
				case <-ctx.Done():
					budget.give(len(data))
					return
				}
			}
		}()
	}
	go func() {
		defer close(specs)
		for _, p := range pages {
			select {
			case specs <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		fetchers.Wait()
		close(queue)
	}()

	// one uploader: the server writes pages into the chapter's folder, and
	// the order they arrive in doesn't matter
	for page := range queue {
		err := w.upload(ctx, t.ID, page.spec.Index+1, page.data)
		budget.give(len(page.data))
		if err != nil {
			if gone(err) {
				fail(errors.New("the server gave this task to someone else"))
			} else {
				fail(fmt.Errorf("uploading page %d: %w", page.spec.Index+1, err))
			}
			break
		}
		bytesOut.Add(int64(len(page.data)))
		w.status.stage(t.ID, "", int(done.Add(1)), -1)
	}
	cancel()
	fetchers.Wait()
	close(stopBeat)
	beat.Wait()
	for range queue { //nolint:revive // drain what the fetchers had in hand
	}

	res := result{Pages: int(done.Load()), BytesIn: bytesIn.Load(), BytesOut: bytesOut.Load()}
	if failure != nil {
		return res, failure
	}
	if res.Pages != len(pages) {
		return res, fmt.Errorf("uploaded %d of %d pages", res.Pages, len(pages))
	}
	return res, nil
}

// pageSpecs reads the page list out of a task's spec.
func pageSpecs(t Task) ([]PageSpec, error) {
	raw, ok := t.Spec["pages"]
	if !ok {
		return nil, errors.New("this download task carries no pages")
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var out []PageSpec
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("the page list makes no sense: %w", err)
	}
	if len(out) == 0 {
		return nil, errors.New("this download task carries no pages")
	}
	return out, nil
}

// fetchPage gets one page from the site, with the headers the server said
// it needs, and a few tries before giving up.
func (w *Worker) fetchPage(ctx context.Context, p PageSpec) ([]byte, error) {
	var last error
	for attempt := range pageRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<(2*attempt)) * time.Second): // 4s, 16s
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.URL, nil)
		if err != nil {
			return nil, err
		}
		for k, v := range p.Headers {
			req.Header.Set(k, v)
		}
		resp, err := w.cfg.Fetch.Do(req)
		if err != nil {
			last = err
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			last = fmt.Errorf("HTTP error %d for %s", resp.StatusCode, p.URL)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
		resp.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if len(data) == 0 {
			last = errors.New("the site sent an empty page")
			continue
		}
		return data, nil
	}
	return nil, last
}

// budget bounds what the prefetch cache holds, in bytes.
type budget struct {
	mu    sync.Mutex
	cond  *sync.Cond
	limit int
	used  int
}

func newBudget(limit int) *budget {
	b := &budget{limit: limit}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// take waits until n bytes fit. It reports false when the context ended.
func (b *budget) take(ctx context.Context, n int) bool {
	if n > b.limit {
		n = b.limit // one enormous page still has to get through
	}
	stop := context.AfterFunc(ctx, func() { b.cond.Broadcast() })
	defer stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+n > b.limit {
		if ctx.Err() != nil {
			return false
		}
		b.cond.Wait()
	}
	b.used += n
	return true
}

func (b *budget) give(n int) {
	if n > b.limit {
		n = b.limit
	}
	b.mu.Lock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.mu.Unlock()
	b.cond.Broadcast()
}

// rate is what a worker may ask a site for, as the server worked it out.
type rate struct {
	perMinute     int
	maxConcurrent int
	minDelay      time.Duration
	jitter        time.Duration

	mu   sync.Mutex
	next time.Time
}

// rateFrom reads the budget out of a task's spec (an empty one means "as
// fast as you like", which is what a site with no limits gets).
func rateFrom(spec map[string]any) *rate {
	r := &rate{}
	raw, ok := spec["rate"].(map[string]any)
	if !ok {
		return r
	}
	num := func(k string) int {
		v, _ := raw[k].(float64)
		return int(v)
	}
	r.perMinute, r.maxConcurrent = num("requestsPerMinute"), num("maxConcurrent")
	r.minDelay = time.Duration(num("minDelayMs")) * time.Millisecond
	r.jitter = time.Duration(num("jitterMs")) * time.Millisecond
	return r
}

// wait holds a fetcher until its next request is due.
func (r *rate) wait(ctx context.Context) error {
	gap := r.minDelay
	if r.perMinute > 0 {
		if every := time.Minute / time.Duration(r.perMinute); every > gap {
			gap = every
		}
	}
	if r.jitter > 0 {
		gap += time.Duration(rand.Int64N(int64(r.jitter)))
	}
	if gap <= 0 {
		return ctx.Err()
	}
	r.mu.Lock()
	now := time.Now()
	if r.next.Before(now) {
		r.next = now
	}
	due := r.next
	r.next = due.Add(gap)
	r.mu.Unlock()
	d := time.Until(due)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
