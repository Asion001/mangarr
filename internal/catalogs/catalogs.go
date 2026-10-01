// Package catalogs tracks the catalogs (individual sources such as
// "MangaDex (EN)") offered by the active source modules, with per-catalog
// preferences (enabled, priority, throttling) and the global source settings
// (hidden NSFW catalogs, default languages).
//
// It keeps a generation counter that changes whenever the set of usable
// catalogs may have changed (module reloads, extension changes, preference or
// settings edits), so caches keyed by it never serve results from catalogs
// that were disabled, hidden or removed.
package catalogs

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/quiet"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcegov"
	"github.com/Asion001/mangarr/internal/sourcepriority"
)

// ListTTL is how long a module's catalog list is reused.
const ListTTL = time.Minute

// DefaultPriority is the priority of catalogs without preferences.
const DefaultPriority = 100

// Catalog is one source catalog of a module instance with its preferences.
type Catalog struct {
	ModuleID   int64  `json:"moduleId"`
	ModuleName string `json:"moduleName"`
	source.SourceInfo
	Enabled  bool `json:"enabled"`
	Priority int  `json:"priority"`
	// Hidden is true for NSFW catalogs while NSFW catalogs are hidden.
	Hidden bool `json:"hidden"`
	// Throttle is this catalog's override (empty = global settings).
	Throttle       model.ThrottleConfig `json:"throttle"`
	CooldownUntil  *time.Time           `json:"cooldownUntil,omitempty"`
	CooldownReason string               `json:"cooldownReason,omitempty"`
}

// Key identifies a catalog across modules.
func (c Catalog) Key() string { return Key(c.ModuleID, c.ID) }

// Key builds the "moduleId:sourceId" key.
func Key(moduleID int64, sourceID string) string {
	return strconv.FormatInt(moduleID, 10) + ":" + sourceID
}

// ParseKey splits a "moduleId:sourceId" key.
func ParseKey(k string) (int64, string, bool) {
	m, s, ok := strings.Cut(k, ":")
	id, err := strconv.ParseInt(m, 10, 64)
	return id, s, ok && err == nil && s != ""
}

type entry struct {
	at   time.Time
	list []source.SourceInfo
}

type prefKey struct {
	module int64
	source string
}

// Service caches catalog lists and preferences.
type Service struct {
	db       *db.DB
	mods     *modules.Manager
	bus      *events.Bus
	settings *settings.Store
	log      *slog.Logger
	// Gov throttles requests to catalogs.
	Gov *sourcegov.Governor

	mu    sync.Mutex
	lists map[int64]entry
	prefs map[prefKey]model.CatalogPref
	gen   atomic.Int64
}

func New(d *db.DB, mods *modules.Manager, bus *events.Bus, st *settings.Store, log *slog.Logger) *Service {
	s := &Service{db: d, mods: mods, bus: bus, settings: st, log: log, lists: map[int64]entry{}, prefs: map[prefKey]model.CatalogPref{}}
	s.gen.Store(time.Now().UnixMilli()) // distinct across restarts
	s.Gov = sourcegov.New(s.throttle, s.persistCooldown)
	mods.OnChange(func() { s.Invalidate(0) })
	mods.Decorate(modules.KindSource, sourcegov.Decorator(s.Gov))
	return s
}

// Load reads preferences and persisted cooldowns (call once at startup).
func (s *Service) Load(ctx context.Context) error {
	var rows []model.CatalogPref
	if err := s.db.NewSelect().Model(&rows).Scan(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	for _, r := range rows {
		s.prefs[prefKey{r.ModuleID, r.SourceID}] = r
	}
	s.mu.Unlock()
	for _, r := range rows {
		if r.CooldownUntil != nil && r.CooldownUntil.After(time.Now()) {
			s.Gov.Restore(sourcegov.Key{ModuleID: r.ModuleID, SourceID: r.SourceID}, *r.CooldownUntil, r.CooldownStrikes, r.LastThrottle)
		}
	}
	return nil
}

// Generation changes whenever the usable catalog set may have changed.
func (s *Service) Generation() int64 { return s.gen.Load() }

// Bump changes the generation (e.g. after settings edits) and notifies clients.
func (s *Service) Bump() {
	g := s.gen.Add(1)
	if s.bus != nil {
		s.bus.Changed("catalogs", "updated", g)
	}
}

// Invalidate drops the cached list of a module (0 = all) and bumps the generation.
func (s *Service) Invalidate(moduleID int64) {
	s.mu.Lock()
	if moduleID == 0 {
		s.lists = map[int64]entry{}
	} else {
		delete(s.lists, moduleID)
	}
	s.mu.Unlock()
	s.Bump()
}

func (s *Service) sourceSettings() settings.Sources {
	v, err := s.settings.Sources(context.Background())
	if err != nil {
		return settings.DefaultSources()
	}
	return v
}

// throttle resolves the effective throttle of a catalog.
func (s *Service) throttle(k sourcegov.Key) model.ThrottleConfig {
	s.mu.Lock()
	p := s.prefs[prefKey{k.ModuleID, k.SourceID}]
	s.mu.Unlock()
	layers := []model.ThrottleConfig{s.sourceSettings().Throttle, p.Throttle}
	// quiet hours can switch to a gentler preset
	if sched, err := s.settings.Schedule(context.Background()); err == nil {
		if q := quiet.Evaluate(sched, time.Now()); q.Throttle != "" {
			layers = append(layers, model.ThrottleConfig{Preset: q.Throttle})
		}
	}
	return sourcegov.Resolve(layers...)
}

// EffectiveThrottle returns the throttle applied to a catalog.
func (s *Service) EffectiveThrottle(moduleID int64, sourceID string) model.ThrottleConfig {
	return s.throttle(sourcegov.Key{ModuleID: moduleID, SourceID: sourceID})
}

// persistCooldown stores cooldown changes without blocking the request path.
func (s *Service) persistCooldown(ev sourcegov.CooldownEvent) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := s.updatePref(ctx, ev.Key.ModuleID, ev.Key.SourceID, func(p *model.CatalogPref) {
			p.CooldownUntil, p.CooldownStrikes, p.LastThrottle = ev.Until, ev.Strikes, ev.Reason
		}); err != nil && s.log != nil {
			s.log.Warn("persist catalog cooldown", "catalog", ev.Key.String(), "err", err)
		}
		if ev.Until != nil && s.log != nil {
			s.log.Warn("source throttled us, pausing it", "catalog", ev.Key.String(), "until", ev.Until.Local().Format(time.TimeOnly), "reason", ev.Reason)
		}
		if s.bus != nil {
			s.bus.Changed("catalogs", "cooldown", s.gen.Load())
		}
	}()
}

func (s *Service) pref(moduleID int64, sourceID string) (model.CatalogPref, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.prefs[prefKey{moduleID, sourceID}]
	return p, ok
}

// DefaultOn says whether a catalog nobody switched on or off is on: every
// catalog until search languages are chosen, then only those in a search
// language or a language default, multi-language ones, and catalogs a
// language default names.
func DefaultOn(st settings.Sources, moduleID int64, sourceID, lang string) bool {
	langs := slices.Clone(st.DefaultLanguages)
	key := Key(moduleID, sourceID)
	for _, d := range st.LanguageDefaults {
		langs = append(langs, strings.ToLower(strings.TrimSpace(d.Language)))
		if slices.Contains(d.Sources, key) {
			return true
		}
	}
	return langMatch(lang, langs)
}

// defaultOn is DefaultOn for a catalog by id, its language from the cached
// lists (unknown: on).
func (s *Service) defaultOn(moduleID int64, sourceID string) bool {
	s.mu.Lock()
	e, ok := s.lists[moduleID]
	s.mu.Unlock()
	if ok {
		for _, si := range e.list {
			if si.ID == sourceID {
				return DefaultOn(s.sourceSettings(), moduleID, sourceID, si.Lang)
			}
		}
	}
	return true
}

func (s *Service) decorate(moduleID int64, moduleName string, si source.SourceInfo, st settings.Sources) Catalog {
	c := Catalog{ModuleID: moduleID, ModuleName: moduleName, SourceInfo: si, Enabled: DefaultOn(st, moduleID, si.ID, si.Lang), Priority: DefaultPriority, Hidden: st.HideNSFW && si.NSFW}
	if p, ok := s.pref(moduleID, si.ID); ok {
		c.Enabled, c.Priority, c.Throttle = p.Enabled, p.Priority, p.Throttle
	}
	if until, reason := s.Gov.Cooldown(sourcegov.Key{ModuleID: moduleID, SourceID: si.ID}); !until.IsZero() {
		c.CooldownUntil, c.CooldownReason = &until, reason
	}
	return c
}

// List returns every catalog of the active source modules (including hidden
// and disabled ones), sorted by language and name.
func (s *Service) List(ctx context.Context, fresh bool) (out []Catalog, errs []string) {
	st := s.sourceSettings()
	for _, m := range modules.ActiveAs[source.Module](s.mods, modules.KindSource) {
		s.mu.Lock()
		e, ok := s.lists[m.Def.ID]
		s.mu.Unlock()
		if !ok || fresh || time.Since(e.at) > ListTTL {
			l, err := m.Instance.Sources(ctx)
			if err != nil {
				errs = append(errs, m.Def.Name+": "+err.Error())
				continue
			}
			e = entry{at: time.Now(), list: l}
			s.mu.Lock()
			s.lists[m.Def.ID] = e
			s.mu.Unlock()
		}
		for _, si := range e.list {
			out = append(out, s.decorate(m.Def.ID, m.Def.Name, si, st))
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Lang != out[j].Lang {
			return out[i].Lang < out[j].Lang
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, errs
}

// Scope selects catalogs for searches.
type Scope string

const (
	// ScopeActive: enabled catalogs in the default languages.
	ScopeActive Scope = "active"
	// ScopeAll: every catalog that isn't hidden.
	ScopeAll Scope = "all"
)

// Filter narrows Select.
type Filter struct {
	RootFolderID int64
	Scope        Scope
	// Lang overrides the default languages ("" = defaults for active scope).
	Lang string
	// Keys selects exact catalogs ("moduleId:sourceId"); disabled ones are allowed.
	Keys []string
}

func langMatch(catalogLang string, langs []string) bool {
	if len(langs) == 0 || catalogLang == "all" || catalogLang == "multi" {
		return true
	}
	return slices.Contains(langs, catalogLang)
}

// Select returns the catalogs to search, ordered by priority. Hidden (NSFW)
// catalogs are never returned.
func (s *Service) Select(ctx context.Context, f Filter) ([]Catalog, []string) {
	all, errs := s.List(ctx, false)
	st := s.sourceSettings()
	// A language default orders the catalogs for that language; unlike keys
	// asked for by name, it does not bring back ones switched off.
	preset := false
	if f.Scope != ScopeAll && len(f.Keys) == 0 && f.Lang != "" {
		if p, ok := st.ForLanguage(f.Lang); ok && len(p.Sources) > 0 {
			f.Keys, preset = p.Sources, true
		}
	}
	var langs []string
	switch {
	case f.Lang != "":
		langs = []string{f.Lang}
	case f.Scope != ScopeAll && len(f.Keys) == 0:
		langs = st.DefaultLanguages
	}
	var out []Catalog
	byKey := map[string]Catalog{}
	for _, c := range all {
		if c.Hidden {
			continue
		}
		byKey[c.Key()] = c
		if len(f.Keys) > 0 {
			continue
		}
		if !langMatch(c.Lang, langs) || (f.Scope != ScopeAll && !c.Enabled) {
			continue
		}
		out = append(out, c)
	}
	if len(f.Keys) > 0 {
		for _, key := range f.Keys {
			if c, ok := byKey[key]; ok && (!preset || c.Enabled) {
				out = append(out, c)
			}
		}
		return out, errs
	}
	SortByPriority(out)
	lang := f.Lang
	if lang == "" && len(langs) == 1 {
		lang = langs[0]
	}
	entries := make([]sourcepriority.Entry, 0, len(out))
	for _, c := range out {
		entries = append(entries, sourcepriority.Entry{Key: c.Key(), Priority: c.Priority})
	}
	ranks, err := sourcepriority.Resolve(ctx, s.db, f.RootFolderID, lang, entries)
	if err != nil {
		return nil, append(errs, err.Error())
	}
	sort.SliceStable(out, func(i, j int) bool { return ranks[out[i].Key()] < ranks[out[j].Key()] })
	return out, errs
}

// SortByPriority orders catalogs by priority, then language and name.
func SortByPriority(cs []Catalog) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Priority != cs[j].Priority {
			return cs[i].Priority < cs[j].Priority
		}
		if cs[i].Lang != cs[j].Lang {
			return cs[i].Lang < cs[j].Lang
		}
		return strings.ToLower(cs[i].Name) < strings.ToLower(cs[j].Name)
	})
}

// ErrHidden is returned for catalogs hidden by the NSFW setting.
var ErrHidden = errors.New("this catalog is hidden (NSFW catalogs are hidden in Settings → Sources)")

// Allowed checks that a catalog may be used for browsing and searching.
// Unknown catalogs (e.g. an extension that was uninstalled) are allowed.
func (s *Service) Allowed(ctx context.Context, moduleID int64, sourceID string) error {
	if !s.sourceSettings().HideNSFW {
		return nil
	}
	all, _ := s.List(ctx, false)
	for _, c := range all {
		if c.ModuleID == moduleID && c.ID == sourceID && c.Hidden {
			return ErrHidden
		}
	}
	return nil
}

// Patch changes the preferences of one catalog; nil fields are kept.
type Patch struct {
	Enabled  *bool                 `json:"enabled,omitempty"`
	Priority *int                  `json:"priority,omitempty"`
	Throttle *model.ThrottleConfig `json:"throttle,omitempty"`
	// ClearCooldown ends a running cooldown.
	ClearCooldown bool `json:"clearCooldown,omitempty"`
}

// Update applies patches keyed by "moduleId:sourceId".
func (s *Service) Update(ctx context.Context, patches map[string]Patch) error {
	for key, p := range patches {
		mid, sid, ok := ParseKey(key)
		if !ok {
			return errors.New("invalid catalog key " + key)
		}
		if _, err := s.updatePref(ctx, mid, sid, func(pref *model.CatalogPref) {
			if p.Enabled != nil {
				pref.Enabled = *p.Enabled
			}
			if p.Priority != nil {
				pref.Priority = *p.Priority
			}
			if p.Throttle != nil {
				pref.Throttle = *p.Throttle
			}
			if p.ClearCooldown {
				pref.CooldownUntil, pref.CooldownStrikes, pref.LastThrottle = nil, 0, ""
			}
		}); err != nil {
			return err
		}
		if p.ClearCooldown {
			s.Gov.ClearCooldown(sourcegov.Key{ModuleID: mid, SourceID: sid})
		}
	}
	s.Bump()
	return nil
}

// updatePref loads (or creates) a preference row, applies fn and saves it.
func (s *Service) updatePref(ctx context.Context, moduleID int64, sourceID string, fn func(*model.CatalogPref)) (model.CatalogPref, error) {
	p, ok := s.pref(moduleID, sourceID)
	if !ok {
		p = model.CatalogPref{ModuleID: moduleID, SourceID: sourceID, Enabled: s.defaultOn(moduleID, sourceID), Priority: DefaultPriority}
	}
	fn(&p)
	p.UpdatedAt = time.Now().UTC()
	_, err := s.db.NewInsert().Model(&p).
		On("CONFLICT (module_id, source_id) DO UPDATE").
		Set("enabled = EXCLUDED.enabled").Set("priority = EXCLUDED.priority").Set("throttle = EXCLUDED.throttle").
		Set("cooldown_until = EXCLUDED.cooldown_until").Set("cooldown_strikes = EXCLUDED.cooldown_strikes").
		Set("last_throttle = EXCLUDED.last_throttle").Set("updated_at = EXCLUDED.updated_at").
		Returning("id").Exec(ctx)
	if err != nil {
		return p, err
	}
	s.mu.Lock()
	s.prefs[prefKey{moduleID, sourceID}] = p
	s.mu.Unlock()
	return p, nil
}
