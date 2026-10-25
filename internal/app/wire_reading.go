package app

import (
	"context"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/komgaapi"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/library"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/Asion001/mangarr/internal/readsync"
)

// Progress fan-out delays: changes are batched per series (a reader turning
// pages sends one update every few seconds); unreads go out sooner so a
// server's own sync doesn't bring them back first.
const (
	FanOutDelay       = 30 * time.Second
	FanOutUnreadDelay = 3 * time.Second
)

// ReadAheadDelay batches a reader's progress before looking ahead.
const ReadAheadDelay = 10 * time.Second

// Debouncer runs a function once things settle, per key.
type Debouncer struct {
	mu      sync.Mutex
	delay   time.Duration
	pending map[[2]int64]*time.Timer
}

// SetDelay changes the delay (tests).
func (d *Debouncer) SetDelay(delay time.Duration) {
	d.mu.Lock()
	d.delay = delay
	d.mu.Unlock()
}

// Do runs fn after the delay, restarting the wait on every call for key.
func (d *Debouncer) Do(key [2]int64, fn func()) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t := d.pending[key]; t != nil {
		t.Stop()
	}
	d.pending[key] = time.AfterFunc(d.delay, func() {
		d.mu.Lock()
		delete(d.pending, key)
		d.mu.Unlock()
		fn()
	})
}

// wireReading sets up what reading apps see, the Komga-compatible API and
// mangarr as the progress hub between apps and library servers.
func (a *App) wireReading(ctx context.Context) error {
	a.Reading = &reading.Service{DB: a.DB, Settings: a.Settings, Library: a.Library, ImageCache: a.ImageCache, Mods: a.Modules,
		HTTP: a.HTTP, Bus: a.Bus, Downloads: a.Searcher, Staged: a.Downloads.StagedPage, Log: a.Log.With("component", "reading")}
	a.Komga = komgaapi.NewService(komgaapi.Deps{DB: a.DB, Settings: a.Settings, Auth: a.Auth, Reading: a.Reading, Bus: a.Bus, Maintenance: a.InMaintenance,
		Log: a.Log.With("component", "komga-api"), WebListen: a.Cfg.Listen, URLBase: a.Cfg.URLBase}, a.Cfg.KomgaListen)
	a.AddService(a.Komga)

	// what library servers report is logged per server and announced like
	// app progress
	a.ReadSync.OnChange = func(ctx context.Context, acc *model.LibraryAccount, changes []readsync.Change) {
		name := "Library server"
		if l, ok := a.Modules.Get(acc.ModuleID); ok {
			name = l.Def.Name
		}
		out := make([]reading.Outcome, len(changes))
		for i, c := range changes {
			out[i] = reading.Outcome{Change: reading.Change{ChapterID: c.ChapterID, SeriesID: c.SeriesID, Completed: c.Completed, Page: c.Page,
				Unread: c.Outcome == model.OutcomeUnread}, Result: c.Outcome}
		}
		a.Reading.Observe(ctx, acc.UserID, out, reading.By{Origin: model.EventOriginServer, Client: name})
	}

	// every change reaches the other library servers
	// reading drives downloads: the next chapters after a reader's position
	a.ReadAhead = &Debouncer{delay: ReadAheadDelay, pending: map[[2]int64]*time.Timer{}}
	a.Bus.Subscribe(func(e events.Event) {
		p, ok := e.Payload.(reading.ProgressPayload)
		if !ok || p.Deleted || e.SeriesID == 0 {
			return
		}
		seriesID, userID := e.SeriesID, p.UserID
		a.ReadAhead.Do([2]int64{userID, seriesID}, func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			if _, err := a.Reading.ReadAhead(ctx, userID, seriesID); err != nil {
				a.Log.Warn("read ahead failed", "series", seriesID, "err", err)
			}
		})
	}, reading.ProgressChanged)

	a.FanOut = &FanOut{a: a, pending: map[int64]*fanOutSeries{}, delay: FanOutDelay, unreadDelay: FanOutUnreadDelay}
	a.Bus.Subscribe(func(e events.Event) {
		p, ok := e.Payload.(reading.ProgressPayload)
		if !ok || e.SeriesID == 0 {
			return
		}
		// what a server reported only needs to reach the user's other servers
		if p.Origin == model.EventOriginServer && a.FanOut.writableAccounts(p.UserID) < 2 {
			return
		}
		a.FanOut.schedule(e.SeriesID, p)
	}, reading.ProgressChanged)
	return nil
}

// FanOut pushes progress changes to library servers, batched per series.
type FanOut struct {
	a                  *App
	mu                 sync.Mutex
	pending            map[int64]*fanOutSeries
	delay, unreadDelay time.Duration
}

// SetDelays changes the batching delays (tests).
func (f *FanOut) SetDelays(delay, unread time.Duration) {
	f.mu.Lock()
	f.delay, f.unreadDelay = delay, unread
	f.mu.Unlock()
}

type fanOutSeries struct {
	timer  *time.Timer
	due    time.Time
	unread map[int64]bool
}

// writableAccounts counts the user's accounts on servers that accept progress.
func (f *FanOut) writableAccounts(userID int64) int {
	var accs []model.LibraryAccount
	if err := f.a.DB.NewSelect().Model(&accs).Column("id", "module_id").Where("user_id = ?", userID).Scan(context.Background()); err != nil {
		return 0
	}
	n := 0
	for _, acc := range accs {
		if _, _, err := modules.GetAs[library.ProgressWriter](f.a.Modules, acc.ModuleID); err == nil {
			n++
		}
	}
	return n
}

func (f *FanOut) schedule(seriesID int64, p reading.ProgressPayload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ps := f.pending[seriesID]
	if ps == nil {
		ps = &fanOutSeries{unread: map[int64]bool{}}
		f.pending[seriesID] = ps
	}
	delay := f.delay
	if p.Deleted {
		delay = f.unreadDelay
		for _, c := range p.ChapterIDs {
			ps.unread[c] = true
		}
	}
	due := time.Now().Add(delay)
	if ps.timer != nil {
		if !due.Before(ps.due) {
			return // already due sooner
		}
		ps.timer.Stop()
	}
	ps.due = due
	ps.timer = time.AfterFunc(delay, func() { f.push(seriesID) })
}

func (f *FanOut) push(seriesID int64) {
	f.mu.Lock()
	ps := f.pending[seriesID]
	delete(f.pending, seriesID)
	f.mu.Unlock()
	if ps == nil {
		return
	}
	unread := make([]int64, 0, len(ps.unread))
	for c := range ps.unread {
		unread = append(unread, c)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := f.a.ReadSync.PushSeries(ctx, seriesID, unread)
	switch {
	case err != nil:
		f.a.Log.Warn("progress not pushed to library servers", "series", seriesID, "err", err)
	case res.Written > 0:
		f.a.Log.Info("progress pushed to library servers", "series", seriesID, "books", res.Written, "unknownToServer", res.Missing)
	}
}
