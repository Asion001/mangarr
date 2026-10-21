package api

import (
	"fmt"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
)

func TestUpdatesSQLCursorAndVisibility(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, database *db.DB) {
		ctx := t.Context()
		for _, name := range []string{"chapters_first_seen", "series_added", "works_created"} {
			var count int
			query := "SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?"
			if database.Kind == db.Postgres {
				query = "SELECT COUNT(*) FROM pg_indexes WHERE schemaname = current_schema() AND indexname = ?"
			}
			if err := database.QueryRowContext(ctx, query, name).Scan(&count); err != nil || count != 1 {
				t.Fatalf("index %s: count=%d err=%v", name, count, err)
			}
		}
		now := time.Now().UTC().Truncate(time.Second)
		profile := &model.Profile{Name: "Updates SQL", CreatedAt: now, UpdatedAt: now}
		if _, err := database.NewInsert().Model(profile).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		visibleRoot := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
		hiddenRoot := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
		if _, err := database.NewInsert().Model(visibleRoot).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := database.NewInsert().Model(hiddenRoot).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		series := []*model.Series{
			{Title: "Visible", SortTitle: "visible", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll, RootFolderID: visibleRoot.ID, Path: "visible", ProfileID: profile.ID, Tags: []int64{7}, AddedAt: now.Add(-2 * time.Hour), UpdatedAt: now},
			{Title: "Excluded tag", SortTitle: "excluded", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll, RootFolderID: visibleRoot.ID, Path: "excluded", ProfileID: profile.ID, Tags: []int64{7, 8}, AddedAt: now.Add(-2 * time.Hour), UpdatedAt: now},
			{Title: "Wrong root", SortTitle: "wrong root", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll, RootFolderID: hiddenRoot.ID, Path: "wrong-root", ProfileID: profile.ID, Tags: []int64{7}, AddedAt: now.Add(-2 * time.Hour), UpdatedAt: now},
		}
		for _, item := range series {
			if _, err := database.NewInsert().Model(item).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		seenAt := now.Add(-time.Hour)
		chapters := make([]model.Chapter, 0, 700)
		for i := range 600 {
			chapters = append(chapters, model.Chapter{SeriesID: series[0].ID, NumberKey: fmt.Sprint(i + 1), NumberSort: float64(i + 1), State: model.ChapterMissing, FirstSeenAt: seenAt, UpdatedAt: now})
		}
		for i := range 50 {
			chapters = append(chapters, model.Chapter{SeriesID: series[1].ID, NumberKey: fmt.Sprint(i + 1), NumberSort: float64(i + 1), State: model.ChapterMissing, FirstSeenAt: seenAt, UpdatedAt: now})
			chapters = append(chapters, model.Chapter{SeriesID: series[2].ID, NumberKey: fmt.Sprint(i + 1), NumberSort: float64(i + 1), State: model.ChapterMissing, FirstSeenAt: seenAt, UpdatedAt: now})
		}
		if _, err := database.NewInsert().Model(&chapters).Exec(ctx); err != nil {
			t.Fatal(err)
		}

		server := &Server{app: &app.App{DB: database}}
		viewer := &access.Principal{Perms: map[string]bool{}, Scope: access.Scope{IncludeTags: []int64{7}, ExcludeTags: []int64{8}, RootFolders: []int64{visibleRoot.ID}}}
		seriesPage, err := server.listUpdates(ctx, viewer, updateFilter{Days: 7, Kind: "series"}, 1, 20, nil)
		if err != nil {
			t.Fatal(err)
		}
		if seriesPage.Total != 1 || len(seriesPage.Items) != 1 || seriesPage.Items[0].SeriesTitle != "Visible" {
			t.Fatalf("database visibility was not applied to series updates: %+v", seriesPage)
		}
		tieSeries := &model.Series{Title: "Tie title", SortTitle: "tie title", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
			RootFolderID: visibleRoot.ID, Path: "tie-title", ProfileID: profile.ID, Tags: []int64{7}, AddedAt: seenAt, UpdatedAt: now}
		if _, err := database.NewInsert().Model(tieSeries).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		mixedFirst, err := server.listUpdates(ctx, viewer, updateFilter{Days: 7, Kind: "all"}, 1, 600, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(mixedFirst.Items) != 600 || mixedFirst.Items[0].Kind != "chapter" || mixedFirst.NextCursor == "" {
			t.Fatalf("mixed first page did not keep chapter ties first: %+v", mixedFirst)
		}
		mixedCursor, err := decodeUpdateCursor(mixedFirst.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
		mixedSecond, err := server.listUpdates(ctx, viewer, updateFilter{Days: 7, Kind: "all"}, 1, 10, &mixedCursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(mixedSecond.Items) == 0 || mixedSecond.Items[0].Kind != "series" || mixedSecond.Items[0].SeriesTitle != tieSeries.Title {
			t.Fatalf("mixed cursor skipped the lower-ranked tied series event: %+v", mixedSecond)
		}

		var cursor *updateCursor
		seen := map[int64]bool{}
		pages := 0
		var previousID int64
		for {
			started := time.Now()
			page, err := server.listUpdates(ctx, viewer, updateFilter{Days: 7, Kind: "chapter"}, 1, 37, cursor)
			if err != nil {
				t.Fatal(err)
			}
			pages++
			if pages == 1 && page.Total != 600 {
				t.Fatalf("total=%d want 600", page.Total)
			}
			if pages == 1 {
				elapsed := time.Since(started)
				t.Logf("%s 600-row first page: %s", dbtest.Name(database), elapsed)
				if elapsed > 2*time.Second {
					t.Fatalf("first page took %s", elapsed)
				}
			}
			for _, item := range page.Items {
				if item.SeriesID != series[0].ID {
					t.Fatalf("hidden series leaked into feed: %+v", item)
				}
				if seen[item.ChapterID] {
					t.Fatalf("chapter %d appeared twice", item.ChapterID)
				}
				if previousID > 0 && item.ChapterID >= previousID {
					t.Fatalf("tie ordering is unstable: %d after %d", item.ChapterID, previousID)
				}
				seen[item.ChapterID] = true
				previousID = item.ChapterID
			}
			if pages == 1 {
				newChapter := &model.Chapter{SeriesID: series[0].ID, NumberKey: "newer", NumberSort: 9999, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
				if _, err := database.NewInsert().Model(newChapter).Exec(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if page.NextCursor == "" {
				break
			}
			next, err := decodeUpdateCursor(page.NextCursor)
			if err != nil {
				t.Fatal(err)
			}
			cursor = &next
			if pages > 20 {
				t.Fatal("cursor did not terminate")
			}
		}
		if len(seen) != 600 {
			t.Fatalf("cursor returned %d original rows, want 600", len(seen))
		}
	})
}
