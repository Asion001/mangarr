package reading

import (
	"context"
	"sort"
	"time"
)

// NextUp is the next chapter to read in a series.
type NextUp struct {
	Series SeriesInfo
	Book   BookInfo
}

// OnDeck lists, for each series the reader has started and not finished,
// the first unread chapter after the last one read, most recently read
// series first.
func (s *Service) OnDeck(ctx context.Context, readerID int64) ([]NextUp, error) {
	series, err := s.AllSeries(ctx, readerID, 0)
	if err != nil {
		return nil, err
	}
	started := map[int64]SeriesInfo{}
	for _, si := range series {
		if (si.Read > 0 || si.InProgress > 0) && si.Read < si.Books {
			started[si.Series.ID] = si
		}
	}
	if len(started) == 0 {
		return []NextUp{}, nil
	}
	books, err := s.Books(ctx, readerID, 0)
	if err != nil {
		return nil, err
	}
	bySeries := map[int64][]BookInfo{}
	for _, b := range books {
		if _, ok := started[b.Chapter.SeriesID]; ok {
			bySeries[b.Chapter.SeriesID] = append(bySeries[b.Chapter.SeriesID], b)
		}
	}
	out := []NextUp{}
	for sid, list := range bySeries { // list is ordered by number
		last := -1
		for i, b := range list {
			if b.State != nil && b.State.Completed {
				last = i
			}
		}
		for i := last + 1; i < len(list); i++ {
			if list[i].State == nil || !list[i].State.Completed {
				out = append(out, NextUp{Series: started[sid], Book: list[i]})
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return lastRead(out[i].Series).After(lastRead(out[j].Series))
	})
	return oncePerTitle(out, bySeries), nil
}

// oncePerTitle keeps one entry per title: of a title's language editions
// (they share their progress) the one the reader last read in, else the
// first in order.
func oncePerTitle(list []NextUp, books map[int64][]BookInfo) []NextUp {
	title := func(n NextUp) int64 {
		if n.Series.Series.WorkID > 0 {
			return n.Series.Series.WorkID
		}
		return -n.Series.Series.ID
	}
	// the edition whose chapter the title's latest progress was reported on
	seriesOf := map[int64]int64{}
	for sid, bs := range books {
		for _, b := range bs {
			seriesOf[b.Chapter.ID] = sid
		}
	}
	lastIn := map[int64]int64{}
	latest := map[int64]time.Time{}
	for _, n := range list {
		for _, b := range books[n.Series.Series.ID] {
			if b.State == nil {
				continue
			}
			at := b.State.SyncedAt
			if sid, ok := seriesOf[b.State.SourceChapterID]; ok && (lastIn[title(n)] == 0 || at.After(latest[title(n)])) {
				lastIn[title(n)], latest[title(n)] = sid, at
			}
		}
	}
	out := make([]NextUp, 0, len(list))
	at := map[int64]int{}
	for _, n := range list {
		i, seen := at[title(n)]
		switch {
		case !seen:
			at[title(n)] = len(out)
			out = append(out, n)
		case lastIn[title(n)] == n.Series.Series.ID:
			out[i] = n
		}
	}
	return out
}

func lastRead(si SeriesInfo) time.Time {
	if si.LastRead == nil {
		return time.Time{}
	}
	return *si.LastRead
}
