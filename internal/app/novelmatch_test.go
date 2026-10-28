package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

// novelMeta lists a light novel ahead of the manga of the same name, as
// MangaUpdates and Kitsu do.
type novelMeta struct{}

var novelMetaSeries = []metadata.SeriesMetadata{
	{Provider: "novelmeta", ID: "n1", Title: "Solo Leveling", Format: "novel"},
	{Provider: "novelmeta", ID: "m1", Title: "Solo Leveling", Format: "manhwa"},
	{Provider: "novelmeta", ID: "n2", Title: "Only a Novel", Format: "novel"},
}

func (novelMeta) Test(context.Context) error { return nil }
func (novelMeta) Search(context.Context, string, int) ([]metadata.SeriesMetadata, error) {
	return novelMetaSeries, nil
}
func (novelMeta) Get(_ context.Context, id string) (*metadata.SeriesMetadata, error) {
	for _, md := range novelMetaSeries {
		if md.ID == id {
			return &md, nil
		}
	}
	return nil, errors.New("not found")
}

func init() {
	modules.Register(&modules.Implementation{Kind: modules.KindMetadata, Name: "novelmeta", DisplayName: "Novel metadata",
		Settings: func() any { return &struct{}{} },
		New:      func(modules.Deps, any) (modules.Instance, error) { return novelMeta{}, nil }})
}

// TestTitleMatchSkipsNovels: an import or a duplicate check matching a
// title by name takes the manga, never the light novel that shares it.
func TestTitleMatchSkipsNovels(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			def := &model.ProviderDefinition{Kind: "metadata", Implementation: "novelmeta", Name: "Novels", Enabled: true}
			if err := e.App.Modules.Create(e.Ctx, def); err != nil {
				t.Fatal(err)
			}
			if c, _ := imports.MatchTitleCandidate(e.Ctx, e.App.Metadata, "Solo Leveling", nil); c == nil || c.ID != "m1" {
				t.Fatalf("Solo Leveling matched %+v, want the manhwa m1", c)
			}
			if c, _ := imports.MatchTitleCandidate(e.Ctx, e.App.Metadata, "Only a Novel", nil); c != nil {
				t.Fatalf("a novel-only title matched %+v", c)
			}
		})
	}
}
