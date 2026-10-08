package series

import (
	"context"
	"errors"
	"fmt"

	"github.com/Asion001/mangarr/internal/library"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
)

// EditionOptions are one language's settings in AddEditions. Empty fields
// fall back to the language defaults (Settings → Search).
type EditionOptions struct {
	Language         string `json:"language"`
	ProfileID        int64  `json:"profileId,omitempty"`
	ReadingDirection string `json:"readingDirection,omitempty" enum:",rtl,ltr,vertical,webtoon"`
}

// AddEditionsRequest adds a title from sources in any number of languages:
// each language becomes its own edition, in that language's folder.
type AddEditionsRequest struct {
	// WorkID adds the editions to an existing title.
	WorkID   int64            `json:"workId,omitempty"`
	Metadata *metadataagg.Ref `json:"metadata,omitempty"`
	Title    string           `json:"title,omitempty"`
	// Sources in priority order; each source's lang picks its edition.
	Sources           []SourceLink     `json:"sources"`
	Editions          []EditionOptions `json:"editions,omitempty"`
	Monitor           string           `json:"monitor" enum:"all,future,latest,from,none"`
	LatestCount       int              `json:"latestCount,omitempty"`
	FromChapter       float64          `json:"fromChapter,omitempty"`
	MonitorNew        string           `json:"monitorNew,omitempty" enum:"all,none"`
	SearchMissing     bool             `json:"searchMissing"`
	Tags              []int64          `json:"tags,omitempty"`
	BlockedScanlators []string         `json:"blockedScanlators,omitempty"`
	RequestID         int64            `json:"requestId,omitempty"`
	// NoRefresh skips queueing the first refresh (the caller syncs itself).
	NoRefresh bool `json:"-"`
}

// AddEditionsResult lists the title's editions the add created or added
// sources to, in the order their languages first appear in the sources.
type AddEditionsResult struct {
	WorkID   int64           `json:"workId"`
	Editions []*model.Series `json:"editions"`
}

// AddEditions splits the sources by language and adds one edition per
// language. A language the title already has gets the new sources instead of
// a second edition. Every language's folder is checked before anything is
// created, so a language without a folder blocks the whole add.
func (s *Service) AddEditions(ctx context.Context, req AddEditionsRequest) (*AddEditionsResult, error) {
	if len(req.Sources) == 0 {
		return nil, ValidationError{"at least one source is required"}
	}
	var order []string
	groups := map[string][]SourceLink{}
	for _, l := range req.Sources {
		lang := library.NormalizeLanguage(l.Lang)
		if lang == "" {
			if info := s.lookupSource(ctx, l.ModuleID, l.SourceID); info != nil {
				lang = library.NormalizeLanguage(info.Lang)
			}
		}
		if lang == "" {
			return nil, ValidationError{fmt.Sprintf("choose a language for %s", firstNonEmpty(l.SourceName, l.SourceID))}
		}
		l.Lang = lang
		if _, ok := groups[lang]; !ok {
			order = append(order, lang)
		}
		groups[lang] = append(groups[lang], l)
	}
	folders := map[string]*model.RootFolder{}
	for _, lang := range order {
		rf, err := s.lib.FolderForLanguage(ctx, lang)
		if err != nil {
			return nil, ValidationError{err.Error()}
		}
		folders[lang] = rf
	}
	options := map[string]EditionOptions{}
	for _, o := range req.Editions {
		options[library.NormalizeLanguage(o.Language)] = o
	}
	defaults, _ := s.lib.Settings().Sources(ctx)

	res := &AddEditionsResult{WorkID: req.WorkID, Editions: []*model.Series{}}
	existing := map[string]*model.Series{}
	meta, title := req.Metadata, req.Title
	if req.WorkID > 0 {
		var siblings []model.Series
		if err := s.db.NewSelect().Model(&siblings).Where("work_id = ?", req.WorkID).Order("id").Scan(ctx); err != nil {
			return nil, err
		}
		if len(siblings) == 0 {
			return nil, ValidationError{"work not found"}
		}
		for i := range siblings {
			if lang := library.NormalizeLanguage(siblings[i].Language); lang != "" && existing[lang] == nil {
				existing[lang] = &siblings[i]
			}
		}
		// a new language of a known title is resolved from the title's own match
		if meta == nil {
			meta = s.agg.RefFor(siblings[0].Metadata.ExternalIDs)
		}
		title = firstNonEmpty(title, siblings[0].Title)
	}
	for _, lang := range order {
		links := groups[lang]
		if ser := existing[lang]; ser != nil {
			if err := s.joinSources(ctx, ser, links); err != nil {
				return res, fmt.Errorf("%s edition: %w", lang, err)
			}
			res.Editions = append(res.Editions, ser)
			continue
		}
		o := options[lang]
		d, _ := defaults.ForLanguage(lang)
		profileID := o.ProfileID
		if profileID == 0 {
			profileID = d.ProfileID
		}
		ser, err := s.Add(ctx, AddRequest{
			WorkID: res.WorkID, Metadata: meta, Title: title, Sources: links,
			RootFolderID: folders[lang].ID, ProfileID: profileID, Language: lang,
			ReadingDirection: firstNonEmpty(o.ReadingDirection, d.ReadingDirection),
			Monitor:          req.Monitor, LatestCount: req.LatestCount, FromChapter: req.FromChapter,
			MonitorNew: req.MonitorNew, SearchMissing: req.SearchMissing, Tags: req.Tags,
			BlockedScanlators: req.BlockedScanlators, RequestID: req.RequestID, NoRefresh: req.NoRefresh,
		})
		var exists ExistsError
		if errors.As(err, &exists) {
			// the title is already here in this language: add the sources to it
			if ser, err = s.Get(ctx, exists.SeriesID); err == nil {
				err = s.joinSources(ctx, ser, links)
			}
		}
		if err != nil {
			if len(res.Editions) == 0 {
				return nil, err
			}
			return res, fmt.Errorf("%s edition: %w", lang, err)
		}
		if res.WorkID == 0 {
			res.WorkID = ser.WorkID
		}
		res.Editions = append(res.Editions, ser)
	}
	return res, nil
}

// joinSources links the sources an edition doesn't have yet, after its own.
func (s *Service) joinSources(ctx context.Context, ser *model.Series, links []SourceLink) error {
	var have []model.SeriesSource
	if err := s.db.NewSelect().Model(&have).Where("series_id = ?", ser.ID).Scan(ctx); err != nil {
		return err
	}
	for _, l := range links {
		dup := false
		for _, h := range have {
			if h.ModuleID == l.ModuleID && h.SourceID == l.SourceID && h.MangaURL == l.URL {
				dup = true
				break
			}
		}
		if dup {
			continue
		}
		if _, err := s.LinkSource(ctx, ser.ID, l); err != nil {
			return err
		}
	}
	return nil
}

// Offered are the languages people pick from when they add or request a
// title: the ones searched by default (Languages) first, then every other
// language a root folder holds or has defaults set up.
func (s *Service) Offered(ctx context.Context) (defaults, all []string, err error) {
	if defaults, err = s.Languages(ctx); err != nil {
		return nil, nil, err
	}
	seen := map[string]bool{}
	add := func(lang string) {
		// "*" is the automatic folder, not a language
		if lang = library.NormalizeLanguage(lang); lang != "" && lang != "*" && !seen[lang] {
			seen[lang] = true
			all = append(all, lang)
		}
	}
	for _, lang := range defaults {
		add(lang)
	}
	var roots []model.RootFolder
	if err := s.db.NewSelect().Model(&roots).Order("id").Scan(ctx); err != nil {
		return nil, nil, err
	}
	for _, rf := range roots {
		add(rf.Language)
	}
	src, err := s.lib.Settings().Sources(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, d := range src.LanguageDefaults {
		add(d.Language)
	}
	if defaults == nil {
		defaults = []string{}
	}
	if all == nil {
		all = []string{}
	}
	return defaults, all, nil
}

// Languages are the edition languages an automatic add looks for: the
// default search languages, else the languages the root folders hold.
func (s *Service) Languages(ctx context.Context) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(lang string) {
		if lang = library.NormalizeLanguage(lang); lang != "" && !seen[lang] {
			seen[lang] = true
			out = append(out, lang)
		}
	}
	src, err := s.lib.Settings().Sources(ctx)
	if err != nil {
		return nil, err
	}
	for _, lang := range src.DefaultLanguages {
		add(lang)
	}
	if len(out) > 0 {
		return out, nil
	}
	var roots []model.RootFolder
	if err := s.db.NewSelect().Model(&roots).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	for _, rf := range roots {
		add(rf.Language)
	}
	return out, nil
}
