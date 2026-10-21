// Package sourcegov throttles requests to source catalogs so sites don't
// block us. Extensions already apply their own per-site rate limits inside
// the engine (Mihon's RateLimitInterceptor); this adds core-side limits on
// top: a token bucket, a minimum gap with random jitter, a concurrency cap,
// random pauses between chapters and between series checks, and a cooldown
// when a site answers with rate-limit or Cloudflare errors.
package sourcegov

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

// Key identifies a catalog.
type Key struct {
	ModuleID int64
	SourceID string
}

func (k Key) String() string { return fmt.Sprintf("%d:%s", k.ModuleID, k.SourceID) }

// Presets are the built-in throttle profiles. "fast" behaves like Mihon (no
// extra delays, a few parallel requests per source).
var Presets = map[string]model.ThrottleConfig{
	"fast": {Preset: "fast", MaxConcurrent: 5},
	"normal": {Preset: "normal", RequestsPerMinute: 120, Burst: 10, JitterMs: 250, MaxConcurrent: 3,
		ChapterGapMinSec: 2, ChapterGapMaxSec: 6, RefreshGapMinSec: 1, RefreshGapMaxSec: 4},
	"gentle": {Preset: "gentle", RequestsPerMinute: 30, Burst: 3, MinDelayMs: 500, JitterMs: 1500, MaxConcurrent: 1,
		ChapterGapMinSec: 10, ChapterGapMaxSec: 30, RefreshGapMinSec: 5, RefreshGapMaxSec: 15},
}

// Resolve layers throttle configs: the preset named by the last layer that
// names one, then every non-zero field of each layer in order.
func Resolve(layers ...model.ThrottleConfig) model.ThrottleConfig {
	preset := "normal"
	for _, l := range layers {
		if _, ok := Presets[l.Preset]; ok {
			preset = l.Preset
		}
	}
	out := Presets[preset]
	for _, l := range layers {
		if l.Preset != "" && l.Preset != preset {
			continue // fields of a layer that picked another preset are relative to it
		}
		over := func(dst *int, v int) {
			if v != 0 {
				*dst = v
			}
		}
		over(&out.RequestsPerMinute, l.RequestsPerMinute)
		over(&out.Burst, l.Burst)
		over(&out.MinDelayMs, l.MinDelayMs)
		over(&out.JitterMs, l.JitterMs)
		over(&out.MaxConcurrent, l.MaxConcurrent)
		over(&out.ChapterGapMinSec, l.ChapterGapMinSec)
		over(&out.ChapterGapMaxSec, l.ChapterGapMaxSec)
		over(&out.RefreshGapMinSec, l.RefreshGapMinSec)
		over(&out.RefreshGapMaxSec, l.RefreshGapMaxSec)
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = 1
	}
	return out
}

// ErrCoolingDown is returned while a catalog is paused after being throttled.
type ErrCoolingDown struct {
	Key    Key
	Until  time.Time
	Reason string
}

func (e *ErrCoolingDown) Error() string {
	return fmt.Sprintf("source is cooling down until %s after %s", e.Until.Local().Format("15:04"), e.Reason)
}

// CoolingDown reports whether err is (or wraps) ErrCoolingDown.
func CoolingDown(err error) (*ErrCoolingDown, bool) {
	var cd *ErrCoolingDown
	ok := errors.As(err, &cd)
	return cd, ok
}

// Classify detects errors that mean the site is throttling or blocking us.
func Classify(err error) (reason string, throttled bool) {
	if err == nil {
		return "", false
	}
	if _, ok := CoolingDown(err); ok {
		return "", false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "http error 429"), strings.Contains(msg, "too many requests"), strings.Contains(msg, "rate limit"):
		return "rate limited (429)", true
	case strings.Contains(msg, "cloudflare"), strings.Contains(msg, "just a moment"), strings.Contains(msg, "cf-chl"):
		return "Cloudflare challenge", true
	case strings.Contains(msg, "http error 403"):
		return "blocked (403)", true
	case strings.Contains(msg, "http error 503"):
		return "unavailable (503)", true
	}
	return "", false
}

// Cooldown durations grow with consecutive throttling: 5m, 10m, 20m ... 2h.
func cooldownFor(strikes int) time.Duration {
	d := 5 * time.Minute
	for i := 1; i < strikes && d < 2*time.Hour; i++ {
		d *= 2
	}
	return min(d, 2*time.Hour)
}

// CooldownEvent is reported when a catalog enters or leaves a cooldown.
type CooldownEvent struct {
	Key     Key
	Until   *time.Time // nil when the cooldown was cleared
	Strikes int
	Reason  string
}

type state struct {
	inflight int
	changed  chan struct{}

	tokens   float64
	refilled time.Time
	nextAt   time.Time
	pace     map[string]time.Time

	cooldownUntil time.Time
	strikes       int
	reason        string
}

// Governor enforces throttling per catalog.
type Governor struct {
	cfg        func(Key) model.ThrottleConfig
	onCooldown func(CooldownEvent)

	mu    sync.Mutex
	st    map[Key]*state
	stats *Stats

	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	rnd   func(n int64) int64
}

// New returns a governor; cfg returns the effective throttle of a catalog and
// onCooldown (optional) persists cooldown changes.
func New(cfg func(Key) model.ThrottleConfig, onCooldown func(CooldownEvent)) *Governor {
	return &Governor{cfg: cfg, onCooldown: onCooldown, st: map[Key]*state{}, stats: newStats(),
		now: time.Now, sleep: sleepCtx, rnd: func(n int64) int64 { return rand.Int64N(n) }}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
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

func (g *Governor) state(k Key) *state {
	s, ok := g.st[k]
	if !ok {
		s = &state{changed: make(chan struct{}), pace: map[string]time.Time{}}
		g.st[k] = s
	}
	return s
}

func (g *Governor) randDur(maxMs int) time.Duration {
	if maxMs <= 0 {
		return 0
	}
	return time.Duration(g.rnd(int64(maxMs)+1)) * time.Millisecond
}

// Restore sets a persisted cooldown (at startup).
func (g *Governor) Restore(k Key, until time.Time, strikes int, reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s := g.state(k)
	s.cooldownUntil, s.strikes, s.reason = until, strikes, reason
}

// Cooldown returns the end of k's cooldown (zero when not cooling down).
func (g *Governor) Cooldown(k Key) (time.Time, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.st[k]
	if !ok || !g.now().Before(s.cooldownUntil) {
		return time.Time{}, ""
	}
	return s.cooldownUntil, s.reason
}

// CoolingDown lists the catalogs cooling down now.
func (g *Governor) CoolingDown() []Key {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []Key
	now := g.now()
	for k, s := range g.st {
		if now.Before(s.cooldownUntil) {
			out = append(out, k)
		}
	}
	return out
}

// ClearCooldown ends k's cooldown and resets its strikes.
func (g *Governor) ClearCooldown(k Key) {
	g.mu.Lock()
	s := g.state(k)
	had := s.strikes > 0 || !s.cooldownUntil.IsZero()
	s.cooldownUntil, s.strikes, s.reason = time.Time{}, 0, ""
	g.mu.Unlock()
	if had && g.onCooldown != nil {
		g.onCooldown(CooldownEvent{Key: k})
	}
}

// Acquire waits for a request slot on k. Call release when the request is done.
func (g *Governor) Acquire(ctx context.Context, k Key) (release func(), err error) {
	cfg := g.cfg(k)
	// concurrency
	for {
		g.mu.Lock()
		s := g.state(k)
		if now := g.now(); now.Before(s.cooldownUntil) {
			until, reason := s.cooldownUntil, s.reason
			g.mu.Unlock()
			return nil, &ErrCoolingDown{Key: k, Until: until, Reason: reason}
		}
		if s.inflight < cfg.MaxConcurrent {
			s.inflight++
			g.mu.Unlock()
			break
		}
		ch := s.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ch:
		}
	}
	release = func() {
		g.mu.Lock()
		s := g.state(k)
		s.inflight--
		close(s.changed)
		s.changed = make(chan struct{})
		g.mu.Unlock()
	}
	// rate: token bucket + minimum gap with jitter
	for {
		g.mu.Lock()
		s := g.state(k)
		now := g.now()
		wait := s.nextAt.Sub(now)
		if cfg.RequestsPerMinute > 0 {
			burst := float64(max(cfg.Burst, 1))
			rate := float64(cfg.RequestsPerMinute) / 60 // tokens per second
			if s.refilled.IsZero() {
				s.tokens, s.refilled = burst, now
			}
			s.tokens = min(burst, s.tokens+now.Sub(s.refilled).Seconds()*rate)
			s.refilled = now
			if s.tokens < 1 {
				wait = max(wait, time.Duration((1-s.tokens)/rate*float64(time.Second)))
			}
		}
		if wait <= 0 {
			if cfg.RequestsPerMinute > 0 {
				s.tokens--
			}
			s.nextAt = now.Add(time.Duration(cfg.MinDelayMs)*time.Millisecond + g.randDur(cfg.JitterMs))
			g.mu.Unlock()
			return release, nil
		}
		g.mu.Unlock()
		if err := g.sleep(ctx, wait); err != nil {
			release()
			return nil, err
		}
	}
}

// Pace spaces out work of one kind ("chapter", "refresh") on k with a random
// gap between the configured bounds. Concurrent callers get successive slots.
func (g *Governor) Pace(ctx context.Context, k Key, kind string) error {
	cfg := g.cfg(k)
	lo, hi := cfg.ChapterGapMinSec, cfg.ChapterGapMaxSec
	if kind == "refresh" {
		lo, hi = cfg.RefreshGapMinSec, cfg.RefreshGapMaxSec
	}
	hi = max(hi, lo)
	if hi <= 0 {
		return nil
	}
	g.mu.Lock()
	s := g.state(k)
	now := g.now()
	slot := now
	if last, ok := s.pace[kind]; ok {
		gap := time.Duration(lo)*time.Second + g.randDur((hi-lo)*1000)
		if t := last.Add(gap); t.After(slot) {
			slot = t
		}
	}
	s.pace[kind] = slot
	g.mu.Unlock()
	return g.sleep(ctx, slot.Sub(now))
}

// Report records the outcome of a request on k: throttling errors start or
// extend a cooldown, successes reset the strike count.
func (g *Governor) Report(k Key, err error) {
	reason, throttled := Classify(err)
	g.mu.Lock()
	s := g.state(k)
	var ev *CooldownEvent
	switch {
	case throttled:
		s.strikes++
		until := g.now().Add(cooldownFor(s.strikes))
		s.cooldownUntil, s.reason = until, reason
		ev = &CooldownEvent{Key: k, Until: &until, Strikes: s.strikes, Reason: reason}
	case err == nil && s.strikes > 0 && !g.now().Before(s.cooldownUntil):
		s.strikes, s.reason, s.cooldownUntil = 0, "", time.Time{}
		ev = &CooldownEvent{Key: k}
	}
	g.mu.Unlock()
	if ev != nil && g.onCooldown != nil {
		g.onCooldown(*ev)
	}
}

// Do runs fn with a request slot on k and reports its outcome.
func (g *Governor) Do(ctx context.Context, k Key, fn func(ctx context.Context) error) error {
	return g.DoOp(ctx, k, "", fn)
}

// DoOp is Do for a request described by what (kept with its failures).
func (g *Governor) DoOp(ctx context.Context, k Key, what string, fn func(ctx context.Context) error) error {
	release, err := g.Acquire(ctx, k)
	if err != nil {
		return err
	}
	defer release()
	start := g.now()
	err = fn(ctx)
	if ctx.Err() == nil {
		g.observe(k, what, g.now().Sub(start), err)
		g.Report(k, err)
	}
	return err
}

// observe counts a finished request on k for the reliability view.
func (g *Governor) observe(k Key, what string, d time.Duration, err error) {
	g.stats.record(k, g.now(), what, d, err)
}

// Observe counts a request made on another machine (a worker fetching a
// chapter's pages itself) in k's reliability numbers.
func (g *Governor) Observe(k Key, what string, d time.Duration, err error) {
	if g != nil {
		g.observe(k, what, d, err)
	}
}

// Fallback counts a download that gave up on k and used another catalog.
func (g *Governor) Fallback(k Key) {
	if g != nil {
		g.stats.fallback(k, g.now())
	}
}

// Stats are the request counts and recent failures of every catalog.
func (g *Governor) Stats() *Stats { return g.stats }

// Limits is the effective throttle of a catalog, for handing part of its
// budget to another machine that will make the requests itself.
func (g *Governor) Limits(k Key) model.ThrottleConfig {
	if g == nil || g.cfg == nil {
		return Presets["normal"]
	}
	return g.cfg(k)
}
