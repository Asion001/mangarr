// Package notifications turns bus events into messages for notify modules:
// per-series digests for imported chapters, event/tag filtering per
// instance and escalating backoff for failing instances.
package notifications

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/notify"
	"github.com/Asion001/mangarr/internal/settings"
)

// DefaultEvents are used when an instance has no explicit event selection.
var DefaultEvents = []string{events.ChapterImported, events.ChapterUpgraded, events.DownloadFailed, events.HealthIssue, events.CleanupDone}

type Dispatcher struct {
	db       *db.DB
	bus      *events.Bus
	mods     *modules.Manager
	settings *settings.Store
	log      *slog.Logger

	// DigestQuiet is how long to wait for more chapters of the same series.
	DigestQuiet time.Duration
	// DigestMax caps how long a digest may be delayed.
	DigestMax time.Duration

	// Followers lists the users to tell about a series' new chapters (those
	// following it who can see it).
	Followers func(ctx context.Context, seriesID int64) []int64
	// Personal hands those users' messages to their linked messenger
	// accounts (internal/messenger).
	Personal func(ctx context.Context, users []int64, event string, seriesID int64, msg notify.Message)
	// Announce posts install-wide events through the messenger bots.
	Announce func(ctx context.Context, event string, msg notify.Message)

	mu      sync.Mutex
	digests map[int64]*digest
	status  map[int64]*instanceStatus
	ctx     context.Context
}

type digest struct {
	series   int64
	title    string
	cover    string
	items    []events.ChapterImportedPayload
	upgraded []events.ChapterImportedPayload
	first    time.Time
	timer    *time.Timer
}

type instanceStatus struct {
	Failures  int       `json:"failures"`
	LastError string    `json:"lastError,omitempty"`
	Until     time.Time `json:"disabledUntil,omitempty"`
}

func New(d *db.DB, bus *events.Bus, mods *modules.Manager, st *settings.Store, log *slog.Logger) *Dispatcher {
	return &Dispatcher{db: d, bus: bus, mods: mods, settings: st, log: log, DigestQuiet: 90 * time.Second, DigestMax: 10 * time.Minute,
		digests: map[int64]*digest{}, status: map[int64]*instanceStatus{}}
}

func (d *Dispatcher) Start(ctx context.Context) error {
	d.ctx = ctx
	d.bus.Subscribe(d.handle, events.NotificationEvents...)
	return nil
}

// Status returns failing instances (for health checks).
func (d *Dispatcher) Status() map[int64]instanceStatus {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := map[int64]instanceStatus{}
	for id, s := range d.status {
		if l, ok := d.mods.Get(id); ok && l.Def.UserID != nil {
			continue // a user's own target is their business
		}
		if s.Failures > 0 {
			out[id] = *s
		}
	}
	return out
}

func (d *Dispatcher) handle(e events.Event) {
	switch e.Type {
	case events.ChapterImported, events.ChapterUpgraded:
		p, ok := e.Payload.(events.ChapterImportedPayload)
		if !ok {
			return
		}
		d.addToDigest(e.SeriesID, e.Type, p)
	default:
		msg := notify.Message{Event: e.Type, SeriesID: e.SeriesID}
		switch p := e.Payload.(type) {
		case events.MessagePayload:
			msg.Title, msg.Body, msg.Items = p.Title, p.Message, p.Items
		default:
			msg.Title = e.Type
		}
		go d.dispatch(e.Type, e.SeriesID, msg)
	}
}

func (d *Dispatcher) addToDigest(seriesID int64, typ string, p events.ChapterImportedPayload) {
	d.mu.Lock()
	defer d.mu.Unlock()
	dg := d.digests[seriesID]
	if dg == nil {
		dg = &digest{series: seriesID, title: p.SeriesTitle, cover: p.CoverURL, first: time.Now()}
		d.digests[seriesID] = dg
	}
	if typ == events.ChapterUpgraded {
		dg.upgraded = append(dg.upgraded, p)
	} else {
		dg.items = append(dg.items, p)
	}
	wait := d.DigestQuiet
	if remaining := d.DigestMax - time.Since(dg.first); remaining < wait {
		wait = max(remaining, 0)
	}
	if dg.timer != nil {
		dg.timer.Stop()
	}
	dg.timer = time.AfterFunc(wait, func() { d.flush(seriesID) })
}

func (d *Dispatcher) flush(seriesID int64) {
	d.mu.Lock()
	dg := d.digests[seriesID]
	delete(d.digests, seriesID)
	d.mu.Unlock()
	if dg == nil {
		return
	}
	if len(dg.items) > 0 {
		msg := DigestMessage(dg.title, dg.cover, dg.items, false)
		d.dispatch(events.ChapterImported, seriesID, msg)
		if d.Followers != nil {
			if users := d.Followers(d.context(), seriesID); len(users) > 0 {
				d.SendToUsers(users, events.ChapterImported, seriesID, msg)
			}
		}
	}
	if len(dg.upgraded) > 0 {
		d.dispatch(events.ChapterUpgraded, seriesID, DigestMessage(dg.title, dg.cover, dg.upgraded, true))
	}
}

// DigestMessage renders "Series: 3 new chapters (120–122)".
func DigestMessage(series, cover string, items []events.ChapterImportedPayload, upgrade bool) notify.Message {
	sort.Slice(items, func(i, j int) bool { return items[i].NumberSort < items[j].NumberSort })
	n := len(items)
	verb := "new chapter"
	if upgrade {
		verb = "upgraded chapter"
	}
	if n != 1 {
		verb += "s"
	}
	title := fmt.Sprintf("%s: %d %s", series, n, verb)
	body := ""
	if n == 1 {
		body = "Chapter " + items[0].Chapter
	} else {
		body = fmt.Sprintf("Chapters %s–%s", items[0].Chapter, items[n-1].Chapter)
	}
	var lines []string
	if n <= 15 {
		for _, it := range items {
			line := "Ch. " + it.Chapter
			if it.Title != "" {
				line += " – " + it.Title
			}
			var extra []string
			if it.Scanlator != "" {
				extra = append(extra, it.Scanlator)
			}
			if it.Upscaled {
				extra = append(extra, "upscaled")
			}
			if len(extra) > 0 {
				line += " (" + strings.Join(extra, ", ") + ")"
			}
			lines = append(lines, line)
		}
	}
	return notify.Message{Title: title, Body: body, Items: lines, ImageURL: cover, Series: series}
}

func wants(def model.ProviderDefinition, event string) bool {
	evs := def.Events
	if len(evs) == 0 {
		evs = DefaultEvents
	}
	for _, e := range evs {
		if e == event {
			return true
		}
	}
	return false
}

var backoffSteps = []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

func (d *Dispatcher) context() context.Context {
	if d.ctx == nil {
		return context.Background()
	}
	return d.ctx
}

// fill completes a message about a series (title, link).
func (d *Dispatcher) fill(ctx context.Context, event string, seriesID int64, msg *notify.Message) []int64 {
	msg.Event, msg.SeriesID = event, seriesID
	var seriesTags []int64
	if seriesID > 0 {
		var s model.Series
		if err := d.db.NewSelect().Model(&s).Column("id", "title", "tags").Where("id = ?", seriesID).Scan(ctx); err == nil {
			seriesTags = s.Tags
			if msg.Series == "" {
				msg.Series = s.Title
			}
		}
		if g, err := d.settings.General(ctx); err == nil && g.PublicURL != "" && msg.URL == "" {
			msg.URL = g.PublicURL + "/series/" + strconv.FormatInt(seriesID, 10)
		}
	}
	return seriesTags
}

// dispatch sends an event to the install's notification targets.
func (d *Dispatcher) dispatch(event string, seriesID int64, msg notify.Message) {
	ctx := d.context()
	seriesTags := d.fill(ctx, event, seriesID, &msg)
	if d.Announce != nil {
		go d.Announce(ctx, event, msg)
	}
	for _, inst := range modules.ActiveAs[notify.Module](d.mods, modules.KindNotify) {
		if inst.Def.UserID != nil || !wants(inst.Def, event) || !tagsMatch(inst.Def.Tags, seriesTags) {
			continue
		}
		d.send(ctx, inst, event, msg)
	}
}

// SendToUsers sends an event to those users' linked messenger accounts.
func (d *Dispatcher) SendToUsers(users []int64, event string, seriesID int64, msg notify.Message) {
	ctx := d.context()
	d.fill(ctx, event, seriesID, &msg)
	if d.Personal != nil {
		d.Personal(ctx, users, event, seriesID, msg)
	}
}

// send delivers to one instance, backing off after failures.
func (d *Dispatcher) send(ctx context.Context, inst modules.Typed[notify.Module], event string, msg notify.Message) {
	d.mu.Lock()
	st := d.status[inst.Def.ID]
	if st == nil {
		st = &instanceStatus{}
		d.status[inst.Def.ID] = st
	}
	skip := time.Now().Before(st.Until)
	d.mu.Unlock()
	if skip {
		return
	}
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err := inst.Instance.Send(sctx, msg)
	cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		st.Failures++
		st.LastError = err.Error()
		st.Until = time.Now().Add(backoffSteps[min(st.Failures, len(backoffSteps)-1)])
		d.log.Warn("notification failed", "instance", inst.Def.Name, "event", event, "err", err)
	} else {
		st.Failures, st.LastError, st.Until = 0, "", time.Time{}
	}
}

// wantsPersonal: a user's target gets every personal event unless it picked some.
func tagsMatch(defTags, seriesTags []int64) bool {
	if len(defTags) == 0 {
		return true
	}
	for _, a := range defTags {
		for _, b := range seriesTags {
			if a == b {
				return true
			}
		}
	}
	return false
}
