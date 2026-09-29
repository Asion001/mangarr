package imports

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/sourcesearch"
	"github.com/Asion001/mangarr/internal/titlematch"
)

// Scores used when matching by title.
const (
	// MetadataThreshold is the title score that links metadata by itself.
	MetadataThreshold = 0.92
	// ReviewThreshold is the lowest score still offered as a suggestion.
	ReviewThreshold = 0.5
)

type linkKey struct {
	moduleID int64
	sourceID string
	url      string
}

type extRef struct {
	moduleID int64
	ext      source.Extension
}

// mapper holds what mapping every entry needs, loaded once per run.
type mapper struct {
	s          *Service
	format     string
	opts       model.ImportOptions
	threshold  float64
	langs      []string
	byID       map[string][]catalogs.Catalog
	byName     map[string][]catalogs.Catalog
	exts       []extRef
	links      map[linkKey]int64
	byExternal map[string]int64
	meta       []modules.Typed[metadata.Module]
}

// norm reduces a catalog or extension name for comparisons.
func norm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "tachiyomi:")
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (s *Service) newMapper(ctx context.Context, imp *model.Import) (*mapper, error) {
	st, err := s.Settings.Sources(ctx)
	if err != nil {
		return nil, err
	}
	m := &mapper{s: s, format: imp.Format, opts: imp.Options, threshold: st.QuickSearch.Threshold, langs: st.DefaultLanguages,
		byID: map[string][]catalogs.Catalog{}, byName: map[string][]catalogs.Catalog{}, links: map[linkKey]int64{}, byExternal: map[string]int64{}}
	if m.threshold <= 0 || m.threshold > 1 {
		m.threshold = 0.88
	}
	list, _ := s.Catalogs.List(ctx, true)
	catalogs.SortByPriority(list)
	for _, c := range list {
		m.byID[c.ID] = append(m.byID[c.ID], c)
		m.byName[norm(c.Name)] = append(m.byName[norm(c.Name)], c)
	}
	for _, em := range modules.ActiveAs[source.ExtensionManager](s.Mods, modules.KindSource) {
		exts, err := em.Instance.Extensions(ctx, false)
		if err != nil {
			s.Log.Warn("list extensions for import", "module", em.Def.Name, "err", err)
			continue
		}
		for _, e := range exts {
			m.exts = append(m.exts, extRef{moduleID: em.Def.ID, ext: e})
		}
	}
	var links []model.SeriesSource
	if err := s.DB.NewSelect().Model(&links).Column("series_id", "module_id", "source_id", "manga_url").Scan(ctx); err != nil {
		return nil, err
	}
	for _, l := range links {
		m.links[linkKey{l.ModuleID, l.SourceID, l.MangaURL}] = l.SeriesID
	}
	var all []model.Series
	if err := s.DB.NewSelect().Model(&all).Column("id", "metadata").Where("preview = ?", false).Scan(ctx); err != nil {
		return nil, err
	}
	for _, ser := range all {
		for k, v := range ser.Metadata.ExternalIDs {
			if v != "" {
				m.byExternal[k+":"+v] = ser.ID
			}
		}
	}
	m.meta = modules.ActiveAs[metadata.Module](s.Mods, modules.KindMetadata)
	return m, nil
}

// Map matches pending entries (or the given ones, again) to catalogs and
// metadata. progress is called after each entry.
func (s *Service) Map(ctx context.Context, id int64, entryIDs []int64, progress func(done, total int)) error {
	unlock, err := s.lock(id)
	if err != nil {
		return err
	}
	defer unlock()
	imp, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	var entries []model.ImportEntry
	q := s.DB.NewSelect().Model(&entries).Where("import_id = ?", id).Order("position")
	if len(entryIDs) > 0 {
		q = q.Where("id IN (?) AND state <> ?", bun.In(entryIDs), model.EntryImported)
	} else {
		q = q.Where("state = ?", model.EntryPending)
	}
	if err := q.Scan(ctx); err != nil {
		return err
	}
	s.setStatus(ctx, imp, model.ImportMapping, fmt.Sprintf("matching 0 of %d", len(entries)), "")
	m, err := s.newMapper(ctx, imp)
	if err != nil {
		s.setStatus(ctx, imp, model.ImportReview, "", err.Error())
		return err
	}
	last := time.Now()
	for i := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := &entries[i]
		m.mapEntry(ctx, e)
		if err := s.saveEntry(ctx, e); err != nil {
			return err
		}
		if progress != nil {
			progress(i+1, len(entries))
		}
		if time.Since(last) > 2*time.Second || i == len(entries)-1 {
			last = time.Now()
			imp.Progress = fmt.Sprintf("matching %d of %d", i+1, len(entries))
			s.setStatus(ctx, imp, model.ImportMapping, imp.Progress, "")
		}
	}
	s.setStatus(ctx, imp, model.ImportReview, "", "")
	return nil
}

func (m *mapper) mapEntry(ctx context.Context, e *model.ImportEntry) {
	d := e.Data
	manual := e.Source != nil && e.Source.How == model.MatchManual
	if !manual {
		e.Source = nil
	}
	if e.Metadata != nil && e.Metadata.How != model.MatchManual {
		e.Metadata = nil
	}
	e.Extension, e.Message, e.SeriesID = nil, "", nil
	if !manual {
		switch m.format {
		case backupimport.FormatAidoku:
			m.mapAidoku(ctx, e)
		default:
			m.mapMihon(e)
		}
		if e.Source == nil && e.Extension == nil && m.opts.FindByTitle {
			m.findByTitle(ctx, e)
		}
	}

	if e.Source != nil {
		if id, ok := m.links[linkKey{e.Source.ModuleID, e.Source.SourceID, e.Source.URL}]; ok {
			e.SeriesID = &id
		}
	}
	for tracker, id := range d.Trackers {
		if e.SeriesID == nil {
			if sid, ok := m.byExternal[tracker+":"+id]; ok {
				e.SeriesID = &sid
			}
		}
	}
	if e.SeriesID == nil && m.opts.MatchMetadata && e.Metadata == nil {
		e.Metadata = m.matchMetadata(ctx, d)
	}
	if e.SeriesID == nil && e.Metadata != nil {
		if sid, ok := m.byExternal[e.Metadata.Provider+":"+e.Metadata.ID]; ok {
			e.SeriesID = &sid
		}
	}

	switch {
	case e.SeriesID != nil:
		e.State, e.Message = model.EntryLibrary, "already in the library: the source and read chapters are merged"
	case e.Source != nil && (e.Source.How != model.MatchTitle || e.Source.Score >= m.threshold):
		e.State = model.EntryReady
	case e.Source != nil:
		e.State = model.EntryReview
		e.Message = fmt.Sprintf("best title match is %.0f%%; check it or pick another", e.Source.Score*100)
	case e.Extension != nil:
		e.State = model.EntryExtension
		e.Message = "install the " + e.Extension.Name + " extension"
	default:
		e.State = model.EntryReview
		if e.Message == "" {
			e.Message = "no matching manga found; pick a source"
		}
	}
	e.Selected = selectedByDefault(d, m.opts) && (e.State == model.EntryReady || e.State == model.EntryLibrary)
}

func (m *mapper) source(c catalogs.Catalog, d backupimport.BackupManga, url, how string) *model.ImportSource {
	return &model.ImportSource{ModuleID: c.ModuleID, SourceID: c.ID, SourceName: firstNonEmpty(c.DisplayName, c.Name), Lang: c.Lang,
		URL: url, Title: d.Title, ThumbnailURL: d.ThumbnailURL, How: how}
}

// mapMihon uses the exact catalog id (Keiyoushi ids are the same everywhere).
func (m *mapper) mapMihon(e *model.ImportEntry) {
	d := e.Data
	if cs := m.byID[d.SourceID]; len(cs) > 0 {
		e.Source = m.source(cs[0], d, d.URL, model.MatchExact)
		return
	}
	if ext := m.findExtension(d.SourceName, ""); ext != nil {
		e.Extension = ext
		return
	}
	e.Message = "no catalog or extension for " + firstNonEmpty(d.SourceName, "source "+d.SourceID)
}

// aidokuRule converts an Aidoku source's manga key to a Keiyoushi url.
type aidokuRule struct {
	names []string
	url   func(backupimport.BackupManga) string
	// exact rules need no check at the catalog
	exact bool
}

var aidokuRules = map[string]aidokuRule{
	"multi.mangadex": {names: []string{"MangaDex"}, url: func(e backupimport.BackupManga) string { return "/manga/" + e.URL }, exact: true},
}

// pathOf returns the path (and query) of a web url.
func pathOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	p := u.EscapedPath()
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

func (m *mapper) aidokuRule(sourceID string) (aidokuRule, string) {
	lang, name, found := strings.Cut(sourceID, ".")
	if !found {
		name, lang = sourceID, ""
	}
	if r, ok := aidokuRules[sourceID]; ok {
		return r, lang
	}
	return aidokuRule{names: []string{name}, url: func(e backupimport.BackupManga) string {
		if strings.HasPrefix(e.URL, "/") {
			return e.URL
		}
		return pathOf(e.WebURL)
	}}, lang
}

// entryLang picks the language of a multi-language source.
func (m *mapper) entryLang(d backupimport.BackupManga, lang string) string {
	if lang != "" && lang != "multi" {
		return lang
	}
	counts := map[string]int{}
	for _, c := range d.Chapters {
		if c.Lang != "" {
			counts[c.Lang]++
		}
	}
	best := ""
	for l, n := range counts {
		if n > counts[best] || (n == counts[best] && l < best) {
			best = l
		}
	}
	if best != "" {
		return best
	}
	if len(m.langs) > 0 {
		return m.langs[0]
	}
	return "en"
}

func (m *mapper) findCatalog(names []string, lang string) *catalogs.Catalog {
	for _, n := range names {
		cs := m.byName[norm(n)]
		for _, want := range []string{lang, "all", "multi", ""} {
			for i := range cs {
				if want == "" || strings.EqualFold(cs[i].Lang, want) {
					return &cs[i]
				}
			}
		}
	}
	return nil
}

func (m *mapper) findExtension(name, lang string) *model.ImportExtension {
	n := norm(name)
	if n == "" {
		return nil
	}
	var best *extRef
	for i := range m.exts {
		x := &m.exts[i]
		if norm(x.ext.Name) != n {
			continue
		}
		if best == nil || (lang != "" && x.ext.Lang == lang && best.ext.Lang != lang) {
			best = x
		}
	}
	if best == nil {
		return nil
	}
	return &model.ImportExtension{ModuleID: best.moduleID, Pkg: best.ext.Pkg, Name: best.ext.Name, Lang: best.ext.Lang}
}

func (m *mapper) mapAidoku(ctx context.Context, e *model.ImportEntry) {
	d := e.Data
	rule, lang := m.aidokuRule(d.SourceID)
	lang = m.entryLang(d, lang)
	cat := m.findCatalog(rule.names, lang)
	if cat == nil {
		if ext := m.findExtension(rule.names[0], lang); ext != nil {
			e.Extension = ext
			return
		}
		e.Message = "no catalog for " + firstNonEmpty(d.SourceName, d.SourceID)
		return
	}
	if u := rule.url(d); u != "" {
		if rule.exact {
			e.Source = m.source(*cat, d, u, model.MatchRule)
			return
		}
		dctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		det, _, err := m.s.Search.Details(dctx, cat.ModuleID, source.MangaRef{SourceID: cat.ID, URL: u, TitleHint: d.Title}, false)
		cancel()
		if err == nil && det.Details != nil && titlematch.Best([]string{d.Title}, det.Details.Title) >= ReviewThreshold {
			e.Source = m.source(*cat, d, u, model.MatchPath)
			e.Source.Title = det.Details.Title
			if det.Details.ThumbnailURL != "" {
				e.Source.ThumbnailURL = det.Details.ThumbnailURL
			}
			return
		}
	}
	m.searchIn(ctx, e, *cat)
}

// searchIn looks for the entry's title in one catalog.
func (m *mapper) searchIn(ctx context.Context, e *model.ImportEntry, cat catalogs.Catalog) {
	d := e.Data
	sctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	page, _, err := m.s.Search.Search(sctx, cat.ModuleID, cat.ID, d.Title, 1)
	cancel()
	if err != nil {
		e.Message = "searching " + cat.DisplayName + ": " + err.Error()
		return
	}
	var best *source.Manga
	score := 0.0
	for i := range page.Mangas {
		if sc := titlematch.Best([]string{d.Title}, page.Mangas[i].Title); sc > score {
			best, score = &page.Mangas[i], sc
		}
	}
	if best == nil || score < ReviewThreshold {
		return
	}
	e.Source = m.source(cat, d, best.URL, model.MatchTitle)
	e.Source.Title, e.Source.ThumbnailURL, e.Source.Score = best.Title, best.ThumbnailURL, score
}

// findByTitle runs the quick search over the active catalogs.
func (m *mapper) findByTitle(ctx context.Context, e *model.ImportEntry) {
	d := e.Data
	if strings.TrimSpace(d.Title) == "" {
		return
	}
	res, err := m.s.Search.Quick(ctx, sourcesearch.QuickSearchInput{Query: d.Title}, sourcesearch.QuickOptions{Budget: 30 * time.Second})
	if err != nil {
		return
	}
	c := res.Match
	if c == nil && len(res.Top) > 0 && res.Top[0].Score >= ReviewThreshold {
		c = &res.Top[0]
	}
	if c == nil {
		return
	}
	e.Source = &model.ImportSource{ModuleID: c.ModuleID, SourceID: c.SourceID, SourceName: c.SourceName, Lang: c.Lang, URL: c.Manga.URL,
		Title: c.Manga.Title, ThumbnailURL: c.Manga.ThumbnailURL, How: model.MatchTitle, Score: c.Score}
	if e.Message != "" {
		e.Message += "; found by title"
	}
}

// trackerOrder tries the most useful tracker ids first.
var trackerOrder = []string{backupimport.TrackerAniList, backupimport.TrackerMAL, backupimport.TrackerMangaUpdates, backupimport.TrackerKitsu}

func (m *mapper) matchMetadata(ctx context.Context, d backupimport.BackupManga) *model.ImportMetadata {
	for _, mm := range m.meta {
		if id := d.Trackers[mm.Def.Implementation]; id != "" {
			return &model.ImportMetadata{ModuleID: mm.Def.ID, Provider: mm.Def.Implementation, ID: id, Title: d.Title, How: model.MatchTracker}
		}
	}
	for _, mm := range m.meta {
		lk, ok := any(mm.Instance).(metadata.ExternalLookup)
		if !ok {
			continue
		}
		for _, tracker := range trackerOrder {
			id := d.Trackers[tracker]
			if id == "" {
				continue
			}
			lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			md, err := lk.LookupExternal(lctx, tracker, id)
			cancel()
			if err == nil && md != nil {
				return &model.ImportMetadata{ModuleID: mm.Def.ID, Provider: md.Provider, ID: md.ID, Title: md.Title, CoverURL: md.CoverURL,
					How: model.MatchLookup}
			}
		}
	}
	if strings.TrimSpace(d.Title) == "" || len(m.meta) == 0 {
		return nil
	}
	sctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	cands, _ := m.s.Metadata.Search(sctx, d.Title, 5)
	cancel()
	type scored struct {
		i     int
		score float64
	}
	var list []scored
	for i, c := range cands {
		list = append(list, scored{i, titlematch.Best(append([]string{c.Title}, c.AltTitles...), d.Title)})
	}
	sort.SliceStable(list, func(a, b int) bool { return list[a].score > list[b].score })
	if len(list) == 0 || list[0].score < MetadataThreshold {
		return nil
	}
	c := cands[list[0].i]
	return &model.ImportMetadata{ModuleID: c.ModuleID, Provider: c.Provider, ID: c.ID, Title: c.Title, CoverURL: c.CoverURL,
		How: model.MatchTitle, Score: list[0].score}
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if x != "" {
			return x
		}
	}
	return ""
}
