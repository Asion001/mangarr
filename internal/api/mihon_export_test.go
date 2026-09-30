package api_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/settings"
)

func TestMihonBackupExportsVisibleLibraryProgressAndDevice(t *testing.T) {
	srv, app := newServer(t, true)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	readingSettings, err := app.Settings.Reading(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readingSettings.Enabled = true
	if err := app.Settings.Set(ctx, settings.KeyReading, readingSettings); err != nil {
		t.Fatal(err)
	}
	root := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: now}
	if _, err := app.DB.NewInsert().Model(root).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var profile model.Profile
	if err := app.DB.NewSelect().Model(&profile).Order("id").Limit(1).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	series := &model.Series{Title: "Exported", SortTitle: "exported", Status: model.StatusOngoing, Monitored: true,
		MonitorNew: model.MonitorAll, RootFolderID: root.ID, Path: "Exported", ProfileID: profile.ID, Language: "en",
		SourcePriorityMode: "inherit", ReadingDirection: "rtl", Tags: []int64{}, Metadata: model.SeriesMetadata{
			Authors: []string{"Writer"}, Artists: []string{"Artist"}, Genres: []string{"Drama"}, ExternalIDs: map[string]string{"anilist": "1234"},
		}, AddedAt: now.Add(-24 * time.Hour), UpdatedAt: now}
	if _, err := app.DB.NewInsert().Model(series).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	chapters := []model.Chapter{
		{SeriesID: series.ID, NumberKey: "1", NumberSort: 1, Title: "Start", Monitored: true, State: model.ChapterImported, FirstSeenAt: now.Add(-2 * time.Hour), UpdatedAt: now},
		{SeriesID: series.ID, NumberKey: "2", NumberSort: 2, Title: "Next", Monitored: true, State: model.ChapterMissing, FirstSeenAt: now.Add(-time.Hour), UpdatedAt: now},
	}
	if _, err := app.DB.NewInsert().Model(&chapters).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	readerID, err := app.Reading.ReaderID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	readAt := now.Add(-30 * time.Minute)
	states := []model.ChapterReadState{
		{ReaderID: readerID, SeriesID: series.ID, ChapterID: chapters[0].ID, Completed: true, ReadAt: &readAt, SyncedAt: readAt},
		{ReaderID: readerID, SeriesID: series.ID, ChapterID: chapters[1].ID, Page: 5, SyncedAt: now},
	}
	for i := range states {
		if err := readstate.Save(ctx, app.DB, &states[i]); err != nil {
			t.Fatal(err)
		}
	}

	resp, err := http.Post(srv.URL+"/api/v1/reading/mihon-backup", "application/json", strings.NewReader(`{"address":"https://manga.example.test/base"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), ".tachibk") || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("response: %d %v %s", resp.StatusCode, resp.Header, data)
	}
	backup, err := backupimport.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if backup.Sources[reading.MihonKomgaSourceID] != "Komga" || len(backup.Entries) != 1 {
		t.Fatalf("backup: %+v", backup)
	}
	entry := backup.Entries[0]
	if entry.Title != series.Title || entry.URL != "https://manga.example.test/base/api/v1/series/"+strconv.FormatInt(series.ID, 10) ||
		entry.Trackers[backupimport.TrackerAniList] != "1234" || len(entry.Chapters) != 2 {
		t.Fatalf("entry: %+v", entry)
	}
	if !entry.Chapters[0].Read || entry.Chapters[0].ReadAt == nil || entry.Chapters[1].Read || entry.Chapters[1].LastPageRead != 4 {
		t.Fatalf("progress: %+v", entry.Chapters)
	}
	var keys []model.ReadingKey
	if err := app.DB.NewSelect().Model(&keys).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !strings.HasPrefix(keys[0].Comment, "Mihon backup ") {
		t.Fatalf("device keys: %+v", keys)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"source_" + reading.MihonKomgaSourceID, "Address", "https://manga.example.test/base", "API key", "StringPreferenceValue"} {
		if !bytes.Contains(raw, []byte(value)) {
			t.Errorf("backup is missing %q", value)
		}
	}
}
