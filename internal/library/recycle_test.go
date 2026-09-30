package library

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/cbz"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/settings"
)

func recycleFixture(t *testing.T, d *db.DB) (*Library, *model.Series, *model.ChapterFile, string) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	profile := &model.Profile{Name: "Archive profile", CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{root, profile} {
		if _, err := d.NewInsert().Model(row).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	series := &model.Series{Title: "Silver Lanterns", SortTitle: "silver lanterns", Path: "Silver Lanterns", RootFolderID: root.ID, ProfileID: profile.ID, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(series).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ch := &model.Chapter{SeriesID: series.ID, NumberKey: "1", NumberSort: 1, State: model.ChapterImported, FirstSeenAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(ch).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	lib := New(d, settings.NewStore(d), http.DefaultClient, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	dir, err := lib.EnsureSeriesDir(ctx, series)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Silver Lanterns - Chapter 1.cbz")
	writeRecycleArchive(t, path, 2)
	f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: series.ID, RelativePath: filepath.Base(path), ImportedAt: now, SourceName: "Paper Source", Scanlator: "Lantern Group", ProcessParams: "original-settings"}
	if err := measureFile(path, f); err != nil {
		t.Fatal(err)
	}
	if _, err := d.NewInsert().Model(f).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.NewUpdate().Model(ch).Set("file_id = ?", f.ID).WherePK().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return lib, series, f, path
}

func writeRecycleArchive(t *testing.T, path string, count int) {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	pages := []cbz.Page{}
	for i := 0; i < count; i++ {
		pages = append(pages, cbz.Page{Name: cbz.PageName(i, ".png"), Data: b.Bytes()})
	}
	if _, err := cbz.Write(path, pages, nil, 0o644, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRecycleRetentionAndRescan(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		l, s, f, path := recycleFixture(t, d)
		ctx := t.Context()
		old := time.Now().Add(-60 * 24 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		dst, err := l.Recycle(ctx, path, s.Path, true, RecycleInfo{Series: s, File: f, Reason: "reprocessed"})
		if err != nil {
			t.Fatal(err)
		}
		dst2, err := l.Recycle(ctx, path, s.Path, true, RecycleInfo{Series: s, File: f, Reason: "upgraded"})
		if err != nil || dst == dst2 {
			t.Fatalf("collision %s %s %v", dst, dst2, err)
		}
		if n, err := l.PurgeRecycleBin(ctx); err != nil || n != 0 {
			t.Fatalf("purged recent hardlink: %d %v", n, err)
		}
		st, err := os.Stat(path)
		if err != nil || !st.ModTime().Equal(old) {
			t.Fatalf("source mtime changed: %v", err)
		}
		var rows []model.RecycledFile
		if err := d.NewSelect().Model(&rows).Order("id").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].ChapterID == nil || *rows[0].ChapterID != f.ChapterID || rows[0].Reason != "reprocessed" {
			t.Fatalf("metadata: %+v", rows)
		}
		if _, err := d.NewUpdate().Model(&rows[0]).Set("recycled_at = ?", old).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		mm := settings.DefaultMediaManagement()
		mm.RecycleBinDays = 0
		if err := l.settings.Set(ctx, settings.KeyMediaManagement, mm); err != nil {
			t.Fatal(err)
		}
		if n, err := l.PurgeRecycleBin(ctx); err != nil || n != 0 {
			t.Fatalf("forever: %d %v", n, err)
		}
		mm.RecycleBinDays = 7
		_ = l.settings.Set(ctx, settings.KeyMediaManagement, mm)
		if n, err := l.PurgeRecycleBin(ctx); err != nil || n != 1 {
			t.Fatalf("expired: %d %v", n, err)
		}
		if _, err := os.Stat(dst); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("expired bytes remain")
		}
		legacy := filepath.Join(l.RecycleDir(ctx), s.Path, time.Now().UTC().Format("20060102-150405")+"-"+f.RelativePath)
		if err := os.Link(path, legacy); err != nil {
			t.Fatal(err)
		}
		if n, err := l.RescanRecycleBin(ctx); err != nil || n != 1 {
			t.Fatalf("rescan: %d %v", n, err)
		}
		if n, err := l.RescanRecycleBin(ctx); err != nil || n != 0 {
			t.Fatalf("duplicate rescan: %d %v", n, err)
		}
		if n, err := l.PurgeRecycleBin(ctx); err != nil || n != 0 {
			t.Fatalf("legacy hardlink: %d %v", n, err)
		}
		if err := os.Remove(legacy); err != nil {
			t.Fatal(err)
		}
		if _, err := l.RescanRecycleBin(ctx); err != nil {
			t.Fatal(err)
		}
		n, err := d.NewSelect().Model((*model.RecycledFile)(nil)).Count(ctx)
		if err != nil || n != 1 {
			t.Fatalf("stale row: %d %v", n, err)
		}
	})
}

func TestRestoreRecycledPreservesIdentityAndProgress(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		l, s, f, path := recycleFixture(t, d)
		ctx := t.Context()
		if _, err := l.Recycle(ctx, path, s.Path, true, RecycleInfo{Series: s, File: f, Reason: "reprocessed"}); err != nil {
			t.Fatal(err)
		}
		var r model.RecycledFile
		if err := d.NewSelect().Model(&r).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		writeRecycleArchive(t, path, 6)
		f.PageCount = 6
		f.SourcePages = []int{0, 0, 0, 1, 1, 1}
		f.Upscaled = true
		if _, err := d.NewUpdate().Model(f).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, complete := range []bool{false, true} {
			reader := &model.Reader{Name: fmt.Sprint("Archive reader ", complete), CreatedAt: time.Now()}
			if _, err := d.NewInsert().Model(reader).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			state := &model.ChapterReadState{ReaderID: reader.ID, ChapterID: f.ChapterID, SeriesID: s.ID, Page: 4, Completed: complete, SyncedAt: time.Now()}
			if err := readstate.Save(ctx, d, state); err != nil {
				t.Fatal(err)
			}
		}
		sid, err := l.RestoreRecycled(ctx, r.ID)
		if err != nil || sid != s.ID {
			t.Fatalf("restore: %d %v", sid, err)
		}
		var got model.ChapterFile
		if err := d.NewSelect().Model(&got).Where("chapter_id = ?", f.ChapterID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if got.ID != f.ID || got.PageCount != 2 || got.Upscaled || got.ProcessParams != "original-settings" {
			t.Fatalf("restored metadata: %+v", got)
		}
		entries, err := cbz.List(path)
		if err != nil || len(entries) != 2 {
			t.Fatalf("restored bytes: %d %v", len(entries), err)
		}
		var states []model.ChapterReadState
		if err := d.NewSelect().Model(&states).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		for _, state := range states {
			if state.Page != 2 {
				t.Fatalf("progress: %+v", state)
			}
		}
		var replaced model.RecycledFile
		if err := d.NewSelect().Model(&replaced).Where("reason = ?", "restored_over").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if replaced.PageCount != 6 {
			t.Fatalf("replacement: %+v", replaced)
		}
		if err := l.DeleteRecycled(ctx, replaced.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRecycleCleanedFolderAndBusy(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		l, s, f, path := recycleFixture(t, d)
		ctx := t.Context()
		if _, err := l.Recycle(ctx, path, s.Path, false, RecycleInfo{Series: s, File: f, Reason: "cleaned"}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewDelete().Model(f).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.Chapter)(nil)).Set("file_id = NULL").Where("id = ?", f.ChapterID).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		var r model.RecycledFile
		if err := d.NewSelect().Model(&r).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		job := &model.DownloadJob{SeriesID: s.ID, ChapterID: f.ChapterID, Kind: model.JobKindReprocess, Status: model.JobQueued, RecycledFileID: &r.ID, CreatedAt: now, UpdatedAt: now, NotBefore: now}
		if _, err := d.NewInsert().Model(job).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := l.DeleteRecycled(ctx, r.ID); !errors.Is(err, ErrRecycleConflict) {
			t.Fatalf("delete busy: %v", err)
		}
		if _, err := l.RestoreRecycled(ctx, r.ID); !errors.Is(err, ErrRecycleConflict) {
			t.Fatalf("restore busy: %v", err)
		}
		if _, err := d.NewDelete().Model(job).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := l.RestoreRecycled(ctx, r.ID); err != nil {
			t.Fatal(err)
		}
		var restored model.ChapterFile
		if err := d.NewSelect().Model(&restored).Where("chapter_id = ?", f.ChapterID).Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if restored.PageCount != 2 {
			t.Fatal(restored)
		}
		dir := filepath.Dir(path)
		if _, err := l.Recycle(ctx, dir, "", false, RecycleInfo{Series: s, Reason: "series_deleted"}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewDelete().Model(s).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		r = model.RecycledFile{}
		if err := d.NewSelect().Model(&r).Where("kind = ?", "folder").Scan(ctx); err != nil {
			t.Fatal(err)
		}
		if r.SeriesID != nil || r.FolderSnapshot == nil {
			t.Fatalf("snapshot: %+v", r)
		}
		sid, err := l.RestoreRecycled(ctx, r.ID)
		if err != nil || sid == 0 {
			t.Fatalf("folder restore: %d %v", sid, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
		var files []model.ChapterFile
		if err := d.NewSelect().Model(&files).Where("series_id = ?", sid).Scan(ctx); err != nil || len(files) != 1 || files[0].ProcessParams != "original-settings" {
			t.Fatalf("folder files: %+v %v", files, err)
		}
	})
}

func TestRecycleRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"../escape", "/absolute", "."} {
		if _, err := safeRecyclePath(root, rel); err == nil {
			t.Fatalf("accepted %s", rel)
		}
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := safeRecyclePath(root, "link/chapter.cbz"); err == nil {
		t.Fatal("accepted symlink")
	}
}
