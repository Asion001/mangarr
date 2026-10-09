package api

import (
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
	sortSeriesSearch(items, "size")
	if items[0].Title != "B" || items[1].Title != "C" || items[2].Title != "A" {
		t.Fatalf("size order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
	}
	sortSeriesSearch(items, "title")
	if items[0].Title != "A" || items[2].Title != "C" {
		t.Fatalf("title order: %s %s %s", items[0].Title, items[1].Title, items[2].Title)
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
	for _, query := range []SeriesSearchQuery{{Genre: "Romance"}, {Genre: "романтика"}, {Genre: "Comedy"}, {Query: "Романтика"}, {Query: "comedy"}} {
		if !matchesSeriesQuery(item, query) {
			t.Fatalf("query should match: %+v", query)
		}
	}
	for _, query := range []SeriesSearchQuery{{Genre: "Horror"}, {Query: "rom"}} {
		if matchesSeriesQuery(item, query) {
			t.Fatalf("query should not match: %+v", query)
		}
	}
}
