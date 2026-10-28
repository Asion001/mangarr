package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/requests"
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

// TestNovelRequests: nobody can ask for a novel, and one asked for before
// is matched to its manga when requests are added together.
func TestNovelRequests(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			ctx := e.Ctx
			def := &model.ProviderDefinition{Kind: "metadata", Implementation: "novelmeta", Name: "Novels", Enabled: true}
			if err := e.App.Modules.Create(ctx, def); err != nil {
				t.Fatal(err)
			}
			u, err := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
			if err != nil {
				t.Fatal(err)
			}
			p, _ := e.App.Auth.UserPrincipal(ctx, u.ID)
			if _, _, err := e.App.Requests.Create(ctx, p, requests.CreateInput{ModuleID: def.ID, MetaID: "n1"}); !errors.As(err, new(requests.ValidationError)) {
				t.Fatalf("asking for a novel: %v", err)
			}
			if _, _, err := e.App.Requests.Create(ctx, p, requests.CreateInput{ModuleID: def.ID, MetaID: "m1"}); err != nil {
				t.Fatalf("asking for the manhwa: %v", err)
			}
			old := model.Request{ID: 999, Title: "Solo Leveling", Metadata: model.RequestMetadata{ModuleID: def.ID, Provider: "novelmeta", ID: "n1", Format: "novel",
				ExternalIDs: map[string]string{"anilist": "1"}}}
			imp, err := e.App.Imports.CreateFromRequests(ctx, []model.Request{old}, nil)
			if err != nil {
				t.Fatal(err)
			}
			e.runCommand(t, "MapImport", map[string]any{"importId": imp.ID})
			entries, _, err := e.App.Imports.Entries(ctx, imp.ID, imports.EntryFilter{}, 0, 0)
			if err != nil || len(entries) != 1 || entries[0].Metadata == nil || entries[0].Metadata.ID != "m1" {
				t.Fatalf("entries = %+v %v", entries, err)
			}
		})
	}
}
