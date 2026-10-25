package envcfg

import (
	"context"
	"io"
	"log/slog"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	_ "github.com/Asion001/mangarr/internal/modules/all"
	"github.com/Asion001/mangarr/internal/settings"
)

func TestSnake(t *testing.T) {
	for in, want := range map[string]string{
		"maxConcurrent": "MAX_CONCURRENT", "minFreeSpaceMb": "MIN_FREE_SPACE_MB", "apiKey": "API_KEY",
		"url": "URL", "flareSolverrURL": "FLARE_SOLVERR_URL", "userIds": "USER_IDS", "pageV2Size": "PAGE_V2_SIZE",
	} {
		if got := Snake(in); got != want {
			t.Errorf("Snake(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		typ  any
		in   string
		want any
	}{
		{"", " x ", "x"},
		{true, "true", true},
		{0, "42", int64(42)},
		{0.0, "0.5", 0.5},
		{[]string{}, "a, b,,c", []any{"a", "b", "c"}},
		{[]string{}, `["a,b"]`, []string{"a,b"}},
		{[]int64{}, "1,2", []any{int64(1), int64(2)}},
		{map[string]string{}, "/a=/b, /c=/d", map[string]any{"/a": "/b", "/c": "/d"}},
	}
	for _, c := range cases {
		got, err := Parse(reflect.TypeOf(c.typ), c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%T, %q) = %#v, %v; want %#v", c.typ, c.in, got, err, c.want)
		}
	}
	if _, err := Parse(reflect.TypeOf(0), "x"); err == nil {
		t.Error("expected error for bad int")
	}
}

func TestSettingsOverlay(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	st := settings.NewStore(d)
	if err := st.Warm(ctx); err != nil {
		t.Fatal(err)
	}
	// saved before the variable existed
	dl := settings.DefaultDownloads()
	dl.MaxConcurrent, dl.PageRetries = 5, 7
	if err := st.Set(ctx, settings.KeyDownloads, dl); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"MANGARR_DOWNLOADS_MAX_CONCURRENT": "2", "MANGARR_API_KEY": "abc", "MANGARR_CLEANUP_EXCLUDE_TAGS": "keep,fav"}
	if err := ApplySettings(env, st); err != nil {
		t.Fatal(err)
	}
	got, _ := st.Downloads(ctx)
	if got.MaxConcurrent != 2 || got.PageRetries != 7 {
		t.Fatalf("overlay not applied: %+v", got)
	}
	g, _ := st.General(ctx)
	c, _ := st.Cleanup(ctx)
	if g.APIKey != "abc" || strings.Join(c.ExcludeTags, ",") != "keep,fav" {
		t.Fatalf("general/cleanup overlay: %+v %+v", g, c)
	}
	if l := st.Locks(settings.KeyDownloads); len(l) != 1 || l[0].Path != "maxConcurrent" || l[0].Env != "MANGARR_DOWNLOADS_MAX_CONCURRENT" {
		t.Fatalf("locks: %+v", l)
	}
	// a UI save can't change the pinned field, and keeps the stored value underneath
	got.MaxConcurrent, got.PageRetries = 9, 1
	if err := st.Set(ctx, settings.KeyDownloads, got); err != nil {
		t.Fatal(err)
	}
	if err := ApplySettings(map[string]string{}, st); err != nil { // variable removed
		t.Fatal(err)
	}
	got, _ = st.Downloads(ctx)
	if got.MaxConcurrent != 5 || got.PageRetries != 1 {
		t.Fatalf("after removing the variable want stored 5 and edited 1, got %+v", got)
	}
	if err := ApplySettings(map[string]string{"MANGARR_DOWNLOADS_MAX_CONCURRENT": "many"}, st); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestParseModules(t *testing.T) {
	env := map[string]string{
		"MANGARR_MODULE_SUWAYOMI_IMPL":            "source/suwayomi",
		"MANGARR_MODULE_SUWAYOMI_URL":             "http://suwayomi:4567",
		"MANGARR_MODULE_SUWAYOMI_PASSWORD":        "pw",
		"MANGARR_MODULE_KOMGA_IMPL":               "komga",
		"MANGARR_MODULE_KOMGA_2_IMPL":             "library/komga",
		"MANGARR_MODULE_KOMGA_2_URL":              "http://komga2",
		"MANGARR_MODULE_KOMGA_2_PATH_MAPPINGS":    "/data=/manga",
		"MANGARR_MODULE_KOMGA_2_ENABLED":          "false",
		"MANGARR_MODULE_KOMGA_2_NAME":             "Second Komga",
		"MANGARR_MODULE_TELEGRAM_BOT_IMPL":        "notify/telegram",
		"MANGARR_MODULE_TELEGRAM_BOT_EVENTS":      "chapter.imported",
		"MANGARR_MODULE_TELEGRAM_BOT_BOT_TOKEN":   "t",
		"MANGARR_MODULE_TELEGRAM_BOT_CHAT_ID":     "1",
		"MANGARR_DOWNLOADS_MAX_CONCURRENT":        "2", // not a module variable
		"MANGARR_MODULE_TELEGRAM_BOT_DISABLE_WEB": "",  // may not exist → error expected below
	}
	delete(env, "MANGARR_MODULE_TELEGRAM_BOT_DISABLE_WEB")
	ms, err := parseModules(env)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]*envModule{}
	for _, m := range ms {
		by[m.key] = m
	}
	if len(ms) != 4 {
		t.Fatalf("want 4 modules, got %d", len(ms))
	}
	k2 := by["KOMGA_2"]
	if k2.name != "Second Komga" || *k2.lock.Name != "Second Komga" || *k2.lock.Enabled || k2.lock.Values["url"] != "http://komga2" ||
		!reflect.DeepEqual(k2.lock.Values["pathMappings"], map[string]any{"/data": "/manga"}) {
		t.Fatalf("KOMGA_2: %+v %+v", k2.lock, k2.lock.Values)
	}
	if by["SUWAYOMI"].name != "Suwayomi (Keiyoushi extensions)" || by["KOMGA"].name != "Komga" || by["TELEGRAM_BOT"].name != "Telegram Bot" || by["KOMGA"].lock.Name != nil {
		t.Fatal("default names")
	}
	for _, bad := range []map[string]string{
		{"MANGARR_MODULE_X_URL": "u"},
		{"MANGARR_MODULE_X_IMPL": "nope"},
		{"MANGARR_MODULE_X_IMPL": "suwayomi", "MANGARR_MODULE_X_COLOR": "red"},
		{"MANGARR_MODULE_X_IMPL": "suwayomi", "MANGARR_MODULE_X_ENABLED": "maybe"},
	} {
		if _, err := parseModules(bad); err == nil {
			t.Errorf("expected error for %v", bad)
		}
	}
	// the unknown-field error lists valid settings
	_, err = parseModules(map[string]string{"MANGARR_MODULE_X_IMPL": "suwayomi", "MANGARR_MODULE_X_COLOR": "red"})
	if err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("error should list valid fields: %v", err)
	}
}

// Settings fields must not collide with the reserved module suffixes.
func TestNoReservedModuleFields(t *testing.T) {
	for _, k := range []modules.Kind{modules.KindSource, modules.KindMetadata, modules.KindLibrary, modules.KindNotify, modules.KindUpscale} {
		for _, impl := range modules.Implementations(k) {
			for suffix := range moduleFields(impl) {
				for _, r := range metaSuffixes {
					if suffix == r {
						t.Errorf("%s/%s has a setting %q that clashes with MANGARR_MODULE_<NAME>_%s", k, impl.Name, suffix, r)
					}
				}
			}
		}
	}
}

func TestSyncModules(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		mods := modules.NewManager(d, nil, discardLog(), t.TempDir())
		// an instance created in the UI earlier is adopted, not duplicated
		ui := model.ProviderDefinition{Kind: "library", Implementation: "komga", Name: "Komga", Enabled: true, Priority: 25,
			Settings: map[string]any{"url": "http://old", "apiKey": "secret"}}
		if err := mods.Create(ctx, &ui); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{
			"MANGARR_MODULE_KOMGA_IMPL": "komga", "MANGARR_MODULE_KOMGA_URL": "http://komga:25600", "MANGARR_MODULE_KOMGA_SERIES_TAGS": "main",
			"MANGARR_MODULE_ANILIST_IMPL": "metadata/anilist",
		}
		if err := SyncModules(ctx, d, mods, env); err != nil {
			t.Fatal(err)
		}
		if err := mods.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		all := mods.All("")
		if len(all) != 2 {
			t.Fatalf("want 2 instances, got %d", len(all))
		}
		k, _ := mods.Get(ui.ID)
		if k.Def.ManagedBy != "env:KOMGA" || k.Def.Settings["url"] != "http://komga:25600" || k.Def.Settings["apiKey"] != "secret" || len(k.Def.Tags) != 1 {
			t.Fatalf("adopted instance: %+v", k.Def)
		}
		// UI edits can't override pinned fields; other fields stay editable
		def := k.Def
		def.Settings = map[string]any{"url": "http://ui", "apiKey": "new"}
		def.Priority = 3
		if err := mods.Update(ctx, &def); err != nil {
			t.Fatal(err)
		}
		k, _ = mods.Get(ui.ID)
		if k.Def.Settings["url"] != "http://komga:25600" || k.Def.Settings["apiKey"] != "new" || k.Def.Priority != 3 {
			t.Fatalf("update: %+v", k.Def)
		}
		if err := mods.Delete(ctx, ui.ID); err != modules.ErrManaged {
			t.Fatalf("delete managed: %v", err)
		}
		// re-sync keeps IDs; removed variables un-manage the instance
		delete(env, "MANGARR_MODULE_ANILIST_IMPL")
		if err := SyncModules(ctx, d, mods, env); err != nil {
			t.Fatal(err)
		}
		_ = mods.Reload(ctx)
		for _, l := range mods.All("") {
			switch l.Def.Implementation {
			case "komga":
				if l.Def.ID != ui.ID || l.Def.ManagedBy == "" {
					t.Fatalf("komga after resync: %+v", l.Def)
				}
			case "anilist":
				if l.Def.ManagedBy != "" {
					t.Fatalf("anilist should be unmanaged: %+v", l.Def)
				}
			}
		}
	})
}

func TestSyncRootFolders(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	if err := SyncRootFolders(ctx, d, map[string]string{RootFoldersVar: "/data/manga/en, /data/manga/ja|ja"}); err != nil {
		t.Fatal(err)
	}
	if err := SyncRootFolders(ctx, d, map[string]string{RootFoldersVar: "/data/manga/en"}); err != nil {
		t.Fatal(err)
	}
	var rows []model.RootFolder
	_ = d.NewSelect().Model(&rows).Order("path").Scan(ctx)
	if len(rows) != 2 || rows[0].ManagedBy != ManagedEnv || rows[1].ManagedBy != "" || rows[1].Language != "ja" {
		t.Fatalf("root folders: %+v", rows)
	}
	if err := SyncRootFolders(ctx, d, map[string]string{RootFoldersVar: "relative/path"}); err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestUnknownVars(t *testing.T) {
	env := map[string]string{"MANGARR_LISTEN": ":1", "MANGARR_DOWNLOADS_MAX_CONCURENT": "2", "MANGARR_DOWNLOADS_MAX_CONCURRENT": "2"}
	if u := Unknown(env); len(u) != 1 || u[0] != "MANGARR_DOWNLOADS_MAX_CONCURENT" {
		t.Fatalf("unknown: %v", u)
	}
}

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// docs/configuration.md must match the registry (regenerate with
// `go run ./cmd/mangarr env --markdown > docs/configuration.md`).
func TestConfigurationDocsUpToDate(t *testing.T) {
	want, err := os.ReadFile("../../docs/configuration.md")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	WriteMarkdown(&b)
	if b.String() != string(want) {
		t.Fatal("docs/configuration.md is stale: run `go run ./cmd/mangarr env --markdown > docs/configuration.md`")
	}
}
