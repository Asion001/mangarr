package readinglists_test

import (
	"context"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readinglists"
)

// TestRetitleAndPurge: an edition joining another title brings its
// statuses and lists without overwriting the title's, and a title with no
// series left loses them.
func TestRetitleAndPurge(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		now := time.Now().UTC()
		u := &model.User{Username: "ann", CreatedAt: now}
		if _, err := d.NewInsert().Model(u).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		const from, to, other = -1, 7, 8
		fav, err := readinglists.Favorites(ctx, d, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := readinglists.Set(ctx, d, u.ID, from, readinglists.TitleLists{Status: model.ReadReading, Lists: []int64{fav.ID}}); err != nil {
			t.Fatal(err)
		}
		if err := readinglists.Set(ctx, d, u.ID, other, readinglists.TitleLists{Status: model.ReadPlanned}); err != nil {
			t.Fatal(err)
		}
		if err := readinglists.Set(ctx, d, u.ID, to, readinglists.TitleLists{Status: model.ReadDropped}); err != nil {
			t.Fatal(err)
		}
		if err := readinglists.Retitle(ctx, d, from, to); err != nil {
			t.Fatal(err)
		}
		got, err := readinglists.Get(ctx, d, u.ID, to)
		if err != nil || got.Status != model.ReadDropped || len(got.Lists) != 1 {
			t.Fatalf("joined title = %+v %v", got, err)
		}
		// no series at all: everything goes
		if err := readinglists.Purge(ctx, d); err != nil {
			t.Fatal(err)
		}
		if got, _ := readinglists.Get(ctx, d, u.ID, other); got.Status != "" || len(got.Lists) != 0 {
			t.Fatalf("after purge = %+v", got)
		}
	})
}
