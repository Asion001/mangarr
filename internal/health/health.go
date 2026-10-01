// Package health runs Sonarr-style health checks and publishes
// health.issue / health.restored events on transitions.
package health

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/fsutil"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/settings"
)

const (
	Notice  = "notice"
	Warning = "warning"
	Error   = "error"
)

type Check struct {
	Source  string `json:"source"`
	Type    string `json:"type"`
	Message string `json:"message"`
	Link    string `json:"link,omitempty"`
	// Items are the things the check is about (e.g. series), with links.
	Items []CheckItem `json:"items,omitempty"`
	// Key (optional) identifies the check across runs when its message
	// changes (e.g. carries a count), so a new count is not a new issue.
	Key string `json:"-"`
}

// CheckItem is one thing a check is about.
type CheckItem struct {
	Label string `json:"label"`
	Link  string `json:"link,omitempty"`
	// Detail explains the item's problem (e.g. the last error).
	Detail string `json:"detail,omitempty"`
}

func (c Check) key() string {
	if c.Key != "" {
		return c.Source + "|" + c.Key
	}
	return c.Source + "|" + c.Message
}

// Text is the message with the items' labels, for notifications.
func (c Check) Text() string {
	if len(c.Items) == 0 {
		return c.Message
	}
	labels := make([]string, 0, len(c.Items))
	for _, it := range c.Items {
		labels = append(labels, it.Label)
	}
	return c.Message + ": " + strings.Join(first(labels, 5), "; ")
}

// moduleLink is the settings page of a module kind.
func moduleLink(kind string) string {
	switch kind {
	case "source":
		return "/settings/sources"
	case "metadata":
		return "/settings/metadata"
	case "library":
		return "/settings/library"
	case "notify":
		return "/settings/notifications"
	case "upscale":
		return "/settings/upscalers"
	}
	return ""
}

func seriesItem(id int64, title, detail string) CheckItem {
	if len(detail) > 300 {
		detail = detail[:300] + "…"
	}
	return CheckItem{Label: title, Link: fmt.Sprintf("/series/%d", id), Detail: detail}
}

// StatusProvider exposes failing instances of a subsystem (notifications, rescans).
type StatusProvider interface {
	Status() map[int64]string
}

type Checker struct {
	db       *db.DB
	bus      *events.Bus
	mods     *modules.Manager
	settings *settings.Store
	log      *slog.Logger
	// Extra status sources, keyed by label.
	statuses map[string]func() map[int64]string
	// Extra checks registered by other packages.
	checks []func(ctx context.Context) []Check

	mu      sync.Mutex
	results []Check
	last    map[string]Check
	at      time.Time
}

func New(d *db.DB, bus *events.Bus, mods *modules.Manager, st *settings.Store, log *slog.Logger) *Checker {
	return &Checker{db: d, bus: bus, mods: mods, settings: st, log: log, statuses: map[string]func() map[int64]string{}, last: map[string]Check{}}
}

// AddCheck registers an extra check.
func (c *Checker) AddCheck(fn func(ctx context.Context) []Check) { c.checks = append(c.checks, fn) }

// AddStatus registers a subsystem whose failing instances become warnings.
func (c *Checker) AddStatus(label string, fn func() map[int64]string) { c.statuses[label] = fn }

// Results returns the latest results.
func (c *Checker) Results() ([]Check, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Check(nil), c.results...), c.at
}

// Run executes every check and publishes transitions.
func (c *Checker) Run(ctx context.Context) []Check {
	var out []Check
	add := func(ch Check) { out = append(out, ch) }

	c.checkModules(ctx, add)
	c.checkRootFolders(ctx, add)
	c.checkSources(ctx, add)
	c.checkQueue(ctx, add)
	c.checkReaders(ctx, add)
	c.checkUpscale(ctx, add)
	for _, fn := range c.checks {
		for _, ch := range fn(ctx) {
			add(ch)
		}
	}
	for label, fn := range c.statuses {
		for id, msg := range fn() {
			name, link := fmt.Sprintf("#%d", id), ""
			if l, ok := c.mods.Get(id); ok {
				name, link = l.Def.Name, moduleLink(l.Def.Kind)
			}
			add(Check{Source: label, Type: Warning, Message: name + ": " + msg, Link: link})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Type) > rank(out[j].Type) })

	c.mu.Lock()
	prev := c.last
	next := map[string]Check{}
	for _, ch := range out {
		next[ch.key()] = ch
	}
	c.results, c.last, c.at = out, next, time.Now().UTC()
	c.mu.Unlock()

	for k, ch := range next {
		if _, ok := prev[k]; !ok && ch.Type != Notice {
			c.bus.Publish(events.Event{Type: events.HealthIssue, Payload: events.MessagePayload{Title: "Health issue: " + ch.Source, Message: ch.Text()}})
		}
	}
	for k, ch := range prev {
		if _, ok := next[k]; !ok && ch.Type != Notice {
			c.bus.Publish(events.Event{Type: events.HealthRestored, Payload: events.MessagePayload{Title: "Health restored: " + ch.Source, Message: ch.Text()}})
		}
	}
	c.bus.Changed("health", "sync", 0)
	return out
}

func rank(t string) int {
	switch t {
	case Error:
		return 2
	case Warning:
		return 1
	}
	return 0
}

func (c *Checker) checkModules(ctx context.Context, add func(Check)) {
	if len(c.mods.Active(modules.KindSource)) == 0 {
		add(Check{Source: "Sources", Type: Warning, Message: "No source module is configured; add one under Settings → Sources", Link: moduleLink("source")})
	}
	// No metadata module or library server is a choice, not a fault: mangarr
	// works from source metadata and serves reading apps itself. The setup
	// checklist on the Series page offers both.
	for _, l := range c.mods.All("") {
		if !l.Def.Enabled || l.Def.UserID != nil {
			continue // users' own targets are theirs to fix
		}
		label := strings.ToUpper(l.Def.Kind[:1]) + l.Def.Kind[1:]
		if l.Err != nil {
			add(Check{Source: label, Type: Error, Message: fmt.Sprintf("%s is misconfigured: %v", l.Def.Name, l.Err), Link: moduleLink(l.Def.Kind)})
			continue
		}
		if l.Def.Kind == string(modules.KindNotify) {
			continue // Test() would send a message; failures come from the dispatcher status
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		var warn string
		var err error
		if hc, ok := modules.As[modules.HealthChecker](l); ok {
			warn, err = hc.HealthCheck(cctx)
		} else {
			err = l.Instance.Test(cctx)
		}
		cancel()
		switch {
		case err != nil:
			add(Check{Source: label, Type: Error, Message: fmt.Sprintf("%s is unavailable: %v", l.Def.Name, err), Link: moduleLink(l.Def.Kind)})
		case warn != "":
			add(Check{Source: label, Type: Warning, Message: fmt.Sprintf("%s: %s", l.Def.Name, warn), Link: moduleLink(l.Def.Kind)})
		}
	}
}

func (c *Checker) checkRootFolders(ctx context.Context, add func(Check)) {
	var roots []model.RootFolder
	if err := c.db.NewSelect().Model(&roots).Scan(ctx); err != nil {
		return
	}
	if len(roots) == 0 {
		add(Check{Source: "Root folders", Type: Warning, Message: "No root folder is configured", Link: "/settings/media"})
	}
	mm, _ := c.settings.MediaManagement(ctx)
	for _, r := range roots {
		if err := fsutil.Writable(r.Path); err != nil {
			add(Check{Source: "Root folders", Type: Error, Message: fmt.Sprintf("%s is not writable: %v", r.Path, err), Link: "/settings/media"})
			continue
		}
		if free, err := fsutil.FreeSpace(r.Path); err == nil && mm.MinFreeSpaceMB > 0 && free < uint64(mm.MinFreeSpaceMB)*2<<20 {
			add(Check{Source: "Root folders", Type: Warning, Message: fmt.Sprintf("%s is low on space (%d MB free)", r.Path, free>>20), Link: "/settings/media"})
		}
	}
}

func (c *Checker) checkSources(ctx context.Context, add func(Check)) {
	type row struct {
		SeriesID   int64  `bun:"series_id"`
		Title      string `bun:"title"`
		SourceName string `bun:"source_name"`
		Failures   int    `bun:"consecutive_failures"`
		LastError  string `bun:"last_error"`
	}
	var failing []row
	_ = c.db.NewSelect().TableExpr("series_sources AS ss").ColumnExpr("ss.series_id, s.title, ss.source_name, ss.consecutive_failures, ss.last_error").
		Join("JOIN series AS s ON s.id = ss.series_id").Where("ss.enabled = ? AND ss.consecutive_failures >= 3", true).
		OrderExpr("ss.consecutive_failures DESC").Limit(50).Scan(ctx, &failing)
	bySource := map[string][]CheckItem{}
	var order []string
	for _, f := range failing {
		if _, ok := bySource[f.SourceName]; !ok {
			order = append(order, f.SourceName)
		}
		bySource[f.SourceName] = append(bySource[f.SourceName], seriesItem(f.SeriesID, f.Title,
			fmt.Sprintf("%d failed checks in a row: %s", f.Failures, f.LastError)))
	}
	for _, src := range order {
		items := bySource[src]
		add(Check{Source: "Sources", Type: Warning, Message: fmt.Sprintf("%s keeps failing for %d series", src, len(items)), Items: items})
	}
	var orphans []model.Series
	_ = c.db.NewSelect().Model(&orphans).Column("id", "title").
		Where("monitored = ? AND NOT EXISTS (SELECT 1 FROM series_sources ss WHERE ss.series_id = series.id AND ss.enabled = ?)", true, true).
		Order("title").Limit(20).Scan(ctx)
	if len(orphans) > 0 {
		items := make([]CheckItem, 0, len(orphans))
		for _, o := range orphans {
			items = append(items, seriesItem(o.ID, o.Title, "no enabled source: link or enable one"))
		}
		add(Check{Source: "Series", Type: Warning, Message: fmt.Sprintf("%d monitored series have no enabled source", len(orphans)), Items: items})
	}
}

func (c *Checker) checkQueue(ctx context.Context, add func(Check)) {
	n, _ := c.db.NewSelect().Model((*model.DownloadJob)(nil)).Where("status = ?", model.JobFailed).
		Where("updated_at > ?", time.Now().UTC().Add(-24*time.Hour)).Count(ctx)
	if n > 0 {
		add(Check{Source: "Downloads", Type: Warning, Message: fmt.Sprintf("%d downloads failed in the last 24 hours", n), Link: "/activity/queue?status=failed"})
	}
}

func (c *Checker) checkReaders(ctx context.Context, add func(Check)) {
	var bad []struct {
		Name      string `bun:"name"`
		LastError string `bun:"last_error"`
	}
	_ = c.db.NewSelect().TableExpr("reader_accounts AS a").ColumnExpr("r.name, a.last_error").
		Join("JOIN readers AS r ON r.id = a.reader_id").Where("a.last_error <> ''").Scan(ctx, &bad)
	for _, b := range bad {
		add(Check{Source: "Readers", Type: Warning, Message: fmt.Sprintf("progress sync for %s fails: %s", b.Name, b.LastError), Link: "/settings/readers"})
	}
}

func (c *Checker) checkUpscale(ctx context.Context, add func(Check)) {
	var profiles []model.Profile
	if err := c.db.NewSelect().Model(&profiles).Scan(ctx); err != nil {
		return
	}
	for _, p := range profiles {
		if p.Config.Upscale.Enabled && len(c.mods.Active(modules.KindUpscale)) == 0 {
			add(Check{Source: "Upscaling", Type: Warning, Message: fmt.Sprintf("profile %q enables upscaling but no upscaler module is configured", p.Name), Link: moduleLink("upscale")})
		}
	}
}

func first(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], "…")
	}
	return xs
}
