// Package duplicates finds titles in the library that are really one work
// (an import that could not find the metadata provider adds a second title
// for an edition already there), and titles that have no metadata provider
// at all, with the match it finds for them.
//
// A scan first asks the metadata providers about every title that has no
// provider yet, then pairs titles of different works that share a tracker id,
// a name or an author. Pairs in different languages can be grouped into one
// title (and are, by themselves, when the scan is sure and the setting is
// on); pairs in the same language are only ever merged on request.
package duplicates

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/titlematch"
)

// Kinds of findings.
const (
	KindPair     = "pair"
	KindMetadata = "metadata"
)

// States of a finding.
const (
	StateOpen      = "open"      // waiting for a decision
	StateAuto      = "auto"      // grouped or linked by the scan itself
	StateDone      = "done"      // grouped, merged or linked on request
	StateDismissed = "dismissed" // "not duplicates": never shown again
	StateNone      = "none"      // metadata: no provider knows the title
)

// recheck is how long a metadata lookup that found nothing is trusted.
const recheck = 7 * 24 * time.Hour

// sureScore is the name score at which a metadata match is linked by itself.
const sureScore = 0.97

// Reason says why two titles look like one work. Code is "id" (a shared
// tracker id; Params provider), "mal" (a shared MyAnimeList id), "name"
// (Params name) or "author" (Params name).
type Reason struct {
	Code   string            `json:"code" enum:"id,mal,name,author"`
	Params map[string]string `json:"params,omitempty"`
}

// Match is a metadata series the scan found for a title.
type Match struct {
	metadataagg.Ref
	Title    string   `json:"title"`
	CoverURL string   `json:"coverUrl,omitempty"`
	Year     int      `json:"year,omitempty"`
	Authors  []string `json:"authors,omitempty"`
	Score    float64  `json:"score"`
	// IDs are the series' ids at every provider that knows it.
	IDs map[string]string `json:"ids,omitempty"`
}

// Finding is one thing the scan found.
type Finding struct {
	bun.BaseModel `bun:"table:duplicate_findings"`
	ID            int64    `bun:"id,pk,autoincrement" json:"id"`
	Kind          string   `bun:"kind,notnull" json:"kind" enum:"pair,metadata"`
	SeriesA       int64    `bun:"series_a,notnull" json:"seriesA"`
	SeriesB       int64    `bun:"series_b,notnull" json:"seriesB"`
	KeepID        int64    `bun:"keep_id,notnull" json:"keepId"`
	SameLanguage  bool     `bun:"same_language,notnull" json:"sameLanguage"`
	Sure          bool     `bun:"sure,notnull" json:"sure"`
	Reasons       []Reason `bun:"reasons,type:jsonb,notnull" json:"reasons" nullable:"false"`
	// Match is the metadata to link: for a pair, what was found for the one
	// of them that has no provider.
	Match     *Match    `bun:"match,type:jsonb" json:"match,omitempty"`
	State     string    `bun:"state,notnull" json:"state" enum:"open,auto,done,dismissed,none"`
	CheckedAt time.Time `bun:"checked_at,notnull" json:"checkedAt"`
	UpdatedAt time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}

// Other is the pair's title that is not kept.
func (f *Finding) Other() int64 {
	if f.KeepID == f.SeriesA {
		return f.SeriesB
	}
	return f.SeriesA
}

// SeriesService is what the scan needs from the series service.
type SeriesService interface {
	SetWork(ctx context.Context, seriesID, workID int64) error
	LinkMetadata(ctx context.Context, id int64, ref metadataagg.Ref) (*model.Series, error)
	Get(ctx context.Context, id int64) (*model.Series, error)
	Merge(ctx context.Context, keepID, dropID int64) error
}

type Service struct {
	DB       bun.IDB
	Series   SeriesService
	Metadata *metadataagg.Aggregator
	Settings *settings.Store
	Log      *slog.Logger
	// Lookup finds the metadata series for a title; imports.MatchTitleCandidate
	// when nil (tests replace it).
	Lookup func(ctx context.Context, title string, alts []string) (*metadataagg.Candidate, float64)
	// Changed is told when findings change.
	Changed func()
}

var ErrNotFound = errors.New("finding not found")

// Result counts what a scan did.
type Result struct {
	LookedUp, Found, Pairs, Grouped, Linked int
}

// title is what the scan knows about one series.
type title struct {
	model.Series
	sources []model.SeriesSource
	files   int
	match   *Match // looked up, when it has no provider
}

func (t *title) workKey() int64 {
	if t.WorkID != 0 {
		return t.WorkID
	}
	return -t.ID
}

// hasProvider says whether a metadata provider supplied its ids.
func (t *title) hasProvider() bool {
	for _, v := range t.Metadata.ExternalIDs {
		if v != "" {
			return true
		}
	}
	return false
}

// ids are its tracker ids: its own, or the looked-up match's.
func (t *title) ids() map[string]string {
	if t.hasProvider() {
		return t.Metadata.ExternalIDs
	}
	if t.match != nil {
		return t.match.IDs
	}
	return nil
}

// names are every name it goes by, normalized.
func (t *title) names() map[string]string {
	out := map[string]string{}
	add := func(n string) {
		if k := titlematch.Normalize(n); len([]rune(k)) >= 4 {
			if _, ok := out[k]; !ok {
				out[k] = n
			}
		}
	}
	add(t.Title)
	for _, n := range t.Metadata.AltTitles {
		add(n)
	}
	for _, n := range t.Metadata.Titles {
		add(n)
	}
	for _, s := range t.sources {
		add(s.Title)
	}
	if t.match != nil {
		add(t.match.Title)
	}
	return out
}

func (t *title) authors() map[string]string {
	out := map[string]string{}
	list := append(append([]string{}, t.Metadata.Authors...), t.Metadata.Artists...)
	if t.match != nil {
		list = append(list, t.match.Authors...)
	}
	for _, a := range list {
		if k := titlematch.Normalize(a); k != "" {
			out[k] = a
		}
	}
	return out
}

// queries are the names a lookup searches: its title, the names its sources
// and links spell, and its other names.
func (t *title) queries() (string, []string) {
	var alts []string
	for _, s := range t.sources {
		if n := imports.SlugTitle(s.MangaURL); n != "" {
			alts = append(alts, n)
		}
		if n := imports.SlugTitle(s.WebURL); n != "" {
			alts = append(alts, n)
		}
	}
	for _, s := range t.sources {
		alts = append(alts, s.Title)
	}
	alts = append(alts, t.Metadata.AltTitles...)
	for _, n := range t.Metadata.Titles {
		alts = append(alts, n)
	}
	return t.Title, alts
}

// Scan looks titles up and records what looks duplicated. With only, it
// looks up just those titles again (whatever was found before) and pairs
// the library as usual.
func (s *Service) Scan(ctx context.Context, only []int64, progress func(string)) (Result, error) {
	var res Result
	if progress == nil {
		progress = func(string) {}
	}
	titles, err := s.load(ctx)
	if err != nil {
		return res, err
	}
	var old []Finding
	if err := s.DB.NewSelect().Model(&old).Scan(ctx); err != nil {
		return res, err
	}
	prev := map[string]*Finding{}
	for i := range old {
		prev[key(old[i].Kind, old[i].SeriesA, old[i].SeriesB)] = &old[i]
	}
	now := time.Now().UTC()

	// 1. ask the providers about titles that have none
	var unlinked []*title
	for _, t := range titles {
		if !t.hasProvider() {
			unlinked = append(unlinked, t)
		} else if f := prev[key(KindMetadata, t.ID, 0)]; f != nil && (f.State == StateOpen || f.State == StateNone) {
			// linked by hand since
			if _, err := s.DB.NewDelete().Model((*Finding)(nil)).Where("id = ?", f.ID).Exec(ctx); err != nil {
				return res, err
			}
			delete(prev, key(KindMetadata, t.ID, 0))
		}
	}
	for i, t := range unlinked {
		f := prev[key(KindMetadata, t.ID, 0)]
		fresh := f != nil && (f.State == StateDismissed || f.State == StateDone || f.State == StateAuto ||
			(f.Match != nil && f.State == StateOpen) || now.Sub(f.CheckedAt) < recheck)
		if len(only) > 0 {
			fresh = !slices.Contains(only, t.ID)
		}
		if fresh {
			if f != nil && f.State != StateDismissed {
				t.match = f.Match
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return res, err
		}
		progress(fmt.Sprintf("looking up %d of %d: %s", i+1, len(unlinked), t.Title))
		res.LookedUp++
		t.match = s.lookup(ctx, t)
		state := StateNone
		if t.match != nil {
			res.Found++
			state = StateOpen
		}
		if f == nil {
			f = &Finding{Kind: KindMetadata, SeriesA: t.ID, KeepID: t.ID, Reasons: []Reason{}}
			prev[key(KindMetadata, t.ID, 0)] = f
		}
		f.Match, f.State, f.Sure, f.CheckedAt, f.UpdatedAt = t.match, state, t.match != nil && t.match.Score >= sureScore, now, now
		if err := s.save(ctx, f); err != nil {
			return res, err
		}
	}

	// 2. pair titles of different works
	progress("comparing titles")
	pairs := pairUp(titles)
	res.Pairs = len(pairs)
	byID := map[int64]*title{}
	for _, t := range titles {
		byID[t.ID] = t
	}
	seen := map[string]bool{}
	for _, p := range pairs {
		k := key(KindPair, p.SeriesA, p.SeriesB)
		seen[k] = true
		f := prev[k]
		if f != nil && f.State != StateOpen {
			continue
		}
		if f == nil {
			f = &p
		} else {
			f.KeepID, f.SameLanguage, f.Sure, f.Reasons, f.Match = p.KeepID, p.SameLanguage, p.Sure, p.Reasons, p.Match
		}
		f.CheckedAt, f.UpdatedAt = now, now
		if err := s.save(ctx, f); err != nil {
			return res, err
		}
		prev[k] = f
	}
	// pairs no longer found (grouped by hand, one deleted) go away
	for _, f := range prev {
		if f.Kind == KindPair && f.State == StateOpen && !seen[key(f.Kind, f.SeriesA, f.SeriesB)] {
			if _, err := s.DB.NewDelete().Model((*Finding)(nil)).Where("id = ?", f.ID).Exec(ctx); err != nil {
				return res, err
			}
		}
	}

	// 3. what the scan is sure of, it does itself
	cfg, err := s.Settings.Duplicates(ctx)
	if err != nil {
		return res, err
	}
	if cfg.AutoGroup {
		linked := map[int64]bool{}
		for _, f := range prev {
			if f.Kind != KindPair || f.State != StateOpen || !f.Sure || f.SameLanguage {
				continue
			}
			progress("grouping " + byID[f.Other()].Title)
			if err := s.group(ctx, f, nil, StateAuto); err != nil {
				s.Log.Warn("group duplicate", "finding", f.ID, "err", err)
				continue
			}
			res.Grouped++
			if f.Match != nil {
				linked[f.SeriesA], linked[f.SeriesB] = true, true
			}
		}
		for _, f := range prev {
			if f.Kind != KindMetadata || f.State != StateOpen || !f.Sure || f.Match == nil || linked[f.SeriesA] {
				continue
			}
			progress("linking metadata to " + byID[f.SeriesA].Title)
			if err := s.link(ctx, f, StateAuto); err != nil {
				s.Log.Warn("link metadata", "series", f.SeriesA, "err", err)
				continue
			}
			res.Linked++
		}
	}
	if s.Changed != nil {
		s.Changed()
	}
	return res, nil
}

func key(kind string, a, b int64) string { return fmt.Sprintf("%s:%d:%d", kind, a, b) }

func (s *Service) save(ctx context.Context, f *Finding) error {
	if f.Reasons == nil {
		f.Reasons = []Reason{}
	}
	if f.ID == 0 {
		_, err := s.DB.NewInsert().Model(f).Exec(ctx)
		return err
	}
	_, err := s.DB.NewUpdate().Model(f).WherePK().Exec(ctx)
	return err
}

// load reads every title in the library with its sources and file count.
func (s *Service) load(ctx context.Context) ([]*title, error) {
	var list []model.Series
	if err := s.DB.NewSelect().Model(&list).Where("preview = ?", false).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := s.DB.NewSelect().Model(&sources).Order("priority", "id").Scan(ctx); err != nil {
		return nil, err
	}
	var files []struct {
		SeriesID int64 `bun:"series_id"`
		N        int   `bun:"n"`
	}
	if err := s.DB.NewSelect().TableExpr("chapters").ColumnExpr("series_id").ColumnExpr("COUNT(*) AS n").
		Where("file_id IS NOT NULL").GroupExpr("series_id").Scan(ctx, &files); err != nil {
		return nil, err
	}
	out := make([]*title, len(list))
	byID := map[int64]*title{}
	for i := range list {
		out[i] = &title{Series: list[i]}
		byID[list[i].ID] = out[i]
	}
	for _, src := range sources {
		if t := byID[src.SeriesID]; t != nil {
			t.sources = append(t.sources, src)
		}
	}
	for _, f := range files {
		if t := byID[f.SeriesID]; t != nil {
			t.files = f.N
		}
	}
	return out, nil
}

// lookup asks the metadata providers for the title.
func (s *Service) lookup(ctx context.Context, t *title) *Match {
	find := s.Lookup
	if find == nil {
		find = func(ctx context.Context, name string, alts []string) (*metadataagg.Candidate, float64) {
			return imports.MatchTitleCandidate(ctx, s.Metadata, name, alts)
		}
	}
	name, alts := t.queries()
	c, score := find(ctx, name, alts)
	if c == nil {
		return nil
	}
	ids := map[string]string{}
	for k, v := range c.ExternalIDs {
		if v != "" {
			ids[k] = v
		}
	}
	ids[c.Provider] = c.ID
	for _, r := range c.Also {
		ids[r.Provider] = r.ID
	}
	return &Match{Ref: metadataagg.Ref{ModuleID: c.ModuleID, Provider: c.Provider, ID: c.ID}, Title: c.Title, CoverURL: c.CoverURL,
		Year: c.Year, Authors: c.Authors, Score: score, IDs: ids}
}

// pairUp finds the titles of different works that look like one.
func pairUp(titles []*title) []Finding {
	type facts struct {
		ids, names, authors map[string]string
	}
	info := make([]facts, len(titles))
	byKey := map[string][]int{}
	for i, t := range titles {
		info[i] = facts{ids: t.ids(), names: t.names(), authors: t.authors()}
		for p, id := range info[i].ids {
			if id != "" {
				byKey["id\x00"+p+"\x00"+id] = append(byKey["id\x00"+p+"\x00"+id], i)
			}
		}
		for n := range info[i].names {
			byKey["name\x00"+n] = append(byKey["name\x00"+n], i)
		}
	}
	candidates := map[[2]int]bool{}
	for _, list := range byKey {
		if len(list) > 20 { // a name every other title has says nothing
			continue
		}
		for x := 0; x < len(list); x++ {
			for y := x + 1; y < len(list); y++ {
				a, b := list[x], list[y]
				if a != b && titles[a].workKey() != titles[b].workKey() {
					candidates[[2]int{min(a, b), max(a, b)}] = true
				}
			}
		}
	}
	var out []Finding
	for c := range candidates {
		a, b := titles[c[0]], titles[c[1]]
		fa, fb := info[c[0]], info[c[1]]
		var reasons []Reason
		strong, mal, name, author := false, false, false, false
		for p, id := range fa.ids {
			if id == "" || fb.ids[p] != id {
				continue
			}
			if p == "mal" {
				mal = true
				reasons = append(reasons, Reason{Code: "mal"})
			} else {
				strong = true
				reasons = append(reasons, Reason{Code: "id", Params: map[string]string{"provider": p}})
			}
		}
		for k, n := range fa.names {
			if _, ok := fb.names[k]; ok && !name {
				name = true
				reasons = append(reasons, Reason{Code: "name", Params: map[string]string{"name": n}})
			}
		}
		for k, n := range fa.authors {
			if _, ok := fb.authors[k]; ok && !author {
				author = true
				reasons = append(reasons, Reason{Code: "author", Params: map[string]string{"name": n}})
			}
		}
		// two providers disagreeing on the id is two works, whatever the name says
		if !strong && conflict(fa.ids, fb.ids) {
			continue
		}
		if !strong && !mal && !name {
			continue
		}
		sort.SliceStable(reasons, func(i, j int) bool { return rank(reasons[i].Code) < rank(reasons[j].Code) })
		f := Finding{Kind: KindPair, SeriesA: a.ID, SeriesB: b.ID, KeepID: keep(a, b).ID, State: StateOpen, Reasons: reasons,
			SameLanguage: strings.EqualFold(a.Language, b.Language),
			Sure:         strong || (mal && (name || author)) || (name && author)}
		for _, t := range []*title{a, b} {
			if !t.hasProvider() && t.match != nil {
				f.Match = t.match
			}
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SeriesA < out[j].SeriesA || out[i].SeriesA == out[j].SeriesA && out[i].SeriesB < out[j].SeriesB
	})
	return out
}

func rank(code string) int { return slices.Index([]string{"id", "mal", "name", "author"}, code) }

// conflict says whether the two know the same provider under different ids.
func conflict(a, b map[string]string) bool {
	for p, id := range a {
		if p != "mal" && id != "" && b[p] != "" && b[p] != id {
			return true
		}
	}
	return false
}

// keep picks the title to keep: the one with a provider, then the one with
// more chapters on disk, then the older one.
func keep(a, b *title) *title {
	if a.hasProvider() != b.hasProvider() {
		if a.hasProvider() {
			return a
		}
		return b
	}
	if a.files != b.files {
		if a.files > b.files {
			return a
		}
		return b
	}
	if a.ID < b.ID {
		return a
	}
	return b
}

// List returns the findings to show, newest first.
func (s *Service) List(ctx context.Context) ([]Finding, error) {
	var out []Finding
	err := s.DB.NewSelect().Model(&out).Order("updated_at DESC", "id DESC").Scan(ctx)
	return out, err
}

func (s *Service) get(ctx context.Context, id int64) (*Finding, error) {
	var f Finding
	if err := s.DB.NewSelect().Model(&f).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, ErrNotFound
	}
	return &f, nil
}

// Group makes the pair one title (other languages) and links the metadata
// found for it; match replaces what the scan found.
func (s *Service) Group(ctx context.Context, id int64, match *metadataagg.Ref) error {
	f, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if f.Kind != KindPair {
		return ErrNotFound
	}
	if err := s.group(ctx, f, match, StateDone); err != nil {
		return err
	}
	if s.Changed != nil {
		s.Changed()
	}
	return nil
}

func (s *Service) group(ctx context.Context, f *Finding, override *metadataagg.Ref, state string) error {
	kept, err := s.Series.Get(ctx, f.KeepID)
	if err != nil {
		return err
	}
	other, err := s.Series.Get(ctx, f.Other())
	if err != nil {
		return err
	}
	if kept.WorkID != other.WorkID {
		if err := s.Series.SetWork(ctx, other.ID, kept.WorkID); err != nil {
			return err
		}
	}
	ref := override
	if ref == nil && f.Match != nil {
		ref = &f.Match.Ref
	}
	if ref != nil {
		for _, ser := range []*model.Series{kept, other} {
			if override != nil || !(&title{Series: *ser}).hasProvider() {
				if _, err := s.Series.LinkMetadata(ctx, ser.ID, *ref); err != nil {
					return err
				}
				s.close(ctx, ser.ID, state)
			}
		}
	}
	f.State, f.UpdatedAt = state, time.Now().UTC()
	return s.save(ctx, f)
}

// Merge folds the pair into keepID (one of the two, same language) and links
// the metadata found for it when it has none.
func (s *Service) Merge(ctx context.Context, id, keepID int64) error {
	f, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	if f.Kind != KindPair || (keepID != f.SeriesA && keepID != f.SeriesB) {
		return ErrNotFound
	}
	f.KeepID = keepID
	if err := s.Series.Merge(ctx, keepID, f.Other()); err != nil {
		return err
	}
	if kept, err := s.Series.Get(ctx, keepID); err == nil && f.Match != nil && !(&title{Series: *kept}).hasProvider() {
		if _, err := s.Series.LinkMetadata(ctx, keepID, f.Match.Ref); err != nil {
			s.Log.Warn("link metadata after merge", "series", keepID, "err", err)
		} else {
			s.close(ctx, keepID, StateDone)
		}
	}
	f.State, f.UpdatedAt = StateDone, time.Now().UTC()
	if err := s.save(ctx, f); err != nil {
		return err
	}
	if s.Changed != nil {
		s.Changed()
	}
	return nil
}

// close marks a title's metadata finding settled.
func (s *Service) close(ctx context.Context, seriesID int64, state string) {
	_, _ = s.DB.NewUpdate().Model((*Finding)(nil)).Set("state = ?", state).Set("updated_at = ?", time.Now().UTC()).
		Where("kind = ? AND series_a = ?", KindMetadata, seriesID).Exec(ctx)
}

// Link links the matches found for these titles. It returns how many were
// linked; titles with nothing found are skipped.
func (s *Service) Link(ctx context.Context, seriesIDs []int64) (int, error) {
	if len(seriesIDs) == 0 {
		return 0, nil
	}
	var list []Finding
	if err := s.DB.NewSelect().Model(&list).Where("kind = ? AND series_a IN (?)", KindMetadata, bun.In(seriesIDs)).Scan(ctx); err != nil {
		return 0, err
	}
	n := 0
	var errs []error
	for i := range list {
		if list[i].Match == nil {
			continue
		}
		if err := s.link(ctx, &list[i], StateDone); err != nil {
			errs = append(errs, err)
			continue
		}
		n++
	}
	if s.Changed != nil {
		s.Changed()
	}
	return n, errors.Join(errs...)
}

func (s *Service) link(ctx context.Context, f *Finding, state string) error {
	if _, err := s.Series.LinkMetadata(ctx, f.SeriesA, f.Match.Ref); err != nil {
		return err
	}
	f.State, f.UpdatedAt = state, time.Now().UTC()
	return s.save(ctx, f)
}

// SetDismissed hides a finding for good ("not duplicates"), or brings it back.
func (s *Service) SetDismissed(ctx context.Context, id int64, dismissed bool) error {
	f, err := s.get(ctx, id)
	if err != nil {
		return err
	}
	f.State = StateDismissed
	if !dismissed {
		f.State = StateOpen
		if f.Kind == KindMetadata && f.Match == nil {
			f.State = StateNone
		}
	}
	f.UpdatedAt = time.Now().UTC()
	if err := s.save(ctx, f); err != nil {
		return err
	}
	if s.Changed != nil {
		s.Changed()
	}
	return nil
}
