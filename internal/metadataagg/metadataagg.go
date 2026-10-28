// Package metadataagg queries all active metadata modules by priority and
// merges their results field by field, recording provenance and respecting
// user locks. Source details act as the lowest-priority fallback.
package metadataagg

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/Asion001/mangarr/internal/genres"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/titlematch"
)

// Ref points at a series in one metadata module instance.
type Ref struct {
	ModuleID int64  `json:"moduleId"`
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

type Candidate struct {
	metadata.SeriesMetadata
	ModuleID   int64  `json:"moduleId"`
	ModuleName string `json:"moduleName"`
	// Also lists the same series found at other providers.
	Also []Ref `json:"also,omitempty"`
	// foundBy is the provider whose id found it, for FindAtPrimary.
	foundBy string
}

type Aggregator struct {
	mods *modules.Manager
	log  *slog.Logger
	// MapToPrimary says whether a match at a lower provider is looked up at
	// the primary one (the first by priority) too; on when nil.
	MapToPrimary func(ctx context.Context) bool
}

func New(m *modules.Manager, log *slog.Logger) *Aggregator { return &Aggregator{mods: m, log: log} }

// HasProviders reports whether any metadata module is active.
func (a *Aggregator) HasProviders() bool {
	return len(modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata)) > 0
}

// Search queries every active module in parallel and merges duplicates.
func (a *Aggregator) Search(ctx context.Context, q string, limit int) ([]Candidate, []error) {
	return a.SearchLanguage(ctx, q, "", limit)
}

// SearchLanguage asks providers that support localized searches to return
// titles in language. Other providers remain part of the merged result set.
func (a *Aggregator) SearchLanguage(ctx context.Context, q, language string, limit int) ([]Candidate, []error) {
	mods := modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata)
	results := make([][]Candidate, len(mods))
	errs := make([]error, len(mods))
	var wg sync.WaitGroup
	for i, m := range mods {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var list []metadata.SeriesMetadata
			var err error
			if localized, ok := m.Instance.(metadata.LanguageSearcher); ok && language != "" {
				list, err = localized.SearchLanguage(ctx, q, language, limit)
			} else {
				list, err = m.Instance.Search(ctx, q, limit)
			}
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", m.Def.Name, err)
				return
			}
			for _, md := range list {
				results[i] = append(results[i], Candidate{SeriesMetadata: md, ModuleID: m.Def.ID, ModuleName: m.Def.Name})
			}
		}()
	}
	wg.Wait()
	var merged []Candidate
	for _, group := range results { // priority order
		for _, c := range group {
			if idx := findSame(merged, c); idx >= 0 {
				merged[idx].Also = append(merged[idx].Also, Ref{ModuleID: c.ModuleID, Provider: c.Provider, ID: c.ID})
				continue
			}
			merged = append(merged, c)
		}
	}
	var outErrs []error
	for _, e := range errs {
		if e != nil {
			outErrs = append(outErrs, e)
		}
	}
	return merged, outErrs
}

// ModuleIDs lists the active metadata modules, highest priority first.
func (a *Aggregator) ModuleIDs() []int64 {
	var ids []int64
	for _, m := range modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata) {
		ids = append(ids, m.Def.ID)
	}
	return ids
}

// SearchModule asks one metadata module, so a caller can go down the
// modules by priority and stop at the first that knows the title.
func (a *Aggregator) SearchModule(ctx context.Context, moduleID int64, q string, limit int) ([]Candidate, error) {
	for _, m := range modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata) {
		if m.Def.ID != moduleID {
			continue
		}
		list, err := m.Instance.Search(ctx, q, limit)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Def.Name, err)
		}
		out := make([]Candidate, 0, len(list))
		for _, md := range list {
			out = append(out, Candidate{SeriesMetadata: md, ModuleID: m.Def.ID, ModuleName: m.Def.Name})
		}
		return out, nil
	}
	return nil, errors.New("metadata module not found or disabled")
}

func findSame(list []Candidate, c Candidate) int {
	for i, x := range list {
		if x.Provider == c.Provider {
			continue
		}
		for k, v := range c.ExternalIDs {
			if v != "" && x.ExternalIDs[k] == v {
				return i
			}
		}
	}
	return -1
}

// Resolved is the merged metadata plus provenance.
type Resolved struct {
	Metadata   metadata.SeriesMetadata
	Provenance map[string]string
	Refs       []Ref
	// Missing are providers of known refs that could not be reached: what
	// they supplied before stays, rather than going to a lower provider.
	Missing []string
}

// Resolve fetches the primary series and enriches it from the other modules.
func (a *Aggregator) Resolve(ctx context.Context, primary Ref, fallback *source.MangaDetails) (*Resolved, error) {
	return a.ResolveLanguage(ctx, primary, fallback, "")
}

// ResolveLanguage preserves the requested edition language when its primary
// metadata provider supports localized records.
func (a *Aggregator) ResolveLanguage(ctx context.Context, primary Ref, fallback *source.MangaDetails, language string) (*Resolved, error) {
	mods := modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata)
	var parts []metadata.SeriesMetadata
	var refs []Ref
	var first *metadata.SeriesMetadata
	for _, m := range mods {
		if m.Def.ID == primary.ModuleID {
			var md *metadata.SeriesMetadata
			var err error
			if localized, ok := m.Instance.(metadata.LanguageGetter); ok && language != "" {
				md, err = localized.GetLanguage(ctx, primary.ID, language)
			} else {
				md, err = m.Instance.Get(ctx, primary.ID)
			}
			if err != nil {
				return nil, err
			}
			first = md
			break
		}
	}
	if first == nil {
		return nil, errors.New("metadata module not found or disabled")
	}
	// the others fill in by module priority, like a refresh does, so an add
	// from a lower provider's match ends up as the refresh would leave it
	refs = append(refs, Ref{ModuleID: primary.ModuleID, Provider: first.Provider, ID: first.ID})
	for _, m := range mods {
		if m.Def.ID == primary.ModuleID {
			parts = append(parts, *first)
			continue
		}
		md := a.enrichFrom(ctx, m, *first)
		if md != nil {
			parts = append(parts, *md)
			refs = append(refs, Ref{ModuleID: m.Def.ID, Provider: md.Provider, ID: md.ID})
		}
	}
	if top := mods[0]; top.Def.ID != primary.ModuleID && a.mapToPrimary(ctx) && !slices.ContainsFunc(refs, func(r Ref) bool { return r.ModuleID == top.Def.ID }) {
		// what the other providers found tells the primary one more than
		// the match's own name: their ids and every name they know
		if c, _, _ := a.findAt(ctx, top, known(parts)); c != nil {
			if md, err := top.Instance.Get(ctx, c.ID); err == nil {
				parts = append([]metadata.SeriesMetadata{*md}, parts...)
				refs = append(refs, Ref{ModuleID: top.Def.ID, Provider: md.Provider, ID: md.ID})
			}
		}
	}
	if fallback != nil {
		parts = append(parts, FromSource(fallback))
	}
	merged, prov := Merge(parts)
	if strings.TrimSpace(first.Title) != "" && merged.Title != first.Title {
		// the edition keeps the name its match gave, in its language
		merged.AltTitles = slices.DeleteFunc(merged.AltTitles, func(t string) bool { return t == first.Title })
		merged.AltTitles = append(merged.AltTitles, merged.Title)
		merged.Title, prov["title"] = first.Title, first.Provider
	}
	return &Resolved{Metadata: merged, Provenance: prov, Refs: refs}, nil
}

func (a *Aggregator) mapToPrimary(ctx context.Context) bool {
	return a.MapToPrimary == nil || a.MapToPrimary(ctx)
}

// Primary is the primary metadata module (the first by priority), or nil.
func (a *Aggregator) Primary() *model.ProviderDefinition {
	if mods := modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata); len(mods) > 0 {
		def := mods[0].Def
		return &def
	}
	return nil
}

// Known is what a title's providers say about it, to find it at another.
type Known struct {
	IDs    map[string]string
	Names  []string
	Year   int
	Format string
}

func known(parts []metadata.SeriesMetadata) Known {
	k := Known{IDs: map[string]string{}}
	for _, p := range parts {
		for key, v := range p.ExternalIDs {
			if v != "" && k.IDs[key] == "" {
				k.IDs[key] = v
			}
		}
		if p.Provider != "" && p.Provider != "source" && p.ID != "" && k.IDs[p.Provider] == "" {
			k.IDs[p.Provider] = p.ID
		}
		k.Names = append(append(k.Names, p.Title), p.AltTitles...)
		for _, t := range p.Titles {
			k.Names = append(k.Names, t)
		}
		if k.Year == 0 {
			k.Year = p.Year
		}
		if k.Format == "" {
			k.Format = p.Format
		}
	}
	return k
}

// How a title was found at the primary provider.
const (
	FoundByID   = "id"
	FoundByName = "name"
)

// NameThreshold is the name score a match by name needs.
const NameThreshold = 0.92

// FindAtPrimary looks a title up at the primary provider: by the ids the
// other providers give first, then by its names. how is "id" (with the id's
// provider) or "name" (with the score).
func (a *Aggregator) FindAtPrimary(ctx context.Context, k Known) (c *Candidate, how string, detail string, score float64) {
	mods := modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata)
	if len(mods) == 0 {
		return nil, "", "", 0
	}
	c, how, score = a.findAt(ctx, mods[0], k)
	if c != nil && how == FoundByID {
		detail = c.foundBy
	}
	return c, how, detail, score
}

func (a *Aggregator) findAt(ctx context.Context, m modules.Typed[metadata.Module], k Known) (*Candidate, string, float64) {
	if el, ok := m.Instance.(metadata.ExternalLookup); ok {
		keys := make([]string, 0, len(k.IDs))
		for key := range k.IDs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if md, err := el.LookupExternal(ctx, key, k.IDs[key]); err == nil && md != nil && md.ID != "" {
				return &Candidate{SeriesMetadata: *md, ModuleID: m.Def.ID, ModuleName: m.Def.Name, foundBy: key}, FoundByID, 1
			}
		}
	}
	var names []string
	for _, n := range k.Names {
		if n = strings.TrimSpace(n); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	var best *Candidate
	score := 0.0
	for _, q := range names[:min(len(names), 3)] {
		list, err := m.Instance.Search(ctx, q, 5)
		if err != nil {
			continue
		}
		for _, md := range list {
			if md.Format != "" && k.Format != "" && metadata.IsNovel(md.Format) != metadata.IsNovel(k.Format) {
				continue // the novel of a manga goes by the same name
			}
			if k.Year > 0 && md.Year > 0 && abs(k.Year-md.Year) > 1 {
				continue
			}
			cn := append([]string{md.Title}, md.AltTitles...)
			for _, t := range md.Titles {
				cn = append(cn, t)
			}
			for _, n := range names {
				if sc := titlematch.Best(cn, n); sc > score {
					best, score = &Candidate{SeriesMetadata: md, ModuleID: m.Def.ID, ModuleName: m.Def.Name}, sc
				}
			}
		}
		if score >= NameThreshold {
			return best, FoundByName, score
		}
	}
	return nil, "", 0
}

// RefFor picks the first active metadata module that knows one of these
// external IDs, so another language edition can be resolved from them.
func (a *Aggregator) RefFor(ids map[string]string) *Ref {
	for _, m := range modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata) {
		if id := ids[m.Def.Implementation]; id != "" {
			return &Ref{ModuleID: m.Def.ID, Provider: m.Def.Implementation, ID: id}
		}
	}
	return nil
}

// ResolveRefs re-fetches known refs (used by metadata refresh).
func (a *Aggregator) ResolveRefs(ctx context.Context, refs []Ref, fallback *source.MangaDetails) (*Resolved, error) {
	var parts []metadata.SeriesMetadata
	var got []Ref
	var missing []string
	for _, m := range modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata) {
		for _, r := range refs {
			if r.Provider != m.Def.Implementation {
				continue
			}
			md, err := m.Instance.Get(ctx, r.ID)
			if err != nil {
				a.log.Warn("metadata refresh failed", "provider", r.Provider, "id", r.ID, "err", err)
				missing = append(missing, r.Provider)
				continue
			}
			parts = append(parts, *md)
			got = append(got, Ref{ModuleID: m.Def.ID, Provider: md.Provider, ID: md.ID})
			break
		}
	}
	if len(parts) == 0 && fallback == nil {
		return nil, errors.New("no metadata available")
	}
	if fallback != nil {
		parts = append(parts, FromSource(fallback))
	}
	merged, prov := Merge(parts)
	return &Resolved{Metadata: merged, Provenance: prov, Refs: got, Missing: missing}, nil
}

func (a *Aggregator) enrichFrom(ctx context.Context, m modules.Typed[metadata.Module], primary metadata.SeriesMetadata) *metadata.SeriesMetadata {
	if el, ok := m.Instance.(metadata.ExternalLookup); ok {
		keys := make([]string, 0, len(primary.ExternalIDs))
		for k := range primary.ExternalIDs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if md, err := el.LookupExternal(ctx, k, primary.ExternalIDs[k]); err == nil && md != nil {
				return md
			}
		}
	}
	list, err := m.Instance.Search(ctx, primary.Title, 5)
	if err != nil {
		return nil
	}
	want := map[string]bool{Normalize(primary.Title): true}
	for _, t := range primary.AltTitles {
		want[Normalize(t)] = true
	}
	for _, c := range list {
		if !want[Normalize(c.Title)] {
			continue
		}
		if primary.Year > 0 && c.Year > 0 && abs(primary.Year-c.Year) > 1 {
			continue
		}
		if c.Format != "" && metadata.IsNovel(c.Format) != metadata.IsNovel(primary.Format) {
			continue // the novel of a manga goes by the same name
		}
		if md, err := m.Instance.Get(ctx, c.ID); err == nil {
			return md
		}
	}
	return nil
}

var nonAlnum = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// Normalize is used for fuzzy title matching.
func Normalize(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// FromSource converts source details to metadata (provider "source").
func FromSource(d *source.MangaDetails) metadata.SeriesMetadata {
	md := metadata.SeriesMetadata{Provider: "source", Title: d.Title, Description: d.Description, Status: d.Status, URL: d.WebURL}
	if d.Author != "" {
		md.Authors = splitPeople(d.Author)
	}
	if d.Artist != "" {
		md.Artists = splitPeople(d.Artist)
	}
	for _, g := range d.Genres {
		// sources mix technical labels into genres ("Content rating: Suggestive")
		if strings.Contains(g, ":") {
			continue
		}
		md.Genres = append(md.Genres, strings.TrimSpace(g))
	}
	if md.Status == source.StatusUnknown {
		md.Status = ""
	}
	return md
}

func splitPeople(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '&' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Merge combines parts (highest priority first): scalars take the first
// non-empty value, lists are unioned. It returns the merged metadata and a
// field -> provider provenance map.
func Merge(parts []metadata.SeriesMetadata) (metadata.SeriesMetadata, map[string]string) {
	out := metadata.SeriesMetadata{Links: map[string]string{}, ExternalIDs: map[string]string{}, Titles: map[string]string{}}
	prov := map[string]string{}
	str := func(field string, dst *string, v, p string) {
		if *dst == "" && strings.TrimSpace(v) != "" {
			*dst = v
			prov[field] = p
		}
	}
	list := func(field string, dst *[]string, v []string, p string) {
		seen := map[string]bool{}
		for _, x := range *dst {
			seen[strings.ToLower(x)] = true
		}
		added := false
		for _, x := range v {
			x = strings.TrimSpace(x)
			if x != "" && !seen[strings.ToLower(x)] {
				seen[strings.ToLower(x)] = true
				*dst = append(*dst, x)
				added = true
			}
		}
		if added && prov[field] == "" {
			prov[field] = p
		}
	}
	for _, p := range parts {
		if out.Adaptations == nil && p.Adaptations != nil {
			out.Adaptations = p.Adaptations
			prov["adaptations"] = p.Provider
		}
		str("title", &out.Title, p.Title, p.Provider)
		str("description", &out.Description, p.Description, p.Provider)
		str("status", &out.Status, p.Status, p.Provider)
		str("publisher", &out.Publisher, p.Publisher, p.Provider)
		str("coverUrl", &out.CoverURL, p.CoverURL, p.Provider)
		str("format", &out.Format, p.Format, p.Provider)
		str("country", &out.Country, p.Country, p.Provider)
		if out.Year == 0 && p.Year > 0 {
			out.Year, prov["year"] = p.Year, p.Provider
		}
		if out.TotalChapters == 0 && p.TotalChapters > 0 {
			out.TotalChapters, prov["totalChapters"] = p.TotalChapters, p.Provider
		}
		if p.Adult {
			out.Adult = true
		}
		list("authors", &out.Authors, p.Authors, p.Provider)
		list("artists", &out.Artists, p.Artists, p.Provider)
		list("genres", &out.Genres, genres.Normalize(p.Genres), p.Provider)
		list("tags", &out.Tags, genres.Normalize(p.Tags), p.Provider)
		alt := append([]string{}, p.AltTitles...)
		if p.Title != "" && p.Title != out.Title {
			alt = append(alt, p.Title)
		}
		list("altTitles", &out.AltTitles, alt, p.Provider)
		for k, v := range p.Links {
			if _, ok := out.Links[k]; !ok && v != "" {
				out.Links[k] = v
			}
		}
		for k, v := range p.Titles {
			if _, ok := out.Titles[k]; !ok && strings.TrimSpace(v) != "" {
				out.Titles[k] = v
			}
		}
		for k, v := range p.ExternalIDs {
			if _, ok := out.ExternalIDs[k]; !ok && v != "" {
				out.ExternalIDs[k] = v
			}
		}
		if p.Provider != "source" && p.ID != "" {
			if _, ok := out.ExternalIDs[p.Provider]; !ok {
				out.ExternalIDs[p.Provider] = p.ID
			}
		}
	}
	return out, prov
}

// Apply writes resolved metadata into a series, skipping locked fields.
// It returns true when anything changed.
func Apply(s *model.Series, r *Resolved) bool {
	md := r.Metadata
	cur := &s.Metadata
	changed := false
	// kept: the field is the user's, or came from a provider that is down
	kept := func(field string) bool {
		return cur.Locked(field) || slices.Contains(r.Missing, cur.Provenance[field])
	}
	setStr := func(field string, dst *string, v string) {
		if kept(field) || v == "" || *dst == v {
			return
		}
		*dst = v
		changed = true
	}
	setList := func(field string, dst *[]string, v []string) {
		if kept(field) || len(v) == 0 || equalStrings(*dst, v) {
			return
		}
		*dst = v
		changed = true
	}
	setStr("title", &s.Title, md.Title)
	if md.Status != "" && !kept("status") && s.Status != md.Status {
		s.Status = md.Status
		changed = true
	}
	setStr("description", &cur.Description, md.Description)
	setStr("publisher", &cur.Publisher, md.Publisher)
	setStr("coverUrl", &cur.CoverURL, md.CoverURL)
	setStr("format", &cur.Format, md.Format)
	setList("authors", &cur.Authors, md.Authors)
	setList("artists", &cur.Artists, md.Artists)
	setList("genres", &cur.Genres, md.Genres)
	setList("tags", &cur.Tags, md.Tags)
	setList("altTitles", &cur.AltTitles, md.AltTitles)
	if !kept("year") && md.Year > 0 && cur.Year != md.Year {
		cur.Year, changed = md.Year, true
	}
	if !kept("totalChapters") && md.TotalChapters > 0 && cur.TotalChapters != md.TotalChapters {
		cur.TotalChapters, changed = md.TotalChapters, true
	}
	if rating := ageRating(md); rating != "" && !cur.Locked("ageRating") && cur.AgeRating != rating {
		cur.AgeRating, changed = rating, true
	}
	// A fetched empty list clears removed relations; an unavailable provider
	// leaves the last successful result intact.
	if md.Adaptations != nil || cur.ExternalIDs["anilist"] == "" {
		if len(cur.Adaptations) != len(md.Adaptations) || (len(md.Adaptations) > 0 && !reflect.DeepEqual(cur.Adaptations, md.Adaptations)) {
			cur.Adaptations, changed = md.Adaptations, true
		}
		if md.Adaptations == nil {
			delete(cur.Provenance, "adaptations")
		}
	}
	for k, v := range md.Titles {
		if cur.Titles == nil {
			cur.Titles = map[string]string{}
		}
		if cur.Titles[k] != v && (cur.Titles[k] == "" || len(r.Missing) == 0) {
			cur.Titles[k], changed = v, true
		}
	}
	if cur.Links == nil {
		cur.Links = map[string]string{}
	}
	for k, v := range md.Links {
		if cur.Links[k] != v {
			cur.Links[k], changed = v, true
		}
	}
	if cur.ExternalIDs == nil {
		cur.ExternalIDs = map[string]string{}
	}
	for k, v := range md.ExternalIDs {
		if cur.ExternalIDs[k] != v {
			cur.ExternalIDs[k], changed = v, true
		}
	}
	if cur.Provenance == nil {
		cur.Provenance = map[string]string{}
	}
	for k, v := range r.Provenance {
		if !kept(k) {
			cur.Provenance[k] = v
		}
	}
	// Reading direction default from format (only when the user never set one).
	if !cur.Locked("readingDirection") {
		switch md.Format {
		case "manhwa", "manhua":
			if s.ReadingDirection == "rtl" || s.ReadingDirection == "" {
				s.ReadingDirection, changed = "webtoon", true
			}
		}
	}
	return changed
}

func ageRating(md metadata.SeriesMetadata) string {
	if md.Adult {
		return "Adults Only 18+"
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
