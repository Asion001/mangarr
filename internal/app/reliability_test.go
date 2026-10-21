package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/sourcegov"
)

// TestSourceReliability: request counts survive being written to the
// database twice in the same hour (they add up), and the reliability view
// reports rates, response times, fallbacks and recent failures per catalog.
func TestSourceReliability(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			mod := e.addFakeModule(t, "reliability")
			gov, ctx := e.App.Catalogs.Gov, context.Background()
			key := sourcegov.Key{ModuleID: mod, SourceID: "A"}
			ok := func(context.Context) error { return nil }
			for range 4 {
				_ = gov.DoOp(ctx, key, "Search “a”", ok)
			}
			if err := e.App.Catalogs.FlushStats(ctx); err != nil {
				t.Fatal(err)
			}
			_ = gov.DoOp(ctx, key, "Chapter list of Berserk", func(context.Context) error { return errors.New("http error 500") })
			_ = gov.DoOp(ctx, key, "Search “b”", ok)
			gov.Fallback(key)
			list, err := e.App.Catalogs.Reliability(ctx, time.Now().Add(-24*time.Hour), time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, c := range list {
				if c.ModuleID != mod || c.SourceID != "A" {
					continue
				}
				found = true
				if c.Requests != 6 || c.Failures != 1 || c.Fallbacks != 1 || c.Failing() {
					t.Fatalf("totals %+v", c)
				}
				if len(c.Points) < 24 || c.Points[len(c.Points)-1].Requests != 6 {
					t.Fatalf("points %+v", c.Points)
				}
				if len(c.RecentFailures) != 1 || c.RecentFailures[0].What != "Chapter list of Berserk" {
					t.Fatalf("failures %+v", c.RecentFailures)
				}
			}
			if !found {
				t.Fatalf("catalog missing from %+v", list)
			}
			var stored []model.SourceStat
			if err := e.App.DB.NewSelect().Model(&stored).Scan(ctx); err != nil || len(stored) != 1 || stored[0].Requests != 6 {
				t.Fatalf("stored %+v, %v", stored, err)
			}
		})
	}
}
