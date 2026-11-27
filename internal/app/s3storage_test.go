package app_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	apiserver "github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/libfs"
	"github.com/Asion001/mangarr/internal/libfs/libfstest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// newS3TestApp starts an app whose library is on a fake bucket, with one
// root folder that never exists on the disk.
func newS3TestApp(t *testing.T) (*testEnv, *libfstest.Server) {
	t.Helper()
	dsn := dbtest.DSNs(t)["sqlite"]
	srv := libfstest.NewServer(t)
	t.Cleanup(func() { libfs.Use(nil) })

	dir := t.TempDir()
	s3cfg := srv.Config(t)
	cfg := &config.Config{Listen: ":0", DataDir: filepath.Join(dir, "config"), DB: dsn, LogLevel: "error", Storage: &s3cfg}
	_, ring := logging.Setup("error", io.Discard)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a, err := app.New(ctx, cfg, log, ring)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); time.Sleep(50 * time.Millisecond); _ = a.Close() })
	dl := settings.DefaultDownloads()
	dl.MaxAttempts = 1
	_ = a.Settings.Set(ctx, settings.KeyDownloads, dl)
	src := settings.DefaultSources()
	src.Throttle = model.ThrottleConfig{Preset: "fast"}
	_ = a.Settings.Set(ctx, settings.KeySources, src)
	a.Rescanner.Quiet = 100 * time.Millisecond
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "manga") // never created on the disk
	rf := &model.RootFolder{Path: root, Language: "en", CreatedAt: time.Now().UTC()}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	e := &testEnv{App: a, Ctx: ctx, Root: root, RFID: rf.ID}
	return e, srv
}

// TestLibraryOnObjectStorage runs downloads, an upgrade (which recycles the
// old file), reading and a folder rename with the library in a bucket: no
// library file may land on the local disk.
func TestLibraryOnObjectStorage(t *testing.T) {
	e, srv := newS3TestApp(t)
	a, ctx, root, rf, dir := e.App, e.Ctx, e.Root, &model.RootFolder{ID: e.RFID}, filepath.Dir(e.Root)

	sc := fakesource.NewScenario("s3-storage")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	t0 := time.Now().Add(-48 * time.Hour)
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/a/1", Title: "Cloud Series", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/a/1/c1", Name: "Chapter 1", Number: 1, Scanlator: "Random", Uploaded: t0},
		{URL: "/a/1/c2", Name: "Chapter 2", Number: 2, Scanlator: "Random", Uploaded: t0},
	}})
	mod := e.addFakeModule(t, "s3-storage")
	ser, err := a.Series.Add(ctx, series.AddRequest{Title: "Cloud Series", RootFolderID: rf.ID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/a/1", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "two imported chapters", func() bool { return len(e.chapterFiles(t, ser.ID)) == 2 })

	files := e.chapterFiles(t, ser.ID)
	path1 := filepath.Join(root, "Cloud Series", files["1"].RelativePath)
	if !strings.Contains(strings.Join(srv.Keys(), "\n"), libfs.Key(path1)) {
		t.Fatalf("%s is not in the bucket: %v", libfs.Key(path1), srv.Keys())
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("the library folder appeared on the disk: %v", err)
	}
	pages, ci, err := cbz.Read(path1)
	if err != nil || len(pages) != 3 || len(ci) == 0 {
		t.Fatalf("bad cbz in the bucket: pages=%d ci=%d err=%v", len(pages), len(ci), err)
	}
	entries, err := cbz.Entries(path1)
	if err != nil || len(entries) != 3 {
		t.Fatalf("entries: %d %v", len(entries), err)
	}
	rc, size, err := cbz.OpenPage(path1, entries[1])
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(rc)
	rc.Close()
	if int64(len(data)) != size || string(data) != string(pages[1].Data) {
		t.Fatalf("page 2 read by range: %d bytes of %d", len(data), size)
	}

	// a whole-chapter download is sent to a signed link on the bucket
	api := httptest.NewServer(apiserver.New(a))
	defer api.Close()
	general, _ := a.Settings.General(ctx)
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/read/chapters/%d/file", api.URL, files["1"].ChapterID), nil)
	req.Header.Set("X-Api-Key", general.APIKey)
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.HasPrefix(loc, srv.URL+"/") || !strings.Contains(loc, "X-Amz-Signature=") {
		t.Fatalf("download: %d %q", resp.StatusCode, loc)
	}
	got, err := http.Get(loc)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if want, _ := libfs.ReadFile(path1); string(body) != string(want) {
		t.Fatalf("the link served %d bytes, want %d", len(body), len(want))
	}

	// an upgrade puts the old file in the recycle bin, also in the bucket
	var prof model.Profile
	_ = a.DB.NewSelect().Model(&prof).Where("id = ?", ser.ProfileID).Scan(ctx)
	prof.Config.AllowUpgrades = true
	prof.Config.PreferredScanlators = []string{"^Official$"}
	if _, err := a.DB.NewUpdate().Model(&prof).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	sc.Update(func() {
		m := sc.Mangas["A|/a/1"]
		m.Chapters = append(m.Chapters, fakesource.Chapter{URL: "/a/1/c1-official", Name: "Chapter 1", Number: 1, Scanlator: "Official", Uploaded: time.Now()})
	})
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "upgrade", func() bool { return e.chapterFiles(t, ser.ID)["1"].Scanlator == "Official" })
	var recycled []model.RecycledFile
	_ = a.DB.NewSelect().Model(&recycled).Scan(ctx)
	if len(recycled) != 1 {
		t.Fatalf("recycled: %+v", recycled)
	}
	if p := filepath.Join(a.Library.RecycleDir(ctx), recycled[0].RecycledPath); !libfs.Exists(p) || libfs.IsLocal(p) {
		t.Fatalf("recycled: %+v", recycled)
	}

	// moving the series to another root folder moves its files within the
	// bucket, and renaming them renames objects
	root2 := filepath.Join(dir, "manga-ja")
	rf2 := &model.RootFolder{Path: root2, Language: "ja", CreatedAt: time.Now().UTC()}
	if _, err := a.DB.NewInsert().Model(rf2).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "MoveSeries", map[string]any{"seriesId": ser.ID, "rootFolderId": rf2.ID, "moveFiles": true})
	mm, _ := a.Settings.MediaManagement(ctx)
	mm.ChapterFormat = "{Series Title} - {Chapter:000}"
	_ = a.Settings.Set(ctx, settings.KeyMediaManagement, mm)
	e.runCommand(t, "RenameFiles", map[string]any{"seriesIds": []any{ser.ID}})
	if libfs.Exists(path1) {
		t.Fatal("the old file is still in the bucket")
	}
	for n, f := range e.chapterFiles(t, ser.ID) {
		p := filepath.Join(root2, "Cloud Series", f.RelativePath)
		if f.RelativePath != "Cloud Series - 00"+n+".cbz" || !libfs.Exists(p) {
			t.Fatalf("chapter %s: %q, there: %v", n, f.RelativePath, libfs.Exists(p))
		}
	}
	for _, d := range []string{root, root2} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("a library folder appeared on the disk: %v", err)
		}
	}
}

// TestMigrateStorage copies a library from the disk into a bucket, twice
// (the second run has nothing to copy), and checks every file arrived.
func TestMigrateStorage(t *testing.T) {
	dsn := dbtest.DSNs(t)["sqlite"]
	sc := fakesource.NewScenario("s3-migrate")
	sc.Sources = []source.SourceInfo{{ID: "A", Name: "Source A", Lang: "en"}}
	sc.AddManga(&fakesource.Manga{SourceID: "A", URL: "/a/1", Title: "Local Series", Status: source.StatusOngoing, Chapters: []fakesource.Chapter{
		{URL: "/a/1/c1", Name: "Chapter 1", Number: 1, Uploaded: time.Now().Add(-time.Hour)},
		{URL: "/a/1/c2", Name: "Chapter 2", Number: 2, Uploaded: time.Now().Add(-time.Hour)},
	}})
	e := newTestApp(t, dsn)
	mod := e.addFakeModule(t, "s3-migrate")
	ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Local Series", RootFolderID: e.RFID, Monitor: model.MonitorAll, SearchMissing: true,
		Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/a/1", SourceName: "Source A", Lang: "en"}}})
	if err != nil {
		t.Fatal(err)
	}
	e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
	waitFor(t, 20*time.Second, "two imported chapters", func() bool { return len(e.chapterFiles(t, ser.ID)) == 2 })
	e.waitIdle(t)

	srv := libfstest.NewServer(t)
	s3cfg := srv.Config(t)
	cfg := *e.App.Cfg
	cfg.Storage = &s3cfg
	var out strings.Builder
	if err := app.MigrateStorage(e.Ctx, &cfg, app.MigrateOptions{DryRun: true}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if len(srv.Keys()) != 0 {
		t.Fatalf("a dry run copied: %v", srv.Keys())
	}
	out.Reset()
	if err := app.MigrateStorage(e.Ctx, &cfg, app.MigrateOptions{}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "all 2 chapter files the database knows are in the bucket") {
		t.Fatalf("check: %s", out.String())
	}
	for _, f := range e.chapterFiles(t, ser.ID) {
		local := filepath.Join(e.Root, "Local Series", f.RelativePath)
		if !strings.Contains(strings.Join(srv.Keys(), "\n"), libfs.Key(local)) {
			t.Fatalf("%s not copied: %v", local, srv.Keys())
		}
		if _, err := os.Stat(local); err != nil {
			t.Fatalf("the local file is gone: %v", err)
		}
	}
	puts := srv.Requests("PUT")
	out.Reset()
	if err := app.MigrateStorage(e.Ctx, &cfg, app.MigrateOptions{}, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if srv.Requests("PUT") != puts+1 { // the probe's
		t.Fatalf("the second run uploaded again: %d PUTs\n%s", srv.Requests("PUT")-puts, out.String())
	}
}
