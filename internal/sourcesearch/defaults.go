package sourcesearch

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/sourcegov"
	"github.com/Asion001/mangarr/internal/titlematch"
)

// DefaultsInput asks for a title in each language's default sources.
type DefaultsInput struct {
	Query  string   `json:"query" minLength:"1"`
	Titles []string `json:"titles,omitempty"`
	// Langs are the languages to search; empty means the search languages.
	Langs []string `json:"langs,omitempty"`
	// Exclude skips these catalogs (moduleId:sourceId).
	Exclude []string `json:"exclude,omitempty"`
}

// DefaultsSource is what one source of a language's list found.
type DefaultsSource struct {
	Key        string `json:"key"`
	SourceName string `json:"sourceName"`
	// Match is the best confident result with chapters (nil: not found).
	Match *QuickCandidate `json:"match,omitempty"`
	// Empty is a confident result that has no chapters (e.g. licensed and
	// removed); it is never made the primary source.
	Empty *QuickCandidate `json:"empty,omitempty"`
	// Others are further confident results at the same site.
	Others []QuickCandidate `json:"others"`
	Error  string           `json:"error,omitempty"`
	// Searched is false when the time ran out before this source answered.
	Searched bool `json:"searched"`
}

// DefaultsEdition is one language's result.
type DefaultsEdition struct {
	Lang string `json:"lang"`
	// FromDefaults is true when the language has a source list; otherwise
	// its catalogs were searched one by one until the first confident match.
	FromDefaults bool             `json:"fromDefaults"`
	Sources      []DefaultsSource `json:"sources"`
}

// DefaultsResult lists one edition per searched language.
type DefaultsResult struct {
	Editions  []DefaultsEdition `json:"editions"`
	Threshold float64           `json:"threshold"`
	// NoLanguages is true when no language was given and no search language
	// is set: the caller should ask which language to add.
	NoLanguages bool  `json:"noLanguages,omitempty"`
	Generation  int64 `json:"generation"`
}

// Defaults searches every source of each language's default list (not just
// until the first match), so the first found becomes the primary source and
// the rest fallbacks. A language without a list falls back to Quick.
func (s *Service) Defaults(ctx context.Context, in DefaultsInput) (*DefaultsResult, error) {
	st, err := s.Settings.Sources(ctx)
	if err != nil {
		return nil, err
	}
	threshold := st.QuickSearch.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.88
	}
	res := &DefaultsResult{Editions: []DefaultsEdition{}, Threshold: threshold, Generation: s.Catalogs.Generation()}
	langs := normLangs(in.Langs)
	if len(langs) == 0 {
		langs = normLangs(st.DefaultLanguages)
	}
	if len(langs) == 0 {
		res.NoLanguages = true
		return res, nil
	}
	deadline := time.Now().Add(time.Duration(max(st.QuickSearch.BudgetSeconds, 5)) * time.Second)
	dctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	all, _ := s.Catalogs.List(dctx, false)
	byKey := map[string]catalogs.Catalog{}
	for _, c := range all {
		byKey[c.Key()] = c
	}
	titles := append([]string{in.Query}, in.Titles...)
	res.Editions = make([]DefaultsEdition, len(langs))
	var wg sync.WaitGroup
	for i, lang := range langs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			def, ok := st.ForLanguage(lang)
			var keys []string
			if ok {
				for _, k := range def.Sources {
					if c, found := byKey[k]; found && !c.Hidden && c.Enabled && !slices.Contains(in.Exclude, k) {
						keys = append(keys, k)
					}
				}
			}
			if len(keys) == 0 {
				res.Editions[i] = s.quickEdition(dctx, in, lang)
				return
			}
			ed := DefaultsEdition{Lang: lang, FromDefaults: true, Sources: make([]DefaultsSource, len(keys))}
			var inner sync.WaitGroup
			for j, k := range keys {
				inner.Add(1)
				go func() {
					defer inner.Done()
					ed.Sources[j] = s.searchOne(dctx, byKey[k], in.Query, titles, threshold)
				}()
			}
			inner.Wait()
			res.Editions[i] = ed
		}()
	}
	wg.Wait()
	return res, nil
}

// searchOne searches one catalog and picks its best confident result with
// chapters; up to three confident results get their chapter list checked.
func (s *Service) searchOne(ctx context.Context, c catalogs.Catalog, query string, titles []string, threshold float64) DefaultsSource {
	out := DefaultsSource{Key: c.Key(), SourceName: c.DisplayName, Others: []QuickCandidate{}}
	if c.CooldownUntil != nil {
		out.Error = (&sourcegov.ErrCoolingDown{Until: *c.CooldownUntil, Reason: c.CooldownReason}).Error()
		return out
	}
	page, _, err := s.Search(ctx, c.ModuleID, c.ID, query, 1)
	if err != nil {
		out.Error = err.Error()
		out.Searched = ctx.Err() == nil
		return out
	}
	out.Searched = true
	var confident []QuickCandidate
	for _, m := range page.Mangas {
		if score := titlematch.Best(titles, m.Title); score >= threshold {
			confident = append(confident, QuickCandidate{ModuleID: c.ModuleID, SourceID: c.ID, SourceName: c.DisplayName, Lang: c.Lang, Manga: m, Score: score})
		}
	}
	sort.SliceStable(confident, func(i, j int) bool { return confident[i].Score > confident[j].Score })
	for n := range confident {
		cand := &confident[n]
		if n < 3 && out.Match == nil {
			dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if d, cached, err := s.Details(dctx, c.ModuleID, cand.Manga.MangaRef, false); err == nil {
				cand.Chapters = Summarize(d, cached)
			}
			cancel()
			if cand.Chapters != nil && cand.Chapters.Count == 0 {
				if out.Empty == nil {
					e := *cand
					out.Empty = &e
				}
				continue
			}
			m := *cand
			out.Match = &m
			continue
		}
		out.Others = append(out.Others, *cand)
	}
	return out
}

// quickEdition is a language without a source list: its catalogs one by one
// until the first confident match, as Quick does.
func (s *Service) quickEdition(ctx context.Context, in DefaultsInput, lang string) DefaultsEdition {
	ed := DefaultsEdition{Lang: lang, Sources: []DefaultsSource{}}
	q, err := s.Quick(ctx, QuickSearchInput{Query: in.Query, Titles: in.Titles, Lang: lang, Exclude: in.Exclude}, QuickOptions{})
	if err != nil {
		return ed
	}
	for _, sr := range q.Searched {
		src := DefaultsSource{Key: sr.Key, SourceName: sr.SourceName, Error: sr.Error, Searched: true, Others: []QuickCandidate{}}
		if q.Match != nil && catalogs.Key(q.Match.ModuleID, q.Match.SourceID) == sr.Key {
			m := *q.Match
			src.Match = &m
		}
		ed.Sources = append(ed.Sources, src)
	}
	return ed
}

func normLangs(in []string) []string {
	var out []string
	for _, l := range in {
		l = strings.ToLower(strings.TrimSpace(l))
		if l != "" && l != "all" && l != "multi" && !slices.Contains(out, l) {
			out = append(out, l)
		}
	}
	return out
}
