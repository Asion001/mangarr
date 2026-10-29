package api

import (
	"slices"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

func searchSeries(id int64, title string) SeriesResource {
	return SeriesResource{Series: model.Series{ID: id, Title: title, SortTitle: title, Language: "en", RootFolderID: 1,
		Metadata: model.SeriesMetadata{AltTitles: []string{title + " alternate"}}, AddedAt: time.Unix(id, 0)}}
}

func TestMatchesSeriesQueryScopesTitlesAndReading(t *testing.T) {
	item := searchSeries(1, "Moonlit Journey")
	item.Following = true
	item.Editions = []EditionSummary{{ID: 2, Title: "Лунное путешествие", Language: "ru"}}
	item.Stats = SeriesStats{ChapterCount: 10, ReadCount: 3, MissingCount: 2}
	for _, query := range []SeriesSearchQuery{
		{Query: "moonlit"},
		{Query: "alternate"},
		{Query: "лунное"},
		{Filter: "following"},
		{Filter: "missing"},
		{Filter: "unread"},
		{Filter: "reading"},
		{RootFolderID: 1},
		{Language: "en"},
	} {
		if !matchesSeriesQuery(item, query) {
			t.Fatalf("query should match: %+v", query)
		}
	}
	for _, query := range []SeriesSearchQuery{{Query: "absent"}, {RootFolderID: 2}, {Language: "uk"}, {Filter: "completed"}} {
		if matchesSeriesQuery(item, query) {
			t.Fatalf("query should not match: %+v", query)
		}
	}
}

func TestMatchesSeriesQueryIgnoresPunctuation(t *testing.T) {
	item := searchSeries(1, "Einz: The Laid-Off Cheat-Granting Mage")
	for _, q := range []string{"@Einz:The Laid-Off Cheat-Granting Mage", "einz the laid off", "laid-off"} {
		if !matchesSeriesQuery(item, SeriesSearchQuery{Query: q}) {
			t.Fatalf("%q should match", q)
		}
	}
	for _, q := range []string{"@@", "einz mage"} {
		if matchesSeriesQuery(item, SeriesSearchQuery{Query: q}) {
			t.Fatalf("%q should not match", q)
		}
	}
}

func TestSortSeriesSearch(t *testing.T) {
	a, b, c := searchSeries(1, "A"), searchSeries(2, "B"), searchSeries(3, "C")
	a.Stats.SizeOnDisk, b.Stats.SizeOnDisk, c.Stats.SizeOnDisk = 10, 30, 20
	items := []SeriesResource{a, b, c}
	sortSeriesSearch(items, "size", "")
	if items[0].Title != "B" || items[1].Title != "C" || items[2].Title != "A" {
		t.Fatalf("size order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	sortSeriesSearch(items, "title", "")
	if items[0].Title != "A" || items[2].Title != "C" {
		t.Fatalf("title order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	sortSeriesSearch(items, "size", "asc")
	if items[0].Title != "A" || items[2].Title != "B" {
		t.Fatalf("size ascending: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	sortSeriesSearch(items, "title", "desc")
	if items[0].Title != "C" || items[2].Title != "A" {
		t.Fatalf("title descending: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	// downloaded share: B has all monitored chapters, C half, A none monitored
	a, b, c = items[2], items[1], items[0]
	b.Stats.MonitoredCount, c.Stats.MonitoredCount, c.Stats.MissingCount = 4, 4, 2
	items = []SeriesResource{a, c, b}
	sortSeriesSearch(items, "progress", "")
	if items[0].Title != "B" || items[1].Title != "C" || items[2].Title != "A" {
		t.Fatalf("progress order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	// a title nobody asked for sorts after the requested ones
	when := time.Unix(100, 0)
	a.RequestedAt, b.Stats = &when, SeriesStats{}
	items = []SeriesResource{c, b, a}
	sortSeriesSearch(items, "requested", "")
	if items[0].Title != "A" || items[1].Title != "B" {
		t.Fatalf("requested order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	a.Status, b.Status, c.Status = "completed", "ongoing", "hiatus"
	items = []SeriesResource{a, c, b}
	sortSeriesSearch(items, "status", "")
	if items[0].Title != "B" || items[1].Title != "C" || items[2].Title != "A" {
		t.Fatalf("status order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
}

func TestMatchesSeriesQueryManagementFilters(t *testing.T) {
	item := searchSeries(1, "Moonlit Journey")
	item.Status = "ongoing"
	item.Stats = SeriesStats{ChapterCount: 10, MonitoredCount: 10, FileCount: 6, MissingCount: 4, ReadCount: 3}
	item.RequestedBy = []SeriesRequester{{ID: 7, Name: "alex"}}
	for _, query := range []SeriesSearchQuery{
		{Monitored: "no"},
		{Status: "ongoing"},
		{Downloads: "missing"},
		{Reading: "reading"},
		{Reading: "unread"},
		{RequestedBy: "7"},
		{Monitored: "no", Status: "ongoing", Downloads: "missing", RequestedBy: "7"},
	} {
		if !matchesSeriesQuery(item, query) {
			t.Fatalf("%+v should match", query)
		}
	}
	for _, query := range []SeriesSearchQuery{
		{Monitored: "yes"},
		{Status: "completed"},
		{Downloads: "complete"},
		{Downloads: "none"},
		{Reading: "unstarted"},
		{Reading: "finished"},
		{Following: true},
		{RequestedBy: "8"},
		{RequestedBy: "none"},
	} {
		if matchesSeriesQuery(item, query) {
			t.Fatalf("%+v should not match", query)
		}
	}
}

func TestLocalEditionFollowsInterfaceLanguage(t *testing.T) {
	editions := []model.Series{{ID: 1, Language: "en"}, {ID: 2, Language: "ru"}, {ID: 3, Language: "pt-BR"}}
	for header, want := range map[string]int64{"ru": 2, "ru-RU,en;q=0.8": 2, "pt": 3, "EN": 1, "uk": 0, "": 0, "*": 0} {
		got := localEdition(editions, uiLanguage(header))
		if (got == nil && want != 0) || (got != nil && got.ID != want) {
			t.Fatalf("%q: got %+v, want edition %d", header, got, want)
		}
	}
	if uiLanguage("ua") != "uk" {
		t.Fatal("ua should read as Ukrainian")
	}
}

func TestMatchesSeriesQueryByGenre(t *testing.T) {
	item := searchSeries(1, "Moonlit Journey")
	item.Metadata.Genres, item.Metadata.Tags = []string{"Romance"}, []string{"Комедия"}
	for _, query := range []SeriesSearchQuery{
		{Genres: []string{"Romance"}}, {Genres: []string{"романтика"}}, {Genres: []string{"Comedy"}}, {Query: "Романтика"}, {Query: "comedy"},
		{Genres: []string{"Romance", "Comedy"}}, {Genres: []string{"Romance", "Horror"}, GenreMatch: "any"}, {Genres: []string{""}, GenreMatch: "any"},
	} {
		if !matchesSeriesQuery(item, query) {
			t.Fatalf("query should match: %+v", query)
		}
	}
	for _, query := range []SeriesSearchQuery{
		{Genres: []string{"Horror"}}, {Query: "rom"}, {Genres: []string{"Romance", "Horror"}}, {Genres: []string{"Horror", "Drama"}, GenreMatch: "any"},
	} {
		if matchesSeriesQuery(item, query) {
			t.Fatalf("query should not match: %+v", query)
		}
	}
}

func TestSortSeriesSearchRating(t *testing.T) {
	low, high := 6.5, 8.6
	a, b, c := searchSeries(1, "A"), searchSeries(2, "B"), searchSeries(3, "C")
	b.Score, c.Score = &low, &high
	for _, order := range []string{"", "asc"} {
		items := []SeriesResource{a, b, c}
		sortSeriesSearch(items, "rating", order)
		want := []string{"C", "B", "A"}
		if order == "asc" {
			want = []string{"B", "C", "A"}
		}
		// a title without a score comes last either way
		if got := []string{items[0].Title, items[1].Title, items[2].Title}; !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", order, got, want)
		}
	}
}
