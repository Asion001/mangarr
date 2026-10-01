package sourcepriority

import (
	"context"
	"slices"
	"testing"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func TestOrderLibraryThenLanguageThenGlobal(t *testing.T) {
	entries := []Entry{{Key: "1:a", Priority: 50}, {Key: "1:b", Priority: 10}, {Key: "2:c", Priority: 20}, {Key: "3:d", Priority: 1}}
	ranks := Order(entries, []string{"2:c", "1:a"}, []string{"1:b", "1:a"})
	want := map[string]int{"2:c": 0, "1:a": 1, "1:b": 2, "3:d": 3}
	for k, v := range want {
		if ranks[k] != v {
			t.Fatalf("%s: got %d want %d", k, ranks[k], v)
		}
	}
}
func TestLanguageAndKeyNormalization(t *testing.T) {
	if Language(" UA ") != "uk" {
		t.Fatal("ua alias")
	}
	if Key(12, "Manga") != "12:Manga" {
		t.Fatal("key")
	}
	if LibraryScope(7) != "library:7" {
		t.Fatal("library scope")
	}
	if LanguageScope("RU") != "language:ru" {
		t.Fatal("language scope")
	}
}

func TestRanksUsesLibraryThenLanguageAndKeepsCustom(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	for id := int64(1); id <= 3; id++ {
		if _, err := d.ExecContext(ctx, `INSERT INTO provider_definitions
			(id, kind, implementation, name, enabled, priority, tags, events, settings, created_at, updated_at)
			VALUES (?, 'source', 'test', ?, TRUE, 25, '[]', '[]', '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, id, Key(id, "module")); err != nil {
			t.Fatal(err)
		}
	}
	lists := []model.SourcePriorityList{
		{Scope: LibraryScope(7), Sources: []string{"2:c"}},
		{Scope: LanguageScope("uk"), Sources: []string{"1:b", "1:a"}},
	}
	for i := range lists {
		if _, err := d.NewInsert().Model(&lists[i]).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	prefs := []model.CatalogPref{
		{ModuleID: 1, SourceID: "a", Priority: 30},
		{ModuleID: 1, SourceID: "b", Priority: 20},
		{ModuleID: 2, SourceID: "c", Priority: 10},
		{ModuleID: 3, SourceID: "d", Priority: 1},
	}
	for i := range prefs {
		if _, err := d.NewInsert().Model(&prefs[i]).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	links := []model.SeriesSource{
		{ID: 11, ModuleID: 1, SourceID: "a", Priority: 9},
		{ID: 12, ModuleID: 1, SourceID: "b", Priority: 8},
		{ID: 13, ModuleID: 2, SourceID: "c", Priority: 7},
		{ID: 14, ModuleID: 3, SourceID: "d", Priority: 6},
	}
	inherited, err := Ranks(ctx, d, model.Series{RootFolderID: 7, Language: "ua", SourcePriorityMode: "inherit"}, links)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]int{13: 0, 12: 1, 11: 2, 14: 3}
	for id, rank := range want {
		if inherited[id] != rank {
			t.Fatalf("link %d: got %d want %d", id, inherited[id], rank)
		}
	}
	custom, err := Ranks(ctx, d, model.Series{SourcePriorityMode: "custom"}, links)
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range links {
		if custom[link.ID] != link.Priority {
			t.Fatalf("custom link %d: got %d want %d", link.ID, custom[link.ID], link.Priority)
		}
	}
}

// TestLanguageDefaultIsTheLanguageOrder: old "language:xx" orders move into
// the language defaults, which then decide the order.
func TestLanguageDefaultIsTheLanguageOrder(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		if err := st.Set(ctx, settings.KeySources, settings.Sources{LanguageDefaults: []settings.LanguageDefault{
			{Language: "en", Sources: []string{"1:x"}},
			{Language: "ru"},
		}}); err != nil {
			t.Fatal(err)
		}
		for _, l := range []model.SourcePriorityList{
			{Scope: LanguageScope("en"), Sources: []string{"1:y"}}, // the default already lists sources: it wins
			{Scope: LanguageScope("ru"), Sources: []string{"1:b", "1:a"}},
			{Scope: LanguageScope("ja"), Sources: []string{"1:j"}},
			{Scope: LibraryScope(3), Sources: []string{"1:l"}},
		} {
			if _, err := d.NewInsert().Model(&l).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		n, err := MergeLanguageLists(ctx, d, st)
		if err != nil || n != 3 {
			t.Fatalf("merged %d, %v", n, err)
		}
		src, _ := st.Sources(ctx)
		got := map[string][]string{}
		for _, def := range src.LanguageDefaults {
			got[def.Language] = def.Sources
		}
		if !slices.Equal(got["en"], []string{"1:x"}) || !slices.Equal(got["ru"], []string{"1:b", "1:a"}) || !slices.Equal(got["ja"], []string{"1:j"}) {
			t.Fatalf("defaults: %v", got)
		}
		left, _ := d.NewSelect().Model((*model.SourcePriorityList)(nil)).Count(ctx)
		if left != 1 {
			t.Fatalf("%d rows left, want only the library one", left)
		}
		ranks, err := Resolve(ctx, d, 0, "RU", []Entry{{"1:a", 1}, {"1:b", 2}, {"1:z", 0}})
		if err != nil {
			t.Fatal(err)
		}
		if ranks["1:b"] != 0 || ranks["1:a"] != 1 || ranks["1:z"] != 2 {
			t.Fatalf("ranks %v", ranks)
		}
	})
}
