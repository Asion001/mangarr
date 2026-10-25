package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/naming"
)

func init() { register((*Server).registerSeriesWorks) }

func (s *Server) workContext(ctx context.Context, seriesID int64) (string, []EditionSummary) {
	var selected model.Series
	if err := s.app.DB.NewSelect().Model(&selected).Column("work_id").Where("id = ?", seriesID).Scan(ctx); err != nil || selected.WorkID == 0 {
		return "", nil
	}
	var work model.Work
	_ = s.app.DB.NewSelect().Model(&work).Column("title").Where("id = ?", selected.WorkID).Scan(ctx)
	var editions []model.Series
	_ = s.app.DB.NewSelect().Model(&editions).Where("work_id = ?", selected.WorkID).Order("language", "id").Scan(ctx)
	p := access.From(ctx)
	out := make([]EditionSummary, 0, len(editions))
	for _, edition := range editions {
		if p.Sees(&edition) {
			out = append(out, editionSummary(edition))
		}
	}
	return work.Title, out
}

// baseLanguage is a language tag's primary subtag, lowercased ("pt-BR" ->
// "pt"), with "ua" read as Ukrainian.
func baseLanguage(tag string) string {
	tag = strings.ToLower(strings.TrimSpace(tag))
	if i := strings.IndexAny(tag, "-_;,"); i >= 0 {
		tag = tag[:i]
	}
	if tag == "ua" {
		return "uk"
	}
	return tag
}

// uiLanguage is the first language of an Accept-Language header, which the
// web UI sets to its interface language.
func uiLanguage(acceptLanguage string) string {
	first, _, _ := strings.Cut(acceptLanguage, ",")
	if lang := baseLanguage(first); lang != "*" {
		return lang
	}
	return ""
}

type uiLanguageKey struct{}

// uiLanguageMiddleware keeps the request's interface language, which
// names titles in the series resources.
func uiLanguageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if lang := uiLanguage(r.Header.Get("Accept-Language")); lang != "" {
			r = r.WithContext(context.WithValue(r.Context(), uiLanguageKey{}, lang))
		}
		next.ServeHTTP(w, r)
	})
}

func uiLanguageFrom(ctx context.Context) string {
	lang, _ := ctx.Value(uiLanguageKey{}).(string)
	return lang
}

// titleLanguages are the metadata names tried, in order, for an interface
// language: Ukrainian falls back to Russian, which Shikimori has.
func titleLanguages(lang string) []string {
	switch lang {
	case "":
		return []string{"en"}
	case "uk":
		return []string{"uk", "ru"}
	}
	return []string{lang}
}

// displayTitle is a series' name in the interface language, else its
// romanized original. ok is false when the metadata names none and the
// series' own title is returned; a title the user set always wins.
func displayTitle(ser model.Series, lang string) (title string, ok bool) {
	md := ser.Metadata
	if md.Locked("title") || len(md.Titles) == 0 {
		return ser.Title, false
	}
	for _, key := range append(titleLanguages(lang), model.TitleRomanized) {
		if t := strings.TrimSpace(md.Titles[key]); t != "" {
			return t, true
		}
	}
	return ser.Title, false
}

// localEdition is the edition in the reader's language, or nil when the
// title has none.
func localEdition(editions []model.Series, lang string) *model.Series {
	if lang == "" {
		return nil
	}
	for i := range editions {
		if baseLanguage(editions[i].Language) == lang {
			return &editions[i]
		}
	}
	return nil
}

// groupedSeriesResources turns visible language editions into one row per
// canonical work. Callers may pre-filter list by library or language; only
// those editions then contribute to the row and its aggregate statistics.
// A title with an edition in lang shows that edition's name and cover;
// otherwise it shows the title's own name and its first listed edition.
func (s *Server) groupedSeriesResources(ctx context.Context, list []model.Series, lang string) ([]SeriesResource, error) {
	stats, err := s.seriesStats(ctx, 0)
	if err != nil {
		return nil, err
	}
	p := access.From(ctx)
	following := s.follows(ctx)
	visible := make([]model.Series, 0, len(list))
	for _, ser := range list {
		if p.Sees(&ser) {
			visible = append(visible, ser)
		}
	}
	works := map[int64]model.Work{}
	var workRows []model.Work
	if err := s.app.DB.NewSelect().Model(&workRows).Scan(ctx); err != nil {
		return nil, err
	}
	for _, work := range workRows {
		works[work.ID] = work
	}
	groups := map[int64][]model.Series{}
	var order []int64
	for _, ser := range visible {
		key := ser.WorkID
		if key == 0 {
			key = -ser.ID
		}
		if len(groups[key]) == 0 {
			order = append(order, key)
		}
		groups[key] = append(groups[key], ser)
	}
	var grouped []int64 // editions of titles shown in more than one language
	for _, editions := range groups {
		if len(editions) > 1 {
			for _, edition := range editions {
				grouped = append(grouped, edition.ID)
			}
		}
	}
	titles, err := s.titleCounts(ctx, grouped)
	if err != nil {
		return nil, err
	}
	out := make([]SeriesResource, 0, len(groups))
	for _, key := range order {
		editions := groups[key]
		representative := editions[0]
		local := localEdition(editions, lang)
		if local != nil {
			representative = *local
		}
		r := s.seriesResource(ctx, representative, stats, false)
		r.Editions = make([]EditionSummary, 0, len(editions))
		r.Stats = SeriesStats{}
		for _, edition := range editions {
			r.Editions = append(r.Editions, editionSummary(edition))
			r.Stats = addStats(r.Stats, stats[edition.ID])
			r.Following = r.Following || following[edition.ID]
		}
		if work, ok := works[representative.WorkID]; ok {
			r.WorkTitle = work.Title
			if local == nil {
				r.Title, r.SortTitle = work.Title, work.SortTitle
				if _, named := displayTitle(representative, lang); !named {
					r.DisplayTitle = work.Title
				}
			}
		}
		r.SortTitle = naming.SortTitle(r.DisplayTitle)
		if t, ok := titles[key]; ok {
			// a chapter in two languages is one chapter of the title
			r.Stats.ChapterCount, r.Stats.FileCount, r.Stats.ReadCount, r.Stats.InProgressCount = t.Chapters, t.Files, t.Read, t.InProgress
		}
		out = append(out, r)
	}
	return out, nil
}

// titleCount is a work's chapters counted by number across its editions.
type titleCount struct {
	WorkID     int64 `bun:"work_id"`
	Chapters   int   `bun:"chapters"`
	Files      int   `bun:"files"`
	Read       int   `bun:"read_count"`
	InProgress int   `bun:"in_progress"`
}

// titleCounts counts, per work, the chapters of the given editions by
// number: how many there are, how many have a file in some language, and how
// many the counted readers finished or started.
func (s *Server) titleCounts(ctx context.Context, editions []int64) (map[int64]titleCount, error) {
	out := map[int64]titleCount{}
	if len(editions) == 0 {
		return out, nil
	}
	var rows []titleCount
	if err := s.app.DB.NewSelect().TableExpr("chapters AS c").Join("JOIN series AS s ON s.id = c.series_id").
		ColumnExpr("s.work_id").
		ColumnExpr("COUNT(DISTINCT c.number_key) AS chapters").
		ColumnExpr("COUNT(DISTINCT CASE WHEN c.file_id IS NOT NULL THEN c.number_key END) AS files").
		Where("c.series_id IN (?)", bun.In(editions)).GroupExpr("s.work_id").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.WorkID] = r
	}
	readers := s.statsUsers(ctx)
	if len(readers) == 0 {
		return out, nil
	}
	rows = nil
	if err := s.app.DB.NewSelect().TableExpr("chapter_read_states AS rs").
		Join("JOIN chapters AS c ON c.id = rs.chapter_id").Join("JOIN series AS s ON s.id = rs.series_id").
		ColumnExpr("s.work_id").
		ColumnExpr("COUNT(DISTINCT CASE WHEN rs.completed THEN c.number_key END) AS read_count").
		ColumnExpr("COUNT(DISTINCT CASE WHEN NOT rs.completed AND rs.page > 0 THEN c.number_key END) AS in_progress").
		Where("rs.series_id IN (?) AND rs.user_id IN (?)", bun.In(editions), bun.In(readers)).GroupExpr("s.work_id").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		t := out[r.WorkID]
		t.Read, t.InProgress = r.Read, r.InProgress
		out[r.WorkID] = t
	}
	return out, nil
}

func (s *Server) registerSeriesWorks() {
	tags := []string{"Series"}
	huma.Register(s.api, huma.Operation{OperationID: "series-work-update", Method: http.MethodPut, Path: "/api/v1/series/{id}/work", Tags: tags,
		Summary: "Group this language edition with a work, or separate it with workId 0"},
		func(ctx context.Context, in *struct {
			ID   int64 `path:"id"`
			Body struct {
				WorkID int64 `json:"workId"`
			}
		}) (*struct{}, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			return nil, seriesError(s.app.Series.SetWork(ctx, in.ID, in.Body.WorkID))
		})
}
