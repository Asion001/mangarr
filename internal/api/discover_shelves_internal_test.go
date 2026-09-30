package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/sourcecache"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

type discoverTestEnv struct {
	s       *Server
	ctx     context.Context
	root    int64
	profile int64
	reader  int64
	now     time.Time
	serial  int
}

func discoverTestDialects(t *testing.T, fn func(*testing.T, *discoverTestEnv)) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			_, ring := logging.Setup("error", io.Discard)
			a, err := app.New(t.Context(), &config.Config{DataDir: t.TempDir(), DB: dsn, AuthDisabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			now := time.Now().UTC().Truncate(time.Second)
			root := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: now}
			if _, err := a.DB.NewInsert().Model(root).Exec(t.Context()); err != nil {
				t.Fatal(err)
			}
			var profile model.Profile
			if err := a.DB.NewSelect().Model(&profile).Order("id").Limit(1).Scan(t.Context()); err != nil {
				t.Fatal(err)
			}
			reader, err := a.Reading.ReaderID(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			p := access.AdminPrincipal(access.KindAnonymous)
			p.ReaderID = reader
			fn(t, &discoverTestEnv{s: &Server{app: a}, ctx: access.With(t.Context(), p), root: root.ID, profile: profile.ID, reader: reader, now: now})
		})
	}
}

func (e *discoverTestEnv) add(t *testing.T, title string, age int) *model.Series {
	t.Helper()
	e.serial++
	at := e.now.Add(-time.Duration(age) * time.Hour)
	ser := &model.Series{Title: title, SortTitle: strings.ToLower(title), Status: model.StatusOngoing, RootFolderID: e.root, ProfileID: e.profile,
		Path: fmt.Sprintf("series-%d", e.serial), Language: "en", Tags: []int64{7}, Metadata: model.SeriesMetadata{Genres: []string{"Adventure"}, Tags: []string{"Quest"}, Format: "manga"}, AddedAt: at, UpdatedAt: at}
	if _, err := e.s.app.DB.NewInsert().Model(ser).Exec(e.ctx); err != nil {
		t.Fatal(err)
	}
	ch := &model.Chapter{SeriesID: ser.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterMissing, FirstSeenAt: at, UpdatedAt: at}
	if _, err := e.s.app.DB.NewInsert().Model(ch).Exec(e.ctx); err != nil {
		t.Fatal(err)
	}
	return ser
}

func (e *discoverTestEnv) save(t *testing.T, ser *model.Series) {
	t.Helper()
	if _, err := e.s.app.DB.NewUpdate().Model(ser).WherePK().Exec(e.ctx); err != nil {
		t.Fatal(err)
	}
}

func (e *discoverTestEnv) page(t *testing.T, in DiscoverShelfInput) DiscoverShelfPage {
	t.Helper()
	if in.PageSize == 0 {
		in.PageSize = 50
	}
	out, err := e.s.discoverShelf(e.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func discoverIDs(page DiscoverShelfPage) []int64 {
	ids := []int64{}
	for _, item := range page.Library {
		ids = append(ids, item.SeriesID)
	}
	return ids
}

func TestDiscoverShelfLibraryFiltersSortsAndVisibility(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		a := e.add(t, "Amber Voyage", 3)
		b := e.add(t, "Beryl Voyage", 2)
		c := e.add(t, "Cobalt Voyage", 1)
		b.Language, b.Status, b.Metadata.Format, b.Metadata.Genres, b.Metadata.Tags, b.Tags = "fr", model.StatusCompleted, "manhwa", []string{"Drama"}, []string{"School"}, []int64{8}
		c.Metadata.Format = "manhua"
		a.UpdatedAt = e.now
		e.save(t, a)
		e.save(t, b)
		e.save(t, c)
		def := &model.ProviderDefinition{Kind: "source", Implementation: "fake", Name: "Shelf source", Enabled: true, Settings: map[string]any{"scenario": "shelf-filter"}}
		fakesource.NewScenario("shelf-filter")
		if err := e.s.app.Modules.Create(e.ctx, def); err != nil {
			t.Fatal(err)
		}
		link := &model.SeriesSource{SeriesID: b.ID, ModuleID: def.ID, SourceID: "B", MangaURL: "/beryl", CreatedAt: e.now, NextCheckAt: e.now}
		if _, err := e.s.app.DB.NewInsert().Model(link).Exec(e.ctx); err != nil {
			t.Fatal(err)
		}
		for _, shelf := range []string{"recommendations", "recently-updated"} {
			for _, tc := range []struct {
				name  string
				input DiscoverShelfInput
				want  []int64
			}{
				{"language", DiscoverShelfInput{Lang: " FR "}, []int64{b.ID}},
				{"genre", DiscoverShelfInput{Genre: "dRaMa"}, []int64{b.ID}},
				{"tag", DiscoverShelfInput{Tag: "sChOoL"}, []int64{b.ID}},
				{"tag-id", DiscoverShelfInput{TagID: 8}, []int64{b.ID}},
				{"manga", DiscoverShelfInput{Format: "manga"}, []int64{a.ID}},
				{"manhwa", DiscoverShelfInput{Format: "manhwa"}, []int64{b.ID}},
				{"manhua", DiscoverShelfInput{Format: "manhua"}, []int64{c.ID}},
				{"status", DiscoverShelfInput{Status: model.StatusCompleted}, []int64{b.ID}},
				{"source", DiscoverShelfInput{Source: catalogs.Key(def.ID, "B")}, []int64{b.ID}},
				{"in-library", DiscoverShelfInput{InLibrary: "true"}, []int64{a.ID, b.ID, c.ID}},
				{"not-in-library", DiscoverShelfInput{InLibrary: "false"}, []int64{}},
				{"combined", DiscoverShelfInput{Lang: "en", Genre: "Drama"}, []int64{}},
				{"root", DiscoverShelfInput{RootFolderID: e.root + 999}, []int64{}},
				{"title", DiscoverShelfInput{Sort: "title"}, []int64{a.ID, b.ID, c.ID}},
				{"newest", DiscoverShelfInput{Sort: "newest"}, []int64{c.ID, b.ID, a.ID}},
				{"updated", DiscoverShelfInput{Sort: "recently-updated"}, []int64{a.ID, c.ID, b.ID}},
			} {
				t.Run(shelf+"/"+tc.name, func(t *testing.T) {
					tc.input.Shelf = shelf
					if tc.input.Sort == "" {
						tc.input.Sort = "title"
					}
					got := e.page(t, tc.input)
					if !reflect.DeepEqual(discoverIDs(got), tc.want) {
						t.Fatalf("got %v want %v", discoverIDs(got), tc.want)
					}
				})
			}
		}
		// The personalized default still scores genres from started titles.
		var chapter model.Chapter
		if err := e.s.app.DB.NewSelect().Model(&chapter).Where("series_id = ?", b.ID).Scan(e.ctx); err != nil {
			t.Fatal(err)
		}
		rs := &model.ChapterReadState{ReaderID: e.reader, ChapterID: chapter.ID, SeriesID: b.ID, Completed: true, SyncedAt: e.now}
		if err := readstate.Save(e.ctx, e.s.app.DB, rs); err != nil {
			t.Fatal(err)
		}
		a.Metadata.Genres = []string{"Drama"}
		e.save(t, a)
		ranked := e.page(t, DiscoverShelfInput{Shelf: "recommendations"})
		if !reflect.DeepEqual(discoverIDs(ranked), []int64{a.ID, c.ID}) || ranked.Library[0].Reason != "matches-genres" {
			t.Fatalf("ranking: %+v", ranked)
		}
		viewer := &access.Principal{Kind: access.KindUser, ReaderID: e.reader, Perms: map[string]bool{}, Scope: access.Scope{RootFolders: []int64{e.root}, IncludeTags: []int64{7}, ExcludeTags: []int64{8}}}
		e.ctx = access.With(t.Context(), viewer)
		b.Tags = []int64{7, 8}
		e.save(t, b)
		for _, shelf := range []string{"recommendations", "recently-updated"} {
			got := e.page(t, DiscoverShelfInput{Shelf: shelf, Sort: "title"})
			if !reflect.DeepEqual(discoverIDs(got), []int64{a.ID, c.ID}) {
				t.Fatalf("visibility %s: %+v", shelf, got)
			}
		}
		viewer.Scope.RootFolders = []int64{e.root + 999}
		if len(e.page(t, DiscoverShelfInput{Shelf: "recently-updated"}).Library) != 0 {
			t.Fatal("root scope leaked")
		}
		viewer.Perms[access.LibraryManage] = true
		if len(e.page(t, DiscoverShelfInput{Shelf: "recently-updated"}).Library) != 3 {
			t.Fatal("manager should see all")
		}
	})
}

func TestDiscoverShelfStableCursor(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		for i := 0; i < 9; i++ {
			e.add(t, "Tied Voyage", 1)
		}
		for _, shelf := range []string{"recommendations", "recently-updated"} {
			for _, order := range []string{"title", "newest", "recently-updated", ""} {
				in := DiscoverShelfInput{Shelf: shelf, Sort: order, PageSize: 2}
				first := e.page(t, in)
				if first.NextCursor == "" {
					t.Fatal("missing continuation")
				}
				before := e.page(t, DiscoverShelfInput{Shelf: shelf, Sort: order, PageSize: 100})
				inserted := e.add(t, "A New Voyage", 0)
				inserted.Path += fmt.Sprint(inserted.ID)
				e.save(t, inserted)
				ids := discoverIDs(first)
				in.Cursor = first.NextCursor
				replay := e.page(t, in)
				if !reflect.DeepEqual(discoverIDs(replay), discoverIDs(e.page(t, in))) {
					t.Fatal("cursor replay changed")
				}
				for in.Cursor != "" {
					page := e.page(t, in)
					ids = append(ids, discoverIDs(page)...)
					in.Cursor = page.NextCursor
					if len(ids) > 100 {
						t.Fatal("cursor did not terminate")
					}
				}
				if !reflect.DeepEqual(ids, discoverIDs(before)) {
					t.Fatalf("unstable paging: %v != %v", ids, discoverIDs(before))
				}
			}
		}
		in := DiscoverShelfInput{Shelf: "recently-updated", PageSize: 1}
		in.Cursor = e.page(t, in).NextCursor
		changed := in
		changed.Lang = "fr"
		if _, err := e.s.discoverShelf(e.ctx, changed); err == nil {
			t.Fatal("changed filters accepted")
		}
		other := access.AdminPrincipal(access.KindAnonymous)
		other.ReaderID = e.reader + 99
		if _, err := e.s.discoverShelf(access.With(t.Context(), other), in); err == nil {
			t.Fatal("another caller accepted")
		}
		// Series visibility is rechecked even with the same principal and cursor.
		p := access.From(e.ctx)
		p.Perms = map[string]bool{}
		p.Scope.ExcludeTags = []int64{8}
		in.Cursor = ""
		in.Cursor = e.page(t, in).NextCursor
		var rows []model.Series
		if err := e.s.app.DB.NewSelect().Model(&rows).Scan(e.ctx); err != nil {
			t.Fatal(err)
		}
		for i := range rows {
			rows[i].Tags = []int64{8}
			e.save(t, &rows[i])
		}
		if len(e.page(t, in).Library) != 0 {
			t.Fatal("hidden rows leaked from cursor")
		}
		e.s.app.SourceCache.Clear()
		_, err := e.s.discoverShelf(e.ctx, in)
		if status, ok := err.(huma.StatusError); !ok || status.GetStatus() != 410 {
			t.Fatalf("expired cursor: %v", err)
		}
	})
}

func TestDiscoverShelfPopularPagingAndFilters(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		local := e.add(t, "Library Voyage", 1)
		local.Metadata.AltTitles = []string{"Amber Alias"}
		e.save(t, local)
		hidden := e.add(t, "Hidden Voyage", 1)
		hidden.Tags = []int64{8}
		e.save(t, hidden)
		sc := fakesource.NewScenario("shelf-popular-" + string(e.s.app.DB.Kind))
		sc.Sources = []source.SourceInfo{
			{ID: "A", Name: "Amber", Lang: "en", SupportsLatest: true}, {ID: "B", Name: "Beryl", Lang: "fr"}, {ID: "N", Name: "Night", Lang: "en", NSFW: true},
		}
		manga := func(title string) source.Manga {
			return source.Manga{MangaRef: source.MangaRef{URL: "/" + strings.ReplaceAll(title, " ", "-"), EngineRef: "ref"}, Title: title, ThumbnailURL: "/cover"}
		}
		sc.BrowsePages = map[string]map[int]*source.MangaPage{
			"popular|A": {1: {Mangas: []source.Manga{manga("Amber Alias"), manga("Hidden Voyage"), manga("New Voyage")}, HasNext: true}, 2: {Mangas: []source.Manga{manga("New Voyage"), manga("Next Voyage")}, HasNext: false}},
			"popular|B": {1: {Mangas: []source.Manga{manga("Next Voyage"), manga("French Voyage")}}},
			"popular|N": {1: {Mangas: []source.Manga{manga("Night Voyage")}}},
			"latest|A":  {1: {Mangas: []source.Manga{manga("Latest Voyage")}}},
		}
		def := &model.ProviderDefinition{Kind: "source", Implementation: "fake", Name: "Shelf provider", Enabled: true, Settings: map[string]any{"scenario": "shelf-popular-" + string(e.s.app.DB.Kind)}}
		if err := e.s.app.Modules.Create(e.ctx, def); err != nil {
			t.Fatal(err)
		}
		st, err := e.s.app.Settings.Sources(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		st.DefaultLanguages = nil
		st.HideNSFW = true
		st.Throttle.Preset = "fast"
		if err := e.s.app.Settings.Set(e.ctx, "sources", st); err != nil {
			t.Fatal(err)
		}
		e.s.app.Catalogs.Bump()
		viewer := &access.Principal{Kind: access.KindUser, ReaderID: e.reader, Perms: map[string]bool{}, Scope: access.Scope{ExcludeTags: []int64{8}}}
		e.ctx = access.With(t.Context(), viewer)
		in := DiscoverShelfInput{Shelf: "popular", PageSize: 1}
		var titles []string
		for i := 0; i < 10; i++ {
			page := e.page(t, in)
			for _, item := range page.Popular {
				titles = append(titles, item.Title)
				if item.Title == "Amber Alias" && item.ExistingSeriesID != local.ID {
					t.Fatal("alternate title not matched")
				}
				if item.Title == "Hidden Voyage" && item.ExistingSeriesID != 0 {
					t.Fatal("hidden library ID leaked")
				}
				if item.ThumbnailURL == "" || item.URL == "" || item.ModuleID != def.ID || item.EngineRef != "ref" {
					t.Fatalf("missing action identity: %+v", item)
				}
			}
			if i == 0 {
				repeat := in
				repeat.Cursor = page.NextCursor
				r1, r2 := e.page(t, repeat), e.page(t, repeat)
				if len(r1.Popular) != 1 || r1.Popular[0].Title != r2.Popular[0].Title {
					t.Fatal("source cursor replay unstable")
				}
			}
			in.Cursor = page.NextCursor
			if in.Cursor == "" {
				break
			}
		}
		want := []string{"Amber Alias", "Hidden Voyage", "New Voyage", "Next Voyage", "French Voyage"}
		if !reflect.DeepEqual(titles, want) {
			t.Fatalf("popular paging %v want %v", titles, want)
		}
		sc.Update(func() {
			if sc.Browses["A"] != 2 || sc.Browses["B"] != 1 || sc.Browses["N"] != 0 {
				t.Fatalf("provider paging/cache: %v", sc.Browses)
			}
		})
		for _, tc := range []struct {
			name string
			in   DiscoverShelfInput
			want []string
		}{
			{"language", DiscoverShelfInput{Lang: "fr"}, []string{"Next Voyage", "French Voyage"}},
			{"source", DiscoverShelfInput{Source: catalogs.Key(def.ID, "B")}, []string{"Next Voyage", "French Voyage"}},
			{"hidden-source", DiscoverShelfInput{Source: catalogs.Key(def.ID, "N")}, []string{}},
			{"membership", DiscoverShelfInput{InLibrary: "true"}, []string{"Amber Alias"}},
			{"not-member", DiscoverShelfInput{InLibrary: "false"}, []string{"Hidden Voyage", "New Voyage", "Next Voyage", "French Voyage"}},
			{"latest", DiscoverShelfInput{Sort: "recently-updated", Source: catalogs.Key(def.ID, "A")}, []string{"Latest Voyage"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				tc.in.Shelf = "popular"
				got := e.page(t, tc.in)
				names := []string{}
				for _, item := range got.Popular {
					names = append(names, item.Title)
				}
				if !reflect.DeepEqual(names, tc.want) {
					t.Fatalf("%v want %v", names, tc.want)
				}
			})
		}
		sc.Update(func() {
			if sc.BrowseKinds["latest"] != 1 {
				t.Fatal("latest sort did not call Latest")
			}
		})
		unsupported := e.page(t, DiscoverShelfInput{Shelf: "popular", Sort: "recently-updated", Source: catalogs.Key(def.ID, "B")})
		if len(unsupported.Popular) != 0 || len(unsupported.SourceErrors) != 1 || unsupported.SourceErrors[0].Source != catalogs.Key(def.ID, "B") {
			t.Fatalf("unsupported catalog sort: %+v", unsupported)
		}
		// Existing NSFW settings and catalog changes invalidate retained pages.
		in = DiscoverShelfInput{Shelf: "popular", PageSize: 1}
		in.Cursor = e.page(t, in).NextCursor
		st.HideNSFW = false
		if err := e.s.app.Settings.Set(e.ctx, "sources", st); err != nil {
			t.Fatal(err)
		}
		e.s.app.Catalogs.Bump()
		if _, err := e.s.discoverShelf(e.ctx, in); err == nil {
			t.Fatal("catalog change accepted stale cursor")
		}
		visible := e.page(t, DiscoverShelfInput{Shelf: "popular", Source: catalogs.Key(def.ID, "N")})
		if len(visible.Popular) != 1 {
			t.Fatal("NSFW setting not respected")
		}
		// A failed catalog does not hide another catalog's successful results.
		sc.Update(func() { sc.BrowseErr["A"] = context.DeadlineExceeded })
		e.s.app.SourceCache.Clear()
		partial := e.page(t, DiscoverShelfInput{Shelf: "popular"})
		if len(partial.SourceErrors) != 1 || partial.SourceErrors[0].Source != catalogs.Key(def.ID, "A") || len(partial.Popular) != 3 {
			t.Fatalf("partial result: %+v", partial)
		}
	})
}

func TestDiscoverShelfCursorRechecksLibraryState(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		a := e.add(t, "Amber Journey", 1)
		b := e.add(t, "Beryl Journey", 1)
		c := e.add(t, "Cobalt Journey", 1)
		d := e.add(t, "Dawn Journey", 1)
		in := DiscoverShelfInput{Shelf: "recommendations", Sort: "title", Genre: "Adventure", PageSize: 1}
		first := e.page(t, in)
		if !reflect.DeepEqual(discoverIDs(first), []int64{a.ID}) || first.NextCursor == "" {
			t.Fatalf("first page: %+v", first)
		}
		in.Cursor = first.NextCursor
		var chapter model.Chapter
		if err := e.s.app.DB.NewSelect().Model(&chapter).Where("series_id = ?", b.ID).Scan(e.ctx); err != nil {
			t.Fatal(err)
		}
		progress := &model.ChapterReadState{ReaderID: e.reader, ChapterID: chapter.ID, SeriesID: b.ID, Page: 1, SyncedAt: e.now}
		if err := readstate.Save(e.ctx, e.s.app.DB, progress); err != nil {
			t.Fatal(err)
		}
		// Newly started and no-longer-matching candidates disappear; renaming
		// a remaining candidate does not move it ahead of the saved cursor.
		c.Metadata.Genres = []string{"Drama"}
		e.save(t, c)
		d.Title = "A Changed Journey"
		e.save(t, d)
		in.PageSize = 3
		next := e.page(t, in)
		if !reflect.DeepEqual(discoverIDs(next), []int64{d.ID}) || next.NextCursor != "" || next.Library[0].Title != d.Title {
			t.Fatalf("changed candidates: %+v", next)
		}
		if _, err := e.s.app.DB.NewDelete().Model(d).WherePK().Exec(e.ctx); err != nil {
			t.Fatal(err)
		}
		if got := e.page(t, in); len(got.Library) != 0 || got.NextCursor != "" {
			t.Fatalf("deleted candidate returned: %+v", got)
		}
		updated := e.page(t, DiscoverShelfInput{Shelf: "recently-updated", Sort: "title"})
		if len(updated.Library) != 3 || updated.Library[1].SeriesID != b.ID || updated.Library[1].Unread != 0 || updated.Library[1].LatestChapter != "1" {
			t.Fatalf("current reader/chapter state: %+v", updated)
		}
	})
}

func TestDiscoverShelfCursorExpiryAndCapacity(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		e.add(t, "Amber Trail", 1)
		e.add(t, "Beryl Trail", 1)
		in := DiscoverShelfInput{Shelf: "recommendations", PageSize: 1}
		in.Cursor = e.page(t, in).NextCursor
		viewer := *access.From(e.ctx)
		viewer.Perms = map[string]bool{}
		if _, err := e.s.discoverShelf(access.With(t.Context(), &viewer), in); err == nil {
			t.Fatal("changed permissions accepted a cursor")
		}
		key := "discover-cursor|" + in.Cursor
		cached, ok := e.s.app.SourceCache.Get(key)
		if !ok {
			t.Fatal("missing cursor state")
		}
		state := cached.(discoverShelfCursor)
		state.Expires = time.Now().Add(-time.Second)
		e.s.app.SourceCache.Set(key, state, 1024, time.Minute)
		_, err := e.s.discoverShelf(e.ctx, in)
		if status, ok := err.(huma.StatusError); !ok || status.GetStatus() != 410 {
			t.Fatalf("expired snapshot: %v", err)
		}
		e.s.app.SourceCache = sourcecache.New(1)
		in.Cursor = ""
		_, err = e.s.discoverShelf(e.ctx, in)
		if status, ok := err.(huma.StatusError); !ok || status.GetStatus() != 503 {
			t.Fatalf("cache capacity: %v", err)
		}
	})
}

func TestDiscoverShelfPopularFilteredContinuation(t *testing.T) {
	discoverTestDialects(t, func(t *testing.T, e *discoverTestEnv) {
		local := e.add(t, "Amber Crossing", 1)
		// Membership includes translations and titles without chapters.
		local.Language = "fr"
		e.save(t, local)
		if _, err := e.s.app.DB.NewDelete().Model((*model.Chapter)(nil)).Where("series_id = ?", local.ID).Exec(e.ctx); err != nil {
			t.Fatal(err)
		}
		scenarioName := "shelf-filtered-" + string(e.s.app.DB.Kind)
		sc := fakesource.NewScenario(scenarioName)
		sc.Sources = []source.SourceInfo{{ID: "A", Name: "Amber", Lang: "en"}}
		pages := map[int]*source.MangaPage{}
		for i := 1; i <= 9; i++ {
			title := fmt.Sprintf("Uncollected Crossing %d", i)
			if i == 9 {
				title = local.Title
			}
			pages[i] = &source.MangaPage{Mangas: []source.Manga{{Title: title, MangaRef: source.MangaRef{URL: fmt.Sprintf("/crossing-%d", i)}}}, HasNext: i < 9}
		}
		sc.BrowsePages = map[string]map[int]*source.MangaPage{"popular|A": pages}
		def := &model.ProviderDefinition{Kind: "source", Implementation: "fake", Name: "Crossing catalog", Enabled: true, Settings: map[string]any{"scenario": scenarioName}}
		if err := e.s.app.Modules.Create(e.ctx, def); err != nil {
			t.Fatal(err)
		}
		st, err := e.s.app.Settings.Sources(e.ctx)
		if err != nil {
			t.Fatal(err)
		}
		st.Throttle.Preset = "fast"
		if err := e.s.app.Settings.Set(e.ctx, "sources", st); err != nil {
			t.Fatal(err)
		}
		viewer := &access.Principal{Kind: access.KindUser, ReaderID: e.reader, Scope: access.Scope{ExcludeTags: []int64{8}}}
		e.ctx = access.With(t.Context(), viewer)
		in := DiscoverShelfInput{Shelf: "popular", Lang: "en", InLibrary: "true", PageSize: 2}
		first := e.page(t, in)
		if len(first.Popular) != 0 || first.NextCursor == "" {
			t.Fatalf("filtered page must continue: %+v", first)
		}
		sc.Update(func() {
			if sc.Browses["A"] != 8 {
				t.Fatalf("browse budget: %v", sc.Browses)
			}
		})
		in.Cursor = first.NextCursor
		last := e.page(t, in)
		if len(last.Popular) != 1 || last.Popular[0].ExistingSeriesID != local.ID || last.NextCursor != "" {
			t.Fatalf("filtered continuation: %+v", last)
		}
		local.Tags = []int64{8}
		e.save(t, local)
		if hidden := e.page(t, in); len(hidden.Popular) != 0 || hidden.NextCursor != "" {
			t.Fatalf("membership not rechecked: %+v", hidden)
		}
	})
}

func TestDiscoverShelfUnsupportedCombinations(t *testing.T) {
	for _, in := range []DiscoverShelfInput{
		{Shelf: "popular", Genre: "Drama"}, {Shelf: "popular", Tag: "Quest"}, {Shelf: "popular", TagID: 1},
		{Shelf: "popular", Format: "manga"}, {Shelf: "popular", Status: "ongoing"},
		{Shelf: "popular", Sort: "title"}, {Shelf: "popular", Sort: "newest"}, {Shelf: "popular", Sort: "recommended"},
		{Shelf: "recommendations", Sort: "popularity"}, {Shelf: "recently-updated", Sort: "popularity"}, {Shelf: "recently-updated", Sort: "recommended"},
		{Shelf: "popular", Source: "invalid"}, {Shelf: "popular", Source: "0:A"},
	} {
		if err := normalizeDiscoverShelf(&in); err == nil {
			t.Errorf("accepted unsupported query: %+v", in)
		}
	}
}
