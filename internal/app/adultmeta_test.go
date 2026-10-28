package app_test

import (
	"context"
	"testing"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/settings"
)

// adultMeta answers every search with one title, adult or not, and remembers
// whether the search asked it to leave adult titles out.
type adultMeta struct {
	provider string
	adult    bool
	asked    *bool
}

func (adultMeta) Test(context.Context) error { return nil }
func (a adultMeta) Search(ctx context.Context, q string, _ int) ([]metadata.SeriesMetadata, error) {
	*a.asked = metadata.AdultHidden(ctx)
	return []metadata.SeriesMetadata{{Provider: a.provider, ID: a.provider + ":1", Title: q, Adult: a.adult, Format: "manga"}}, nil
}
func (a adultMeta) Get(context.Context, string) (*metadata.SeriesMetadata, error) {
	return nil, metadata.ErrNotFound
}

var adultTopAsked, adultLowAsked bool

func init() {
	for _, a := range []adultMeta{{provider: "adulttop", adult: true, asked: &adultTopAsked}, {provider: "adultlow", asked: &adultLowAsked}} {
		modules.Register(&modules.Implementation{Kind: modules.KindMetadata, Name: a.provider, DisplayName: a.provider,
			Settings: func() any { return &struct{}{} },
			New:      func(modules.Deps, any) (modules.Instance, error) { return a, nil }})
	}
}

// TestHideNSFWMetadata: while NSFW is hidden, metadata searches leave adult
// titles out (a provider with only those counts as having none), and the
// providers are told so; with it off they are found.
func TestHideNSFWMetadata(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			var top int64
			for i, impl := range []string{"adulttop", "adultlow"} {
				def := &model.ProviderDefinition{Kind: "metadata", Implementation: impl, Name: impl, Enabled: true, Priority: i + 1}
				if err := e.App.Modules.Create(e.Ctx, def); err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					top = def.ID
				}
			}
			// hidden by default
			list, _, skipped, _ := e.App.Metadata.SearchFirst(e.Ctx, "Kite", "", 10, 0)
			if len(list) != 1 || list[0].Provider != "adultlow" || len(skipped) != 1 || !adultTopAsked {
				t.Fatalf("hidden: got %+v, skipped %v, top told %v", list, skipped, adultTopAsked)
			}
			if list, _ := e.App.Metadata.Search(e.Ctx, "Kite", 10); len(list) != 1 || list[0].Adult {
				t.Fatalf("merged search kept an adult title: %+v", list)
			}
			if list, _ := e.App.Metadata.SearchModule(e.Ctx, top, "Kite", 10); len(list) != 0 {
				t.Fatalf("one provider's search kept an adult title: %+v", list)
			}
			if c, _, _, _ := e.App.Metadata.FindAtPrimary(e.Ctx, metadataagg.Known{Names: []string{"Kite"}}); c != nil {
				t.Fatalf("matched an adult title: %+v", c)
			}

			if err := e.App.Settings.Set(e.Ctx, settings.KeySources, settings.Sources{HideNSFW: false}); err != nil {
				t.Fatal(err)
			}
			list, _, _, _ = e.App.Metadata.SearchFirst(e.Ctx, "Kite", "", 10, 0)
			if len(list) != 1 || list[0].Provider != "adulttop" || adultTopAsked {
				t.Fatalf("shown: got %+v, top told to hide %v", list, adultTopAsked)
			}
		})
	}
}
