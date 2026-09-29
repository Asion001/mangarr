package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/sourcesearch"
)

// TitlePreviewInput opens a title without adding it: either an exact source
// link, or a title (with its metadata) that is matched at the sources.
type TitlePreviewInput struct {
	Metadata *metadataagg.Ref `json:"metadata,omitempty"`
	Title    string           `json:"title,omitempty" maxLength:"500"`
	// Titles are other names to match at the sources.
	Titles   []string           `json:"titles,omitempty" maxItems:"20"`
	Language string             `json:"language,omitempty" maxLength:"20"`
	Source   *series.SourceLink `json:"source,omitempty"`
}

type TitlePreview struct {
	SeriesID int64 `json:"seriesId"`
	// InLibrary: the title is already in the library; SeriesID is that series.
	InLibrary bool `json:"inLibrary"`
	Created   bool `json:"created"`
}

func init() { register((*Server).registerPreviews) }

func (s *Server) registerPreviews() {
	huma.Register(s.api, huma.Operation{OperationID: "previews-open", Method: http.MethodPost, Path: "/api/v1/previews", Tags: []string{"Series"},
		Summary: "Open a title without adding it to the library (its chapters stream from the source)"},
		func(ctx context.Context, in *struct{ Body TitlePreviewInput }) (*struct{ Body TitlePreview }, error) {
			b := in.Body
			b.Title = strings.TrimSpace(b.Title)
			link := b.Source
			if link != nil && !access.From(ctx).Can(access.LibraryManage) && !access.From(ctx).Can(access.RequestsManage) {
				return nil, huma.Error403Forbidden("only library managers can pick a source")
			}
			if link == nil {
				if b.Title == "" {
					return nil, huma.Error400BadRequest("a title or a source is required")
				}
				res, err := s.app.Search.Quick(ctx, sourcesearch.QuickSearchInput{Query: b.Title, Titles: b.Titles, Lang: b.Language}, sourcesearch.QuickOptions{})
				if err != nil {
					return nil, toHTTPError(err)
				}
				if res.Match == nil {
					return nil, huma.Error404NotFound("none of your sources has this title")
				}
				m := res.Match
				link = &series.SourceLink{ModuleID: m.ModuleID, SourceID: m.SourceID, URL: m.Manga.URL, EngineRef: m.Manga.EngineRef,
					Title: m.Manga.Title, SourceName: m.SourceName, Lang: m.Lang}
			}
			ser, created, err := s.app.Series.Preview(ctx, series.AddRequest{Metadata: b.Metadata, Title: b.Title, Language: b.Language,
				Sources: []series.SourceLink{*link}})
			var exists series.ExistsError
			if errors.As(err, &exists) {
				return &struct{ Body TitlePreview }{TitlePreview{SeriesID: exists.SeriesID, InLibrary: true}}, nil
			}
			if err != nil {
				return nil, seriesError(err)
			}
			// fetch the chapter list (again, for a preview opened before)
			if _, err := s.app.Queue.Push(ctx, "RefreshSeries", map[string]any{"seriesId": ser.ID}, "preview"); err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body TitlePreview }{TitlePreview{SeriesID: ser.ID, Created: created}}, nil
		})
}
