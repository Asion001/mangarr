// Package metadataagg queries all active metadata modules by priority and
// merges their results field by field, recording provenance and respecting
// user locks. Source details act as the lowest-priority fallback.
package metadataagg

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/Asion001/mangarr/internal/genres"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/source"
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
}

type Aggregator struct {
	mods *modules.Manager
	log  *slog.Logger
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
				errs[i] = errors.New(m.Def.Name + ": " + err.Error())
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
	parts = append(parts, *first)
	refs = append(refs, Ref{ModuleID: primary.ModuleID, Provider: first.Provider, ID: first.ID})
	for _, m := range mods {
		if m.Def.ID == primary.ModuleID {
			continue
		}
		md := a.enrichFrom(ctx, m, *first)
		if md != nil {
			parts = append(parts, *md)
			refs = append(refs, Ref{ModuleID: m.Def.ID, Provider: md.Provider, ID: md.ID})
		}
	}
	if fallback != nil {
		parts = append(parts, FromSource(fallback))
	}
	merged, prov := Merge(parts)
	return &Resolved{Metadata: merged, Provenance: prov, Refs: refs}, nil
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
	for _, m := range modules.ActiveAs[metadata.Module](a.mods, modules.KindMetadata) {
		for _, r := range refs {
			if r.Provider != m.Def.Implementation {
				continue
			}
			md, err := m.Instance.Get(ctx, r.ID)
			if err != nil {
				a.log.Warn("metadata refresh failed", "provider", r.Provider, "id", r.ID, "err", err)
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
	return &Resolved{Metadata: merged, Provenance: prov, Refs: got}, nil
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
	out := metadata.SeriesMetadata{Links: map[string]string{}, ExternalIDs: map[string]string{}}
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
	setStr := func(field string, dst *string, v string) {
		if cur.Locked(field) || v == "" || *dst == v {
			return
		}
		*dst = v
		changed = true
	}
	setList := func(field string, dst *[]string, v []string) {
		if cur.Locked(field) || len(v) == 0 || equalStrings(*dst, v) {
			return
		}
		*dst = v
		changed = true
	}
	setStr("title", &s.Title, md.Title)
	if md.Status != "" && !cur.Locked("status") && s.Status != md.Status {
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
	if !cur.Locked("year") && md.Year > 0 && cur.Year != md.Year {
		cur.Year, changed = md.Year, true
	}
	if md.TotalChapters > 0 && cur.TotalChapters != md.TotalChapters {
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
		if !cur.Locked(k) {
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
