package cleanup

import (
	"strconv"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/model"
)

var now = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)

func chapters(n int) []ChapterData {
	out := make([]ChapterData, n)
	for i := range out {
		num := float64(i + 1)
		out[i] = ChapterData{Chapter: model.Chapter{ID: int64(i + 1), NumberSort: num, NumberKey: strconv.Itoa(i + 1)},
			File: model.ChapterFile{ID: int64(100 + i), Size: 1000}}
	}
	return out
}

func read(states map[int64]map[int64]model.ChapterReadState, reader int64, chapter int64, completed bool, daysAgo int) {
	if states[chapter] == nil {
		states[chapter] = map[int64]model.ChapterReadState{}
	}
	at := now.Add(-time.Duration(daysAgo) * 24 * time.Hour)
	page := 0
	if !completed {
		page = 3
	}
	states[chapter][reader] = model.ChapterReadState{ReaderID: reader, ChapterID: chapter, Completed: completed, Page: page, ReadAt: &at, SyncedAt: at}
}

func baseRules() Rules {
	return Rules{Enabled: true, Statuses: []string{"ongoing"}, RequiredReaders: []int64{1, 2}, IgnoreNotStarted: true, KeepLastRead: 1, GraceDays: 7}
}

func ids(c []Candidate) []int64 {
	var out []int64
	for _, x := range c {
		out = append(out, x.ChapterID)
	}
	return out
}

func TestEvaluate(t *testing.T) {
	sd := SeriesData{Series: model.Series{ID: 1, Status: "ongoing"}, Chapters: chapters(5), States: map[int64]map[int64]model.ChapterReadState{}}
	// reader 1 read 1-4 long ago, reader 2 read 1-3 long ago and 4 yesterday
	for ch := int64(1); ch <= 4; ch++ {
		read(sd.States, 1, ch, true, 30)
	}
	for ch := int64(1); ch <= 3; ch++ {
		read(sd.States, 2, ch, true, 20)
	}
	read(sd.States, 2, 4, true, 1)

	cands, reason := Evaluate(baseRules(), sd, now)
	// completed by both: 1-4; keep last 1 (ch4); ch4 anyway in grace. → 1,2,3
	if reason != "" || len(cands) != 3 || cands[0].ChapterID != 1 || cands[2].ChapterID != 3 {
		t.Fatalf("got %v (%s)", ids(cands), reason)
	}

	// keep last 3 → only chapter 1
	r := baseRules()
	r.KeepLastRead = 3
	cands, _ = Evaluate(r, sd, now)
	if len(cands) != 1 || cands[0].ChapterID != 1 {
		t.Fatalf("keepLastRead: %v", ids(cands))
	}

	// grace 25 days: only chapters whose last required read is older (1-3 read by reader 2 20 days ago) → none
	r = baseRules()
	r.GraceDays = 25
	if cands, reason = Evaluate(r, sd, now); len(cands) != 0 || reason == "" {
		t.Fatalf("grace: %v %s", ids(cands), reason)
	}
}

func TestReadersNotStarted(t *testing.T) {
	sd := SeriesData{Series: model.Series{ID: 1, Status: "ongoing"}, Chapters: chapters(3), States: map[int64]map[int64]model.ChapterReadState{}}
	read(sd.States, 1, 1, true, 30)
	read(sd.States, 1, 2, true, 30)
	// reader 2 never opened the series
	cands, _ := Evaluate(baseRules(), sd, now)
	if len(cands) != 1 || cands[0].ChapterID != 1 {
		t.Fatalf("ignore not started: %v", ids(cands))
	}
	r := baseRules()
	r.IgnoreNotStarted = false
	if cands, reason := Evaluate(r, sd, now); len(cands) != 0 || reason == "" {
		t.Fatalf("reader 2 must block when not ignored: %v", ids(cands))
	}
	// reader 2 started (in progress) → blocks
	read(sd.States, 2, 1, false, 2)
	if cands, _ := Evaluate(baseRules(), sd, now); len(cands) != 0 {
		t.Fatalf("started reader must block: %v", ids(cands))
	}
}

func TestScope(t *testing.T) {
	sd := SeriesData{Series: model.Series{ID: 1, Status: "completed"}, Chapters: chapters(2), States: map[int64]map[int64]model.ChapterReadState{}}
	if _, reason := Evaluate(baseRules(), sd, now); reason == "" {
		t.Fatal("completed series out of scope by default")
	}
	r := baseRules()
	r.Enabled = false
	if _, reason := Evaluate(r, sd, now); reason != "cleanup disabled" {
		t.Fatal(reason)
	}
}

func TestRequiredReaders(t *testing.T) {
	readers := []model.Reader{{ID: 1}, {ID: 2, CountForCleanup: true}, {ID: 3}}
	if got := requiredReaders(readers, nil); len(got) != 1 || got[0] != 2 {
		t.Fatalf("counting readers: %v", got)
	}
	if got := requiredReaders(readers, []int64{3, 9}); len(got) != 1 || got[0] != 3 {
		t.Fatalf("chosen readers, counting or not: %v", got)
	}
	readers[1].CountForCleanup = false
	if got := requiredReaders(readers, nil); len(got) != 3 {
		t.Fatalf("nobody counting means everyone: %v", got)
	}
}

func TestLimitToGoals(t *testing.T) {
	at := func(days int) time.Time { return now.Add(-time.Duration(days) * 24 * time.Hour) }
	cands := []Candidate{
		{SeriesID: 1, ChapterID: 1, Size: 400, LastReadAt: at(5)},
		{SeriesID: 1, ChapterID: 2, Size: 400, LastReadAt: at(50)},
		{SeriesID: 2, ChapterID: 3, Size: 400, LastReadAt: at(20)},
		{SeriesID: 3, ChapterID: 4, Size: 400, LastReadAt: at(1)}, // another drive, no limit there
	}
	driveOf := map[int64]string{1: "a", 2: "a"}
	kept, held, heldSize := limitToGoals(cands, driveOf, map[string]*spaceGoal{"a": {need: 500}})
	// read longest ago first: ch 2 (400) then ch 3 (800 >= 500); ch 1 held
	if len(kept) != 3 || kept[0].ChapterID != 2 || kept[1].ChapterID != 3 || kept[2].ChapterID != 4 || held != 1 || heldSize != 400 {
		t.Fatalf("kept %v, held %d (%d)", ids(kept), held, heldSize)
	}
}
