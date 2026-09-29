package app_test

import (
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
)

// TestEditionProgress checks that finishing or unreading a chapter in one
// language edition does the same in the title's other editions, by chapter
// number, while page progress stays with its edition.
func TestEditionProgress(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			work := &model.Work{Title: "Abyss", SortTitle: "abyss", CreatedAt: now, UpdatedAt: now}
			other := &model.Work{Title: "Other", SortTitle: "other", CreatedAt: now, UpdatedAt: now}
			for _, w := range []*model.Work{work, other} {
				if _, err := e.App.DB.NewInsert().Model(w).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
			}
			chapters := map[string]map[float64]int64{}
			add := func(name, lang string, workID int64, numbers ...float64) *model.Series {
				ser := &model.Series{WorkID: workID, Title: name, SortTitle: name, Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
					RootFolderID: e.RFID, Path: name, ProfileID: 1, Language: lang, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				chapters[name] = map[float64]int64{}
				for _, n := range numbers {
					key := strconv.FormatFloat(n, 'f', -1, 64)
					c := &model.Chapter{SeriesID: ser.ID, NumberKey: key, NumberSort: n, Title: "Ch " + key, Monitored: true,
						State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
					if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
						t.Fatal(err)
					}
					chapters[name][n] = c.ID
				}
				return ser
			}
			en := add("Abyss EN", "en", work.ID, 1, 2, 3, 4)
			ru := add("Abyss RU", "ru", work.ID, 1, 2, 3)
			add("Other", "en", other.ID, 1)
			reader := &model.Reader{Name: "ann", CountForCleanup: true, CreatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(reader).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			state := func(series string, n float64) *model.ChapterReadState {
				var st model.ChapterReadState
				if err := e.App.DB.NewSelect().Model(&st).Where("reader_id = ? AND chapter_id = ?", reader.ID, chapters[series][n]).Scan(e.Ctx); err != nil {
					return nil
				}
				return &st
			}
			by := reading.By{Origin: model.EventOriginApp, Client: "test"}
			record := func(c reading.Change) []reading.Outcome {
				t.Helper()
				out, err := e.App.Reading.Record(e.Ctx, reader.ID, []reading.Change{c}, by)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}

			// finishing English 1 finishes Russian 1, and only that
			out := record(reading.Change{ChapterID: chapters["Abyss EN"][1], SeriesID: en.ID, Completed: true})
			if len(out) != 1 {
				t.Fatalf("outcomes %+v: mirrored changes aren't the caller's", out)
			}
			if st := state("Abyss RU", 1); st == nil || !st.Completed || st.SeriesID != ru.ID {
				t.Fatalf("russian 1 = %+v, want read", st)
			}
			if st := state("Other", 1); st != nil {
				t.Fatalf("another title's chapter 1 = %+v, want untouched", st)
			}
			var events int
			events, _ = e.App.DB.NewSelect().Model((*model.ReadEvent)(nil)).Where("reader_id = ?", reader.ID).Count(e.Ctx)
			if events != 1 {
				t.Fatalf("%d read events, want only the reported one", events)
			}

			// a page in Russian 2 stays there
			record(reading.Change{ChapterID: chapters["Abyss RU"][2], SeriesID: ru.ID, Page: 5})
			if st := state("Abyss EN", 2); st != nil {
				t.Fatalf("english 2 = %+v, want no page progress", st)
			}
			// finishing it finishes English 2
			record(reading.Change{ChapterID: chapters["Abyss RU"][2], SeriesID: ru.ID, Completed: true})
			if st := state("Abyss EN", 2); st == nil || !st.Completed {
				t.Fatalf("english 2 = %+v, want read", st)
			}

			// unreading English 1 unreads Russian 1
			record(reading.Change{ChapterID: chapters["Abyss EN"][1], SeriesID: en.ID, Unread: true})
			if st := state("Abyss RU", 1); st != nil {
				t.Fatalf("russian 1 = %+v, want unread", st)
			}

			// progress from before grouping (or a chapter that reached one
			// edition later) is caught up, once
			read := now.Add(-time.Hour)
			for _, n := range []float64{3, 4} {
				st := &model.ChapterReadState{ReaderID: reader.ID, ChapterID: chapters["Abyss EN"][n], SeriesID: en.ID, Completed: true, ReadAt: &read, SyncedAt: read}
				if _, err := e.App.DB.NewInsert().Model(st).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
			}
			if n, err := e.App.Reading.CatchUpEditions(e.Ctx, 0); err != nil || n != 1 {
				t.Fatalf("caught up %d, %v; want 1", n, err)
			}
			if st := state("Abyss RU", 3); st == nil || !st.Completed {
				t.Fatalf("russian 3 = %+v, want read", st)
			}
			if n, err := e.App.Reading.CatchUpEditions(e.Ctx, work.ID); err != nil || n != 0 {
				t.Fatalf("second catch-up %d, %v; want 0", n, err)
			}
		})
	}
}
