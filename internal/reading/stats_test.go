package reading

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

func TestStatsScopesAndAggregates(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, database *db.DB) {
		ctx := context.Background()
		now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
		visibleRoot := &model.RootFolder{Path: "/visible", Language: "en", CreatedAt: now}
		hiddenRoot := &model.RootFolder{Path: "/hidden", Language: "uk", CreatedAt: now}
		profile := &model.Profile{Name: "Default", IsDefault: true, Config: model.ProfileConfig{}, CreatedAt: now, UpdatedAt: now}
		for _, item := range []any{visibleRoot, hiddenRoot, profile} {
			if _, err := database.NewInsert().Model(item).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		addSeries := func(title, language, genre string, rootID int64, chapters int) (*model.Series, []*model.Chapter) {
			t.Helper()
			ser := &model.Series{Title: title, SortTitle: title, Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
				RootFolderID: rootID, Path: title, ProfileID: profile.ID, Language: language, SourcePriorityMode: "custom",
				ReadingDirection: "rtl", Tags: []int64{}, Metadata: model.SeriesMetadata{Genres: []string{genre}}, AddOptions: model.AddOptions{},
				BlockedScanlators: []string{}, AddedAt: now, UpdatedAt: now}
			if _, err := database.NewInsert().Model(ser).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			out := make([]*model.Chapter, 0, chapters)
			for i := 1; i <= chapters; i++ {
				chapter := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(i), NumberSort: float64(i), Monitored: true,
					State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
				if _, err := database.NewInsert().Model(chapter).Exec(ctx); err != nil {
					t.Fatal(err)
				}
				out = append(out, chapter)
			}
			return ser, out
		}
		english, englishChapters := addSeries("English", "en", "Fantasy", visibleRoot.ID, 2)
		russian, russianChapters := addSeries("Russian", "ru", "Sports", visibleRoot.ID, 3)
		hidden, hiddenChapters := addSeries("Hidden", "uk", "Horror", hiddenRoot.ID, 1)
		reader := &model.Reader{Name: "Reader", CreatedAt: now}
		if _, err := database.NewInsert().Model(reader).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, pair := range []struct {
			series  *model.Series
			chapter *model.Chapter
		}{
			{english, englishChapters[0]}, {english, englishChapters[1]},
			{russian, russianChapters[0]}, {russian, russianChapters[1]}, {russian, russianChapters[2]},
			{hidden, hiddenChapters[0]},
		} {
			state := &model.ChapterReadState{ReaderID: reader.ID, SeriesID: pair.series.ID, ChapterID: pair.chapter.ID,
				Completed: true, ReadAt: &now, SyncedAt: now}
			if err := readstate.Save(ctx, database, state); err != nil {
				t.Fatal(err)
			}
		}
		for _, session := range []*model.ReadingSession{
			{ID: "stats-english-jan", ReaderID: reader.ID, SeriesID: english.ID, ChapterID: englishChapters[0].ID, ActiveSeconds: 120,
				StartedAt: time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 1, 2, 0, 2, 0, 0, time.UTC)},
			{ID: "stats-english-feb", ReaderID: reader.ID, SeriesID: english.ID, ChapterID: englishChapters[1].ID, ActiveSeconds: 60,
				StartedAt: time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 2, 2, 0, 1, 0, 0, time.UTC)},
			{ID: "stats-russian-feb", ReaderID: reader.ID, SeriesID: russian.ID, ChapterID: russianChapters[0].ID, ActiveSeconds: 30,
				StartedAt: time.Date(2026, 2, 3, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 2, 3, 0, 0, 30, 0, time.UTC)},
			{ID: "stats-hidden", ReaderID: reader.ID, SeriesID: hidden.ID, ChapterID: hiddenChapters[0].ID, ActiveSeconds: 1000,
				StartedAt: now, UpdatedAt: now},
		} {
			if _, err := database.NewInsert().Model(session).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}

		viewer := &access.Principal{Kind: access.KindUser, ReaderID: reader.ID, Perms: map[string]bool{},
			Scope: access.Scope{RootFolders: []int64{visibleRoot.ID}}}
		stats, err := (&Service{DB: database}).Stats(access.With(ctx, viewer), reader.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stats.TotalActiveSeconds != 210 || stats.CompletedChapters != 5 {
			t.Fatalf("totals %+v", stats)
		}
		if stats.ActiveMonth == nil || stats.ActiveMonth.Month != "2026-01" || stats.ActiveMonth.ActiveSeconds != 120 {
			t.Fatalf("active month %+v", stats.ActiveMonth)
		}
		if stats.TopSeries == nil || stats.TopSeries.SeriesID != russian.ID || stats.TopSeries.CompletedChapters != 3 || stats.TopSeries.ActiveSeconds != 30 {
			t.Fatalf("top series %+v", stats.TopSeries)
		}
		if len(stats.Languages) != 2 || stats.Languages[0].Language != "en" || stats.Languages[0].ActiveSeconds != 180 ||
			stats.Languages[1].Language != "ru" || stats.Languages[1].CompletedChapters != 3 {
			t.Fatalf("languages %+v", stats.Languages)
		}
		if len(stats.Genres) != 2 || stats.Genres[0].Genre != "Sports" || stats.Genres[0].CompletedChapters != 3 ||
			stats.Genres[1].Genre != "Fantasy" || stats.Genres[1].ActiveSeconds != 180 {
			t.Fatalf("genres %+v", stats.Genres)
		}
	})
}
