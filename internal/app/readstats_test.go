package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
)

// TestSeriesReadStats checks the series list's read counts and the series
// detail's next unread chapter and per-reader progress.
func TestSeriesReadStats(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			e := newTestApp(t, dsn)
			now := time.Now().UTC()
			ser := &model.Series{Title: "Stats", SortTitle: "stats", Status: model.StatusOngoing, Monitored: true, MonitorNew: model.MonitorAll,
				RootFolderID: e.RFID, Path: "Stats", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
			if _, err := e.App.DB.NewInsert().Model(ser).Exec(e.Ctx); err != nil {
				t.Fatal(err)
			}
			var chs []*model.Chapter
			for i := 1; i <= 4; i++ {
				c := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(i), NumberSort: float64(i), Title: "Ch " + strconv.Itoa(i), Monitored: true,
					State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
				if _, err := e.App.DB.NewInsert().Model(c).Exec(e.Ctx); err != nil {
					t.Fatal(err)
				}
				chs = append(chs, c)
			}
			ann := &model.Reader{Name: "ann", CountForCleanup: true, CreatedAt: now}
			bob := &model.Reader{Name: "bob", CreatedAt: now}
			_, _ = e.App.DB.NewInsert().Model(ann).Exec(e.Ctx)
			_, _ = e.App.DB.NewInsert().Model(bob).Exec(e.Ctx)
			read := now.Add(-time.Hour)
			for _, st := range []*model.ChapterReadState{
				{ReaderID: ann.ID, ChapterID: chs[0].ID, SeriesID: ser.ID, Completed: true, ReadAt: &read, SyncedAt: now},
				{ReaderID: ann.ID, ChapterID: chs[1].ID, SeriesID: ser.ID, Completed: true, ReadAt: &now, SyncedAt: now},
				{ReaderID: ann.ID, ChapterID: chs[2].ID, SeriesID: ser.ID, Page: 7, SyncedAt: now},
				// bob doesn't count for cleanup, so he doesn't count here
				{ReaderID: bob.ID, ChapterID: chs[3].ID, SeriesID: ser.ID, Completed: true, ReadAt: &now, SyncedAt: now},
			} {
				if err := readstate.Save(e.Ctx, e.App.DB, st); err != nil {
					t.Fatal(err)
				}
			}
			srv := httptest.NewServer(api.New(e.App))
			defer srv.Close()
			g, _ := e.App.Settings.General(e.Ctx)
			get := func(path string, out any) {
				req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
				req.Header.Set("X-Api-Key", g.APIKey)
				resp, err := http.DefaultClient.Do(req)
				if err != nil || resp.StatusCode != 200 {
					t.Fatalf("%s: %v %v", path, err, resp.StatusCode)
				}
				defer resp.Body.Close()
				if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
					t.Fatal(err)
				}
			}
			var list []api.SeriesResource
			get("/api/v1/series", &list)
			if len(list) != 1 || list[0].Stats.ReadCount != 2 || list[0].Stats.InProgressCount != 1 || list[0].Stats.LastReadAt == nil ||
				list[0].Stats.LastReadAt.Sub(now).Abs() > time.Second {
				t.Fatalf("stats %+v", list[0].Stats)
			}
			var detail api.SeriesResource
			get("/api/v1/series/"+strconv.FormatInt(ser.ID, 10), &detail)
			rd := detail.Reading
			if rd == nil || rd.NextUnread == nil || rd.NextUnread.Number != "3" || rd.NextUnread.Available || len(rd.Readers) != 2 {
				t.Fatalf("reading %+v", rd)
			}
			if rd.Readers[0].Reader != "ann" || rd.Readers[0].Read != 2 || rd.Readers[0].InProgress != 1 || rd.Readers[1].Read != 1 {
				t.Fatalf("readers %+v", rd.Readers)
			}
		})
	}
}
