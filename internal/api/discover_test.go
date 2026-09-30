package api_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

func TestDiscoverAggregatesPersonalLibraryAndCachedSources(t *testing.T) {
	srv, app := newServer(t, true)
	ctx := context.Background()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: now}
	if _, err := app.DB.NewInsert().Model(root).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var profile model.Profile
	if err := app.DB.NewSelect().Model(&profile).Order("id").Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}

	addSeries := func(title string, genres []string, changed time.Time) (*model.Series, *model.Chapter) {
		t.Helper()
		series := &model.Series{Title: title, SortTitle: strings.ToLower(title), Status: model.StatusOngoing, Monitored: true,
			MonitorNew: model.MonitorAll, RootFolderID: root.ID, Path: title, ProfileID: profile.ID, Language: "en",
			SourcePriorityMode: "inherit", ReadingDirection: "rtl", Tags: []int64{}, Metadata: model.SeriesMetadata{Genres: genres},
			AddedAt: changed, UpdatedAt: changed}
		if _, err := app.DB.NewInsert().Model(series).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		chapter := &model.Chapter{SeriesID: series.ID, NumberKey: "12", NumberSort: 12, Title: "Update", Monitored: true,
			State: model.ChapterMissing, FirstSeenAt: changed, UpdatedAt: changed, ReleaseDate: &changed}
		if _, err := app.DB.NewInsert().Model(chapter).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		return series, chapter
	}
	readSeries, readChapter := addSeries("Read Action", []string{"Action"}, now.Add(-3*time.Hour))
	recommended, _ := addSeries("Suggested Action", []string{"Action", "Adventure"}, now.Add(-2*time.Hour))
	recent, _ := addSeries("Recent Drama", []string{"Drama"}, now.Add(-time.Hour))
	readerID, err := app.Reading.ReaderID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readAt := now.Add(-2 * time.Hour)
	if err := readstate.Save(ctx, app.DB, &model.ChapterReadState{ReaderID: readerID, ChapterID: readChapter.ID,
		SeriesID: readSeries.ID, Completed: true, ReadAt: &readAt, SyncedAt: readAt}); err != nil {
		t.Fatal(err)
	}

	scenario := fakesource.NewScenario("discover")
	scenario.Sources = []source.SourceInfo{
		{ID: "A", Name: "Alpha", DisplayName: "Alpha (EN)", Lang: "en", SupportsLatest: true},
		{ID: "B", Name: "Beta", DisplayName: "Beta (EN)", Lang: "en", SupportsLatest: true},
	}
	scenario.AddManga(&fakesource.Manga{SourceID: "A", URL: "/suggested", Title: recommended.Title})
	scenario.AddManga(&fakesource.Manga{SourceID: "A", URL: "/new", Title: "New Source Title"})
	scenario.AddManga(&fakesource.Manga{SourceID: "B", URL: "/duplicate", Title: "New Source Title"})
	definition := &model.ProviderDefinition{Kind: "source", Implementation: "fake", Name: "Discover fake", Enabled: true,
		Settings: map[string]any{"scenario": "discover"}}
	if err := app.Modules.Create(ctx, definition); err != nil {
		t.Fatal(err)
	}

	get := func() api.DiscoverResponse {
		t.Helper()
		var response api.DiscoverResponse
		if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/discover?lang=en&limit=10", "", &response); code != http.StatusOK {
			t.Fatalf("discover: %d", code)
		}
		return response
	}
	first := get()
	if len(first.Recommendations) < 2 || first.Recommendations[0].SeriesID != recommended.ID || first.Recommendations[0].Reason != "matches-genres" {
		t.Fatalf("personal recommendations: %+v", first.Recommendations)
	}
	if len(first.Updates) != 3 || first.Updates[0].SeriesID != recent.ID || first.Updates[0].LatestChapter != "12" {
		t.Fatalf("recent updates: %+v", first.Updates)
	}
	if len(first.Popular) != 2 || first.PopularCached {
		t.Fatalf("popular aggregation: %+v cached=%v", first.Popular, first.PopularCached)
	}
	foundExisting := false
	for _, item := range first.Popular {
		foundExisting = foundExisting || item.ExistingSeriesID == recommended.ID
	}
	if !foundExisting {
		t.Fatalf("popular title was not matched to the existing series: %+v", first.Popular)
	}
	if first.Popular[0].ThumbnailURL == "" {
		t.Fatal("discover result has no signed thumbnail")
	}
	if code := doJSON(t, http.MethodGet, srv.URL+first.Popular[0].ThumbnailURL, "", nil); code != http.StatusOK {
		t.Fatalf("signed thumbnail: %d", code)
	}
	if code := doJSON(t, http.MethodGet, srv.URL+first.Popular[0].ThumbnailURL+"x", "", nil); code != http.StatusNotFound {
		t.Fatalf("tampered thumbnail token: %d", code)
	}
	for _, tc := range []struct {
		shelf string
		want  []string
	}{
		{"recommendations", []string{recommended.Title, recent.Title}},
		{"recently-updated", []string{recent.Title, recommended.Title, readSeries.Title}},
		{"popular", []string{first.Popular[0].Title, first.Popular[1].Title}},
	} {
		var titles []string
		cursor := ""
		for pageNumber := 0; ; pageNumber++ {
			if pageNumber > 4 {
				t.Fatalf("%s did not terminate", tc.shelf)
			}
			var page api.DiscoverShelfPage
			if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/discover/"+tc.shelf+"?lang=en&pageSize=1&cursor="+cursor, "", &page); code != http.StatusOK {
				t.Fatalf("%s page: %d", tc.shelf, code)
			}
			if page.Library == nil || page.Popular == nil || page.SourceErrors == nil || len(page.Library)+len(page.Popular) > 1 {
				t.Fatalf("invalid shelf response: %+v", page)
			}
			for _, item := range page.Library {
				titles = append(titles, item.Title)
			}
			for _, item := range page.Popular {
				titles = append(titles, item.Title)
				if code := doJSON(t, http.MethodGet, srv.URL+item.ThumbnailURL, "", nil); code != http.StatusOK {
					t.Fatalf("shelf thumbnail: %d", code)
				}
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		if !reflect.DeepEqual(titles, tc.want) {
			t.Fatalf("%s titles: %v want %v", tc.shelf, titles, tc.want)
		}
	}
	second := get()
	if !second.PopularCached {
		t.Fatal("second discover request should reuse every source page")
	}
	scenario.Update(func() {
		if scenario.Browses["A"] != 1 || scenario.Browses["B"] != 1 {
			t.Fatalf("browse cache calls: %v", scenario.Browses)
		}
		scenario.BrowseErr["B"] = context.DeadlineExceeded
	})
	app.SourceCache.Clear()
	partial := get()
	if len(partial.SourceErrors) != 1 || partial.SourceErrors[0].Source != catalogs.Key(definition.ID, "B") || len(partial.Popular) != 2 {
		t.Fatalf("partial source response: errors=%+v popular=%+v", partial.SourceErrors, partial.Popular)
	}
}

func TestDiscoverShelfHTTPValidationAndPermissions(t *testing.T) {
	srv, a := newServer(t, false)
	for _, shelf := range []string{"recommendations", "recently-updated", "popular"} {
		if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/discover/"+shelf, "", nil); code != http.StatusUnauthorized {
			t.Fatalf("anonymous %s: %d", shelf, code)
		}
	}
	for _, name := range []string{"shelf-owner", "shelf-reader"} {
		if _, err := a.Auth.CreateUser(t.Context(), auth.NewUser{Username: name, Password: "fixture-password"}); err != nil {
			t.Fatal(err)
		}
	}
	reader := login(t, srv.URL, "shelf-reader", "fixture-password")
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"recommendations", 200}, {"recently-updated", 200}, {"popular", 200},
		{"unknown", 422}, {"popular?format=manga", 400}, {"recently-updated?sort=popularity", 400},
		{"recommendations?cursor=invalid", 400}, {"popular?pageSize=0", 422}, {"popular?pageSize=101", 422},
		{"recommendations?sort=invalid", 422}, {"recommendations?inLibrary=invalid", 422},
		{"recommendations?format=invalid", 422}, {"recommendations?status=invalid", 422},
		{"popular?source=invalid", 400},
		{"recommendations?cursor=00000000000000000000000000000000", 410},
	} {
		resp, err := reader.Get(srv.URL + "/api/v1/discover/" + tc.query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Errorf("%s: %d want %d", tc.query, resp.StatusCode, tc.status)
		}
	}
}
