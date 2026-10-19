package api_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/settings"
)

// TestCleanupPlanEditions: progress is shared by a title's editions, but
// cleanup only removes chapters of the edition they were read in, leaves a
// chapter alone while a job works on it, and counts every reader when none
// is marked as counting for cleanup.
func TestCleanupPlanEditions(t *testing.T) {
	_, a := newServer(t, true)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-30 * 24 * time.Hour)
	insert := func(v any) {
		t.Helper()
		if _, err := a.DB.NewInsert().Model(v).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	insert(root)
	work := &model.Work{Title: "Shared", SortTitle: "shared", CreatedAt: now, UpdatedAt: now}
	insert(work)
	chapterIDs, seriesIDs := map[string][]int64{}, map[string]int64{}
	for _, lang := range []string{"en", "ru"} {
		ser := &model.Series{WorkID: work.ID, Title: "Shared " + lang, SortTitle: "shared " + lang, Status: model.StatusOngoing, RootFolderID: root.ID,
			ProfileID: 1, Path: "Shared " + lang, Language: lang, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
		insert(ser)
		seriesIDs[lang] = ser.ID
		for i := 1; i <= 4; i++ {
			ch := &model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(i), NumberSort: float64(i), State: model.ChapterImported, FirstSeenAt: now, UpdatedAt: now}
			insert(ch)
			f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: ser.ID, RelativePath: fmt.Sprintf("%d.cbz", i), Size: 100, ImportedAt: now}
			insert(f)
			if _, err := a.DB.NewUpdate().Model(ch).Set("file_id = ?", f.ID).WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
			chapterIDs[lang] = append(chapterIDs[lang], ch.ID)
		}
	}
	// one reader, not marked as counting for cleanup, read 1-4 in English
	reader := &model.Reader{Name: "only", CreatedAt: now}
	insert(reader)
	for _, id := range chapterIDs["en"] {
		var ch model.Chapter
		_ = a.DB.NewSelect().Model(&ch).Where("id = ?", id).Scan(ctx)
		st := model.ChapterReadState{ReaderID: reader.ID, SeriesID: ch.SeriesID, ChapterID: id, Completed: true, ReadAt: &old, SyncedAt: old}
		if err := readstate.Save(ctx, a.DB, &st); err != nil {
			t.Fatal(err)
		}
	}
	// chapter 2 is being re-encoded
	insert(&model.DownloadJob{Kind: model.JobKindReprocess, SeriesID: seriesIDs["en"], ChapterID: chapterIDs["en"][1], Status: model.JobQueued, NotBefore: now, CreatedAt: now, UpdatedAt: now})
	cs := settings.DefaultCleanup()
	cs.Enabled, cs.KeepLastRead, cs.GraceDays = true, 1, 7
	if err := a.Settings.Set(ctx, settings.KeyCleanup, cs); err != nil {
		t.Fatal(err)
	}

	plan, err := a.Cleaner.Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, cd := range plan.Candidates {
		got = append(got, cd.SeriesTitle+" "+cd.Chapter)
	}
	// English 1-4 read, keep the last (4), chapter 2 busy → 1 and 3; nothing Russian
	if fmt.Sprint(got) != "[Shared en 1 Shared en 3]" {
		t.Fatalf("candidates: %v", got)
	}
	var ruSkip string
	for _, s := range plan.Skipped {
		if s.SeriesTitle == "Shared ru" {
			ruSkip = s.Code
		}
	}
	if ruSkip != "no-reader" {
		t.Fatalf("the Russian edition was never opened, skip %q: %+v", ruSkip, plan.Skipped)
	}
}
