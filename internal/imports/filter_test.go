package imports

import (
	"slices"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/model"
)

func TestEntryFilterMatchAndSort(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	later := at.Add(time.Hour)
	list := []model.ImportEntry{
		{ID: 1, Position: 0, Title: "beta", Data: backupimport.BackupManga{Categories: []string{"Reading"}, ReadUpTo: &backupimport.ReadUpTo{Chapter: 12}}},
		{ID: 2, Position: 1, Title: "Alpha", Data: backupimport.BackupManga{Categories: []string{"Planned"}}, Source: &model.ImportSource{SourceName: "Senkuro"}},
		{ID: 3, Position: 2, Title: "gamma", Data: backupimport.BackupManga{Categories: []string{"Reading", "Favorites"},
			Chapters: []backupimport.BackupChapter{{Number: 1, Read: true, ReadAt: &at}, {Number: 2, Read: true, ReadAt: &later}, {Number: 3}}}},
	}
	ids := func(f EntryFilter) []int64 {
		var kept []model.ImportEntry
		for i := range list {
			if f.match(&list[i]) {
				kept = append(kept, list[i])
			}
		}
		f.sort(kept)
		var out []int64
		for _, e := range kept {
			out = append(out, e.ID)
		}
		return out
	}
	cases := []struct {
		name string
		f    EntryFilter
		want []int64
	}{
		{"list", EntryFilter{List: "Reading"}, []int64{1, 3}},
		{"source", EntryFilter{Source: "Senkuro"}, []int64{2}},
		{"no source", EntryFilter{Source: NoSource}, []int64{1, 3}},
		{"started", EntryFilter{Progress: "started"}, []int64{1, 3}},
		{"not started", EntryFilter{Progress: "none"}, []int64{2}},
		{"title", EntryFilter{Sort: "title"}, []int64{2, 1, 3}},
		{"most read", EntryFilter{Sort: "read"}, []int64{1, 3, 2}},
		{"recent", EntryFilter{Sort: "recent"}, []int64{3, 1, 2}},
		{"chapters", EntryFilter{Sort: "chapters"}, []int64{3, 1, 2}},
	}
	for _, c := range cases {
		if got := ids(c.f); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}
