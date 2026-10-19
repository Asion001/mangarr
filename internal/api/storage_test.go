package api_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

func TestStorageSummary(t *testing.T) {
	srv, a := newServer(t, true)
	ctx := context.Background()
	now := time.Now().UTC()
	insert := func(v any) {
		t.Helper()
		if _, err := a.DB.NewInsert().Model(v).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	en := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: now}
	ru := &model.RootFolder{Path: t.TempDir(), Language: "ru", CreatedAt: now}
	insert(en)
	insert(ru)
	readerID, err := a.Reading.ReaderID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// title, root, status, preview, file sizes (original, size)
	seed := []struct {
		title   string
		root    int64
		status  string
		preview bool
		files   [][2]int64
	}{
		{"Big", en.ID, model.StatusOngoing, false, [][2]int64{{500, 300}, {500, 400}}},
		{"Small", ru.ID, model.StatusCompleted, false, [][2]int64{{0, 50}}},
		{"Empty", ru.ID, model.StatusCompleted, false, nil},
		{"Previewed", en.ID, model.StatusOngoing, true, [][2]int64{{0, 9000}}},
	}
	var firstChapter int64
	for i, sd := range seed {
		ser := &model.Series{Title: sd.title, SortTitle: sd.title, Status: sd.status, Preview: sd.preview, RootFolderID: sd.root, ProfileID: 1,
			Path: sd.title, Language: "en", Tags: []int64{}, AddedAt: now, UpdatedAt: now}
		insert(ser)
		for j, f := range sd.files {
			ch := &model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(j), NumberSort: float64(j), FirstSeenAt: now, UpdatedAt: now}
			insert(ch)
			insert(&model.ChapterFile{ChapterID: ch.ID, SeriesID: ser.ID, RelativePath: fmt.Sprintf("%d-%d.cbz", i, j),
				SizeOriginal: f[0], Size: f[1], PageCount: 10, ImportedAt: now})
			if firstChapter == 0 {
				firstChapter = ch.ID
				st := model.ChapterReadState{ReaderID: readerID, SeriesID: ser.ID, ChapterID: ch.ID, Completed: true, ReadAt: &now, SyncedAt: now}
				if err := readstate.Save(ctx, a.DB, &st); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	insert(&model.RecycledFile{Kind: "file", Reason: "cleanup", SeriesTitle: "Big", OriginalRelativePath: "x.cbz", RecycledPath: "x.cbz", Size: 77, RecycledAt: now})

	var got api.StorageSummary
	if code := (caller{t, http.DefaultClient, srv.URL}).do("GET", "/api/v1/system/storage", "", &got); code != 200 {
		t.Fatalf("status %d", code)
	}
	lib := got.Library
	if lib.Series != 3 || lib.Files != 3 || lib.Bytes != 750 || lib.Pages != 30 || lib.Saved != 300 {
		t.Fatalf("library (previews left out): %+v", lib)
	}
	if len(got.Roots) != 2 {
		t.Fatalf("roots: %+v", got.Roots)
	}
	for _, r := range got.Roots {
		want := map[int64][3]int64{en.ID: {1, 2, 700}, ru.ID: {2, 1, 50}}[r.ID]
		if int64(r.Series) != want[0] || int64(r.Files) != want[1] || r.Bytes != want[2] {
			t.Fatalf("root %s: %+v", r.Path, r)
		}
		if r.Error != "" || r.DriveTotal == 0 || r.DriveFree > r.DriveTotal || r.Drive == "" {
			t.Fatalf("drive of %s: %+v", r.Path, r)
		}
	}
	if len(got.Largest) != 2 || got.Largest[0].Title != "Big" || got.Largest[0].Bytes != 700 || got.Largest[1].Title != "Small" {
		t.Fatalf("largest (empty series left out): %+v", got.Largest)
	}
	if len(got.ByStatus) != 2 || got.ByStatus[0] != (api.StorageStatus{Status: model.StatusOngoing, Series: 1, Bytes: 700}) ||
		got.ByStatus[1] != (api.StorageStatus{Status: model.StatusCompleted, Series: 2, Bytes: 50}) {
		t.Fatalf("by status: %+v", got.ByStatus)
	}
	if got.ReadFiles != 1 || got.ReadBytes != 300 {
		t.Fatalf("read: %d files, %d bytes", got.ReadFiles, got.ReadBytes)
	}
	if got.RecycleFiles != 1 || got.RecycleBytes != 77 {
		t.Fatalf("recycle bin: %d files, %d bytes", got.RecycleFiles, got.RecycleBytes)
	}
}
