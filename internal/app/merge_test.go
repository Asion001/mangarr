package app_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// Two titles in one language that are one work merge into the kept one:
// files it lacks move into its folder, the other's source and progress carry
// over, and the other title is gone.
func TestMergeSameLanguageDuplicate(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			scenario := fakesource.NewScenario("merge")
			scenario.Sources = []source.SourceInfo{{ID: "a", Name: "Site A", Lang: "ru"}, {ID: "b", Name: "Site B", Lang: "ru"}}
			scenario.AddManga(&fakesource.Manga{SourceID: "a", URL: "/solo", Title: "Поднятие уровня в одиночку"})
			scenario.AddManga(&fakesource.Manga{SourceID: "b", URL: "/solo-leveling", Title: "Solo Leveling"})
			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, "merge")
			add := func(sourceID, url, title string) *model.Series {
				t.Helper()
				s, err := e.App.Series.Add(e.Ctx, series.AddRequest{
					Sources:      []series.SourceLink{{ModuleID: mod, SourceID: sourceID, URL: url, Title: title, Lang: "ru"}},
					RootFolderID: e.RFID, Language: "ru", Monitor: model.MonitorNone, NoRefresh: true,
				})
				if err != nil {
					t.Fatal(err)
				}
				return s
			}
			keep := add("a", "/solo", "Поднятие уровня в одиночку")
			drop := add("b", "/solo-leveling", "Solo Leveling")
			now := time.Now().UTC()
			chapter := func(s *model.Series, n string, withFile bool) *model.Chapter {
				t.Helper()
				ch := &model.Chapter{SeriesID: s.ID, NumberKey: n, Monitored: true, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(ch).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				if !withFile {
					return ch
				}
				rel := "Chapter " + n + ".cbz"
				dir := filepath.Join(e.Root, s.Path)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, rel), []byte(s.Title+" "+n), 0o644); err != nil {
					t.Fatal(err)
				}
				f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: s.ID, RelativePath: rel, Size: 1, Format: "cbz", ImportedAt: now}
				if _, err := e.App.DB.NewInsert().Model(f).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				ch.FileID, ch.State = &f.ID, model.ChapterImported
				if _, err := e.App.DB.NewUpdate().Model(ch).WherePK().Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				return ch
			}
			chapter(keep, "1", true)
			keep2 := chapter(keep, "2", false)
			chapter(drop, "1", true) // keep has it: stays and goes to the recycle bin
			chapter(drop, "2", true)
			drop3 := chapter(drop, "3", true)
			chapter(drop, "4", false)
			reader := &model.User{Username: "ann", CountForCleanup: true, CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			read := &model.TitleReadState{UserID: reader.ID, TitleID: drop.WorkID, NumberKey: "3", ChapterID: drop3.ID, Completed: true, SyncedAt: now, Origin: "web"}
			if _, err := e.App.DB.NewInsert().Model(read).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}

			if err := e.App.Series.Merge(e.Ctx, keep.ID, drop.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := e.App.Series.Get(e.Ctx, drop.ID); err == nil {
				t.Fatal("the merged title is still there")
			}
			var chapters []model.Chapter
			if err := e.App.DB.NewSelect().Model(&chapters).Where("series_id = ?", keep.ID).Order("number_key").Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, ch := range chapters {
				got[ch.NumberKey] = ch.FileID != nil
			}
			if len(chapters) != 3 || !got["1"] || !got["2"] || !got["3"] {
				t.Fatalf("chapters after merge: %+v", got)
			}
			var f2 model.ChapterFile
			if err := e.App.DB.NewSelect().Model(&f2).Where("chapter_id = ?", keep2.ID).Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(e.Root, keep.Path, f2.RelativePath))
			if err != nil || string(data) != "Solo Leveling 2" {
				t.Fatalf("chapter 2's file: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(e.Root, keep.Path, "Chapter 3.cbz")); err != nil {
				t.Fatal("chapter 3's file did not move:", err)
			}
			if _, err := os.Stat(filepath.Join(e.Root, drop.Path)); !os.IsNotExist(err) {
				t.Fatal("the merged title's folder was not recycled")
			}
			sources, err := e.App.DB.NewSelect().Model((*model.SeriesSource)(nil)).Where("series_id = ?", keep.ID).Count(e.Ctx)
			if err != nil || sources != 2 {
				t.Fatalf("sources after merge: %d %v", sources, err)
			}
			n, err := e.App.DB.NewSelect().Model((*model.TitleReadState)(nil)).
				Where("title_id = ? AND number_key = ? AND completed", keep.WorkID, "3").Count(e.Ctx)
			if err != nil || n != 1 {
				t.Fatalf("progress did not carry over: %d %v", n, err)
			}
		})
	}
}
