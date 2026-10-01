package downloads

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
)

func rankFixture(t *testing.T, d *db.DB, count int) (*Queue, []model.Chapter) {
	t.Helper()
	ctx := t.Context()
	now := time.Now().UTC()
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	profile := &model.Profile{Name: "Queue profile", CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{root, profile} {
		if _, err := d.NewInsert().Model(row).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ser := &model.Series{Title: "Copper Clouds", SortTitle: "copper clouds", RootFolderID: root.ID, ProfileID: profile.ID, Path: "copper-clouds", Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	chapters := make([]model.Chapter, count)
	for i := range chapters {
		chapters[i] = model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(i + 1), NumberSort: float64(i + 1), State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
	}
	if _, err := d.NewInsert().Model(&chapters).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return NewQueue(d, events.NewBus()), chapters
}

func enqueueRankJobs(t *testing.T, q *Queue, chapters []model.Chapter) []model.DownloadJob {
	t.Helper()
	jobs := make([]model.DownloadJob, len(chapters))
	for i, ch := range chapters {
		kind := model.JobKindDownload
		if i%2 == 1 {
			kind = model.JobKindReprocess
		}
		job, created, err := q.Enqueue(t.Context(), ch.SeriesID, ch.ID, nil, kind, false)
		if err != nil || !created {
			t.Fatalf("enqueue: %v %v", created, err)
		}
		jobs[i] = *job
	}
	return jobs
}

func rankOrder(t *testing.T, q *Queue, f ListFilter, size int) []int64 {
	t.Helper()
	var ids []int64
	var previous int64
	for page := 1; ; page++ {
		got, err := q.ListPage(t.Context(), f, page, size)
		if err != nil {
			t.Fatal(err)
		}
		for _, job := range got.Items {
			if len(ids) > 0 && job.Rank <= previous {
				t.Fatalf("ranks not strictly increasing: %d <= %d", job.Rank, previous)
			}
			previous = job.Rank
			ids = append(ids, job.ID)
		}
		if len(ids) >= got.Total {
			break
		}
		if len(got.Items) == 0 {
			t.Fatal("empty intermediate page")
		}
	}
	return ids
}

func TestQueueRankMovesAndPagination(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 12)
		jobs := enqueueRankJobs(t, q, chapters)
		ctx := t.Context()
		id := func(i int) int64 { return jobs[i].ID }
		// Paused jobs occupy the same durable pending order.
		if _, err := d.NewUpdate().Model((*model.DownloadJob)(nil)).Set("status = ?", model.JobPaused).Where("id = ?", id(3)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		moves := []struct {
			ids    []int64
			action string
			anchor int64
			want   []int64
		}{
			{[]int64{id(7), id(3), id(7)}, "top", 0, []int64{id(3), id(7), id(0), id(1), id(2), id(4), id(5), id(6), id(8), id(9), id(10), id(11)}},
			{[]int64{id(3), id(7)}, "bottom", 0, []int64{id(0), id(1), id(2), id(4), id(5), id(6), id(8), id(9), id(10), id(11), id(3), id(7)}},
			{[]int64{id(7), id(3)}, "before", id(1), []int64{id(0), id(3), id(7), id(1), id(2), id(4), id(5), id(6), id(8), id(9), id(10), id(11)}},
			{[]int64{id(7), id(3)}, "after", id(10), []int64{id(0), id(1), id(2), id(4), id(5), id(6), id(8), id(9), id(10), id(3), id(7), id(11)}},
		}
		for _, move := range moves {
			if n, err := q.Move(ctx, move.ids, move.action, move.anchor); err != nil || n != 2 {
				t.Fatalf("%s: %d %v", move.action, n, err)
			}
			for _, size := range []int{1, 3, 5, 50} {
				if got := rankOrder(t, q, ListFilter{}, size); !slices.Equal(got, move.want) {
					t.Fatalf("%s page size %d: %v want %v", move.action, size, got, move.want)
				}
			}
			for _, kind := range []string{model.JobKindDownload, model.JobKindReprocess} {
				var want []int64
				for _, id := range move.want {
					for _, job := range jobs {
						if id == job.ID && job.Kind == kind {
							want = append(want, id)
						}
					}
				}
				if got := rankOrder(t, q, ListFilter{Kind: kind}, 2); !slices.Equal(got, want) {
					t.Fatalf("kind %s: %v want %v", kind, got, want)
				}
			}
		}
		before := rankOrder(t, q, ListFilter{}, 20)
		for _, move := range []struct {
			ids    []int64
			action string
			anchor int64
		}{
			{[]int64{id(0)}, "before", id(0)}, {[]int64{id(0)}, "after", 999999}, {[]int64{id(0)}, "before", 0}, {[]int64{id(0)}, "top", id(1)},
		} {
			if _, err := q.Move(ctx, move.ids, move.action, move.anchor); err == nil {
				t.Fatal("invalid move accepted")
			}
			if got := rankOrder(t, q, ListFilter{}, 20); !slices.Equal(got, before) {
				t.Fatal("invalid move changed order")
			}
		}
	})
}

func TestQueueRankConcurrentMovesAndEnqueues(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 48)
		jobs := enqueueRankJobs(t, q, chapters[:24])
		// A second handle exercises the database lock, not a process-local mutex.
		other := NewQueue(d, events.NewBus())
		start := make(chan struct{})
		errs := make(chan error, 36)
		var wg sync.WaitGroup
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				action := "top"
				if i%2 == 1 {
					action = "bottom"
				}
				_, err := other.Move(t.Context(), []int64{jobs[2*i+1].ID, jobs[2*i].ID}, action, 0)
				errs <- err
			}(i)
		}
		for _, ch := range chapters[24:] {
			wg.Add(1)
			go func(ch model.Chapter) {
				defer wg.Done()
				<-start
				_, _, err := q.Enqueue(t.Context(), ch.SeriesID, ch.ID, nil, model.JobKindDownload, false)
				errs <- err
			}(ch)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		got := rankOrder(t, q, ListFilter{}, 7)
		if len(got) != 48 {
			t.Fatalf("lost jobs: %d", len(got))
		}
		for i := 0; i < 12; i++ {
			a, b := slices.Index(got, jobs[2*i].ID), slices.Index(got, jobs[2*i+1].ID)
			if b != a+1 {
				t.Fatalf("bulk move interleaved/reversed: %v", got)
			}
		}
		// Overlapping moves must each observe and preserve the preceding order.
		errs = make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				action := "top"
				if i%2 == 1 {
					action = "bottom"
				}
				_, err := other.Move(t.Context(), []int64{jobs[1].ID, jobs[0].ID}, action, 0)
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		got = rankOrder(t, q, ListFilter{}, 5)
		if slices.Index(got, jobs[1].ID) != slices.Index(got, jobs[0].ID)+1 {
			t.Fatal("overlapping moves reversed order")
		}
	})
}

func TestQueueRankPinned(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 7)
		jobs := enqueueRankJobs(t, q, chapters)
		ctx := t.Context()
		m := &Manager{db: d, queue: q}
		// A claim only needs the job still queued: re-ranking elsewhere in the
		// queue (moves, enqueues during a big import) must not stall dispatch.
		if _, err := q.Move(ctx, []int64{jobs[5].ID}, "top", 0); err != nil {
			t.Fatal(err)
		}
		if !m.claim(ctx, &jobs[0]) {
			t.Fatal("dispatch failed after an unrelated move")
		}
		for i, status := range []string{model.JobProcessing, model.JobImporting, model.JobFailed, model.JobCompleted} {
			if _, err := d.NewUpdate().Model((*model.DownloadJob)(nil)).Set("status = ?", status).Where("id = ?", jobs[i+1].ID).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		ids := []int64{}
		for _, j := range jobs {
			ids = append(ids, j.ID)
		}
		if n, err := q.Move(ctx, ids, "bottom", 0); err != nil || n != 2 {
			t.Fatalf("move pinned: %d %v", n, err)
		}
		for _, j := range jobs[:5] {
			var rank int64
			if err := d.NewSelect().Table("download_jobs").Column("rank").Where("id = ?", j.ID).Scan(ctx, &rank); err != nil {
				t.Fatal(err)
			}
			if rank != j.Rank {
				t.Fatalf("nonpending job rank changed: %d", j.ID)
			}
		}
		for _, j := range jobs[:5] {
			if _, err := q.Move(ctx, []int64{jobs[5].ID}, "before", j.ID); err == nil {
				t.Fatal("nonpending anchor accepted")
			}
		}
		page, err := q.ListPage(ctx, ListFilter{}, 1, 20)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 5 || page.Items[0].ID != jobs[2].ID || page.Items[1].ID != jobs[1].ID || page.Items[2].ID != jobs[0].ID {
			t.Fatalf("running work not pinned: %+v", page.Items)
		}
	})
}

func TestQueueRankGapRecoveryAndPriority(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 260)
		jobs := enqueueRankJobs(t, q, chapters[:3])
		ctx := t.Context()
		// Repeated insertion in one gap must survive rank compaction.
		for i := 0; i < 50; i++ {
			if _, err := q.Move(ctx, []int64{jobs[2].ID}, "after", jobs[0].ID); err != nil {
				t.Fatal(err)
			}
		}
		if got := rankOrder(t, q, ListFilter{}, 1); !slices.Equal(got, []int64{jobs[0].ID, jobs[2].ID, jobs[1].ID}) {
			t.Fatal(got)
		}
		if err := q.Raise(ctx, jobs[1].ChapterID, model.PriorityReading); err != nil {
			t.Fatal(err)
		}
		if got := rankOrder(t, q, ListFilter{}, 10); got[0] != jobs[1].ID {
			t.Fatal("reader priority did not raise rank")
		}
		if err := q.Raise(ctx, jobs[1].ChapterID, model.PriorityReading); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Move(ctx, []int64{jobs[1].ID}, "bottom", 0); err != nil {
			t.Fatal(err)
		}
		if got := rankOrder(t, q, ListFilter{}, 10); got[2] != jobs[1].ID {
			t.Fatal("legacy priority overrode manual move")
		}
		files := make([]model.ChapterFile, 0, 257)
		for _, ch := range chapters[3:] {
			files = append(files, model.ChapterFile{SeriesID: ch.SeriesID, ChapterID: ch.ID})
		}
		if n, err := q.EnqueueReprocessFiles(ctx, files, -100); err != nil || n != 257 {
			t.Fatalf("batch enqueue: %d %v", n, err)
		}
		if n, err := q.EnqueueReprocessFiles(ctx, files, -100); err != nil || n != 0 {
			t.Fatalf("duplicate batch: %d %v", n, err)
		}
		if got := rankOrder(t, q, ListFilter{}, 37); len(got) != 260 {
			t.Fatal(len(got))
		}
		// Rank is persisted, independent of Queue instances.
		if got := rankOrder(t, NewQueue(d, events.NewBus()), ListFilter{}, 37); len(got) != 260 {
			t.Fatal(len(got))
		}
	})
}

func TestQueueRankMoveAgainstClaim(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 24)
		jobs := enqueueRankJobs(t, q, chapters)
		m := &Manager{db: d, queue: NewQueue(d, events.NewBus())}
		for _, job := range jobs {
			start := make(chan struct{})
			claimed := make(chan bool, 1)
			go func() {
				<-start
				claimed <- m.claim(t.Context(), &job)
			}()
			close(start)
			n, err := q.Move(t.Context(), []int64{job.ID}, "top", 0)
			if err != nil {
				t.Fatal(err)
			}
			var current model.DownloadJob
			won := <-claimed
			if err := d.NewSelect().Model(&current).Where("id = ?", job.ID).Scan(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Either order is fine, but a move never touches a job once it runs,
			// and a move doesn't stop a queued job from being claimed.
			if !won || current.Status != model.JobDownloading {
				t.Fatalf("claim lost to a move: affected=%d job=%+v", n, current)
			}
			if n == 0 && current.Rank != job.Rank {
				t.Fatalf("move changed a running job: %+v", current)
			}
		}
	})
}

func TestQueueRankPaginationRevision(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 5)
		jobs := enqueueRankJobs(t, q, chapters[:4])
		page, err := q.ListPage(t.Context(), ListFilter{}, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := q.Move(t.Context(), []int64{jobs[3].ID}, "before", jobs[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := q.ListPageAt(t.Context(), ListFilter{}, 2, 2, &page.Revision); !errors.Is(err, ErrQueueOrderChanged) {
			t.Fatalf("stale page after move: %v", err)
		}
		page, err = q.ListPage(t.Context(), ListFilter{}, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := q.EnqueuePriority(t.Context(), chapters[4].SeriesID, chapters[4].ID, nil, model.JobKindReprocess, false, model.PriorityReading); err != nil {
			t.Fatal(err)
		}
		if _, err := q.ListPageAt(t.Context(), ListFilter{}, 2, 2, &page.Revision); !errors.Is(err, ErrQueueOrderChanged) {
			t.Fatalf("stale page after enqueue: %v", err)
		}
	})
}

func TestQueueSortByChapterKeepsTheSelectionsPlaces(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 6)
		jobs := enqueueRankJobs(t, q, chapters)
		ctx := t.Context()
		// Scramble the queue into chapters 1 5 2 4 3 6.
		if _, err := q.Move(ctx, []int64{jobs[4].ID}, "before", jobs[1].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := q.Move(ctx, []int64{jobs[3].ID}, "after", jobs[1].ID); err != nil {
			t.Fatal(err)
		}
		want := []int64{jobs[0].ID, jobs[4].ID, jobs[1].ID, jobs[3].ID, jobs[2].ID, jobs[5].ID}
		if got := rankOrder(t, q, ListFilter{}, 10); !slices.Equal(got, want) {
			t.Fatalf("setup order %v, want %v", got, want)
		}
		n, err := q.SortByChapter(ctx, []int64{jobs[4].ID, jobs[1].ID, jobs[3].ID})
		if err != nil || n != 3 {
			t.Fatalf("sort: %d %v", n, err)
		}
		// 5 2 4 become 2 4 5 in the same places; 1, 3 and 6 don't move.
		want = []int64{jobs[0].ID, jobs[1].ID, jobs[3].ID, jobs[4].ID, jobs[2].ID, jobs[5].ID}
		if got := rankOrder(t, q, ListFilter{}, 10); !slices.Equal(got, want) {
			t.Fatalf("sorted order %v, want %v", got, want)
		}
		if n, err := q.SortByChapter(ctx, []int64{jobs[1].ID, jobs[3].ID, jobs[4].ID}); err != nil || n != 0 {
			t.Fatalf("already sorted: %d %v", n, err)
		}
	})
}

// TestQueuePageSeriesTotals: a page cut short still reports the series'
// whole count, so the group header can say "2 of 5 shown".
func TestQueuePageSeriesTotals(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, chapters := rankFixture(t, d, 5)
		enqueueRankJobs(t, q, chapters)
		page, err := q.ListPage(t.Context(), ListFilter{}, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		key := fmt.Sprint(chapters[0].SeriesID)
		if len(page.Items) != 2 || page.SeriesTotals[key] != 5 {
			t.Fatalf("items=%d totals=%v", len(page.Items), page.SeriesTotals)
		}
		page, err = q.ListPage(t.Context(), ListFilter{Kind: model.JobKindDownload}, 1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if page.SeriesTotals[key] != 3 {
			t.Fatalf("filtered totals=%v", page.SeriesTotals)
		}
	})
}
