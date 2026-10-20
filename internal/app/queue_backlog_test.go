package app_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/fakesource"
)

// A processing job queued behind more downloads than the dispatcher looks
// at in one pass still runs while those downloads wait.
func TestDispatcherReachesProcessingBehindLongDownloadQueue(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			name := "backlog-dispatch-" + dialect
			sc := fakesource.NewScenario(name)
			sc.Sources = []source.SourceInfo{{ID: "tin", Name: "Tin Source", Lang: "en"}}
			var chapters []fakesource.Chapter
			for i := 1; i <= 120; i++ {
				chapters = append(chapters, fakesource.Chapter{URL: fmt.Sprintf("/c%d", i), Name: "Chapter", Number: float64(i), Uploaded: time.Now()})
			}
			sc.AddManga(&fakesource.Manga{SourceID: "tin", URL: "/tin", Title: "Tin Rivers", Status: source.StatusOngoing, Chapters: chapters})
			e := newTestApp(t, dsn)
			// downloads wait out quiet hours all day; processing doesn't
			if err := e.App.Settings.Set(e.Ctx, settings.KeySchedule, settings.Schedule{Windows: []settings.ScheduleWindow{
				{Start: "00:00", End: "12:00", PauseDownloads: true},
				{Start: "12:00", End: "00:00", PauseDownloads: true},
			}}); err != nil {
				t.Fatal(err)
			}
			mod := e.addFakeModule(t, name)
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Tin Rivers", RootFolderID: e.RFID, Monitor: model.MonitorNone,
				Sources: []series.SourceLink{{ModuleID: mod, SourceID: "tin", URL: "/tin", SourceName: "Tin Source", Lang: "en"}}})
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "RefreshSeries", map[string]any{"seriesId": ser.ID})
			var chs []model.Chapter
			if err := e.App.DB.NewSelect().Model(&chs).Where("series_id = ?", ser.ID).Order("number_sort").Scan(e.Ctx); err != nil {
				t.Fatal(err)
			}
			if len(chs) != 120 {
				t.Fatalf("chapters: %d", len(chs))
			}
			for _, ch := range chs[:119] {
				if _, created, err := e.App.DLQueue.Enqueue(e.Ctx, ser.ID, ch.ID, nil, model.JobKindDownload, false); err != nil || !created {
					t.Fatalf("enqueue download: %v %v", created, err)
				}
			}
			job, created, err := e.App.DLQueue.EnqueuePriority(e.Ctx, ser.ID, chs[119].ID, nil, model.JobKindReprocess, true, -100)
			if err != nil || !created {
				t.Fatalf("enqueue reprocess: %v %v", created, err)
			}
			e.App.DLQueue.Wake()
			waitFor(t, 20*time.Second, "the processing job to start", func() bool {
				return e.jobs(t)[job.ID].Status != model.JobQueued
			})
			for id, j := range e.jobs(t) {
				if j.Kind == model.JobKindDownload && j.Status != model.JobQueued {
					t.Fatalf("download %d ran during quiet hours: %s", id, j.Status)
				}
			}
		})
	}
}
