package reading

import (
	"context"
	"sort"
	"strings"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
)

// A reading app's device can have a language order. It then sees a title
// that has several language editions once: as the edition in its first
// language the title has, whose chapters come, chapter by chapter, from the
// first language in the order that has the chapter downloaded (then from
// any language that has it at all). Without an order every edition is
// listed on its own, as before.

type languagesKey struct{}

// WithLanguages sets the language order of the request's device.
func WithLanguages(ctx context.Context, order []string) context.Context {
	var clean []string
	for _, l := range order {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			clean = append(clean, l)
		}
	}
	if len(clean) == 0 {
		return ctx
	}
	return context.WithValue(ctx, languagesKey{}, clean)
}

// withoutLanguages lists editions on their own again.
func withoutLanguages(ctx context.Context) context.Context {
	return context.WithValue(ctx, languagesKey{}, []string(nil))
}

func languagesOf(ctx context.Context) []string {
	order, _ := ctx.Value(languagesKey{}).([]string)
	return order
}

// rank is an edition's place in the language order (lower is preferred;
// languages outside the order come last, oldest edition first).
func rank(order []string, ser *model.Series) int {
	lang := strings.ToLower(strings.TrimSpace(ser.Language))
	for i, l := range order {
		if l == lang || strings.HasPrefix(lang, l+"-") {
			return i
		}
	}
	return len(order)
}

// editions is how a device with a language order sees the library.
type editions struct {
	order []string
	// shown maps every visible edition to the one its title is shown as.
	shown map[int64]int64
	// of lists a shown edition's title's editions, preferred first.
	of map[int64][]model.Series
}

// editionsFor groups the visible editions in list by title. It is nil
// without a language order.
func editionsFor(ctx context.Context, list []model.Series) *editions {
	order := languagesOf(ctx)
	if len(order) == 0 {
		return nil
	}
	byTitle := map[int64][]model.Series{}
	for _, ser := range list {
		key := ser.WorkID
		if key == 0 || ser.Preview {
			key = -ser.ID
		}
		byTitle[key] = append(byTitle[key], ser)
	}
	e := &editions{order: order, shown: map[int64]int64{}, of: map[int64][]model.Series{}}
	for _, eds := range byTitle {
		sort.SliceStable(eds, func(i, j int) bool {
			if ri, rj := rank(order, &eds[i]), rank(order, &eds[j]); ri != rj {
				return ri < rj
			}
			return eds[i].ID < eds[j].ID
		})
		for _, ed := range eds {
			e.shown[ed.ID] = eds[0].ID
		}
		e.of[eds[0].ID] = eds
	}
	return e
}

// titleEditions loads the visible editions of seriesID's title for a device
// with a language order (nil without one, or when the title has one edition).
func (s *Service) titleEditions(ctx context.Context, seriesID int64) (*editions, error) {
	if len(languagesOf(ctx)) == 0 {
		return nil, nil
	}
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Column("id", "work_id", "preview").Where("id = ?", seriesID).Scan(ctx); err != nil || ser.WorkID == 0 || ser.Preview {
		return nil, nil // (a missing series is the caller's to report)
	}
	var list []model.Series
	if err := s.DB.NewSelect().Model(&list).Where("work_id = ? AND preview = ?", ser.WorkID, false).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	if sc := scopeOf(ctx); sc != nil {
		kept := list[:0]
		for i := range list {
			if sc.Allows(&list[i]) {
				kept = append(kept, list[i])
			}
		}
		list = kept
	}
	e := editionsFor(ctx, list)
	if e == nil || len(e.of[e.shown[seriesID]]) < 2 {
		return nil, nil
	}
	return e, nil
}

// merge turns the books of every edition into each title's one list: per
// chapter number the preferred edition's downloaded chapter, else the most
// preferred edition's. Books keep their own chapter ids (progress is the
// title's whichever is read) and get the shown edition as their series.
func (e *editions) merge(books []BookInfo) []BookInfo {
	type key struct {
		shown  int64
		number string
	}
	place := map[int64]int{} // edition → its place in its title's order
	for _, eds := range e.of {
		for i, ed := range eds {
			place[ed.ID] = i
		}
	}
	better := func(a, b BookInfo) bool { // a instead of b
		if (a.File != nil) != (b.File != nil) {
			return a.File != nil
		}
		return place[a.EditionID] < place[b.EditionID]
	}
	best := map[key]int{}
	out := make([]BookInfo, 0, len(books))
	for _, b := range books {
		shown, ok := e.shown[b.EditionID]
		if !ok {
			shown = b.EditionID
		}
		k := key{shown, b.Chapter.NumberKey}
		b.Chapter.SeriesID = shown
		if i, seen := best[k]; seen {
			if better(b, out[i]) {
				out[i] = b
			}
			continue
		}
		best[k] = len(out)
		out = append(out, b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Chapter, out[j].Chapter
		if a.SeriesID != b.SeriesID {
			return a.SeriesID < b.SeriesID
		}
		return a.NumberSort < b.NumberSort
	})
	index := map[int64]int{}
	for i := range out {
		index[out[i].Chapter.SeriesID]++
		out[i].Index = index[out[i].Chapter.SeriesID]
	}
	return out
}

// count sets a shown title's counts from its merged books.
func count(si *SeriesInfo, books []BookInfo) {
	si.Books, si.Read, si.InProgress = 0, 0, 0
	for _, b := range books {
		if b.Chapter.SeriesID != si.Series.ID {
			continue
		}
		si.Books++
		switch {
		case b.State == nil:
		case b.State.Completed:
			si.Read++
		case b.State.Page > 0:
			si.InProgress++
		}
	}
}

// chaptersOf loads the chapters of several series in reading order.
func (s *Service) chaptersOf(ctx context.Context, ids []int64) ([]model.Chapter, error) {
	var chapters []model.Chapter
	err := s.DB.NewSelect().Model(&chapters).Where("series_id IN (?)", bun.In(ids)).Order("series_id", "number_sort", "id").Scan(ctx)
	return chapters, err
}
