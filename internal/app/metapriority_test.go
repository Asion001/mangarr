package app_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
)

// knownMeta is a provider that knows some titles and counts its searches.
type knownMeta struct {
	provider string
	titles   []string
	calls    *atomic.Int32
	// ids are other providers' ids its titles carry (provider -> title -> id).
	ids map[string]map[string]string
	// byID are the ids it finds a title by (provider:id -> title).
	byID map[string]string
}

func (knownMeta) Test(context.Context) error { return nil }
func (k knownMeta) Search(_ context.Context, q string, _ int) ([]metadata.SeriesMetadata, error) {
	k.calls.Add(1)
	for _, t := range k.titles {
		if strings.Contains(t, q) {
			return []metadata.SeriesMetadata{{Provider: k.provider, ID: k.provider + ":" + t, Title: t, Format: "manga"}}, nil
		}
	}
	return []metadata.SeriesMetadata{{Provider: k.provider, ID: k.provider + ":other", Title: "Something Else Entirely"}}, nil
}
func (k knownMeta) Get(_ context.Context, id string) (*metadata.SeriesMetadata, error) {
	for _, t := range k.titles {
		if id == k.provider+":"+t {
			md := metadata.SeriesMetadata{Provider: k.provider, ID: id, Title: t, Format: "manga", ExternalIDs: map[string]string{}}
			for p, m := range k.ids {
				if v := m[t]; v != "" {
					md.ExternalIDs[p] = v
				}
			}
			return &md, nil
		}
	}
	return nil, errors.New("not found")
}

func (k knownMeta) LookupExternal(ctx context.Context, provider, id string) (*metadata.SeriesMetadata, error) {
	if t, ok := k.byID[provider+":"+id]; ok {
		return k.Get(ctx, k.provider+":"+t)
	}
	return nil, metadata.ErrNotFound
}

var topCalls, lowCalls atomic.Int32

func init() {
	for _, k := range []knownMeta{
		// the top one spells Vagabond another way, and knows Bagabondo by an id
		{provider: "topmeta", titles: []string{"Berserk", "Vagabond (Manga)", "Bagabondo"}, calls: &topCalls, byID: map[string]string{"midmeta:7": "Bagabondo"}},
		{provider: "lowmeta", titles: []string{"Berserk", "Vagabond", "Monster"}, calls: &lowCalls},
	} {
		modules.Register(&modules.Implementation{Kind: modules.KindMetadata, Name: k.provider, DisplayName: k.provider,
			Settings: func() any { return &struct{}{} },
			New:      func(modules.Deps, any) (modules.Instance, error) { return k, nil }})
	}
}

// TestTitleMatchByPriority: a title is looked up at the providers one at a
// time, by priority; a lower one is asked only when the ones above know none
// of its names.
func TestTitleMatchByPriority(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			for i, impl := range []string{"topmeta", "lowmeta"} {
				def := &model.ProviderDefinition{Kind: "metadata", Implementation: impl, Name: impl, Enabled: true, Priority: i + 1}
				if err := e.App.Modules.Create(e.Ctx, def); err != nil {
					t.Fatal(err)
				}
			}
			topCalls.Store(0)
			lowCalls.Store(0)
			if c, _ := imports.MatchTitleCandidate(e.Ctx, e.App.Metadata, "Berserk", nil); c == nil || c.Provider != "topmeta" {
				t.Fatalf("Berserk matched %+v, want the top provider", c)
			}
			if lowCalls.Load() != 0 {
				t.Fatalf("the lower provider was asked %d times though the top one knew the title", lowCalls.Load())
			}
			if c, _ := imports.MatchTitleCandidate(e.Ctx, e.App.Metadata, "Monster", []string{"Monster (2)"}); c == nil || c.Provider != "lowmeta" {
				t.Fatalf("Monster matched %+v, want the lower provider", c)
			}
			if topCalls.Load() != 3 {
				t.Fatalf("top provider searched %d times, want once for Berserk and once per name of Monster", topCalls.Load())
			}
		})
	}
}

// TestMapToPrimary: a title linked at a lower provider is looked up at the
// primary one by its names too, unless that is turned off.
func TestMapToPrimary(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			var low int64
			for i, impl := range []string{"topmeta", "lowmeta"} {
				def := &model.ProviderDefinition{Kind: "metadata", Implementation: impl, Name: impl, Enabled: true, Priority: i + 1}
				if err := e.App.Modules.Create(e.Ctx, def); err != nil {
					t.Fatal(err)
				}
				low = def.ID
			}
			r, err := e.App.Metadata.Resolve(e.Ctx, metadataagg.Ref{ModuleID: low, Provider: "lowmeta", ID: "lowmeta:Vagabond"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if r.Metadata.ExternalIDs["topmeta"] != "topmeta:Vagabond (Manga)" || r.Provenance["title"] != "lowmeta" {
				t.Fatalf("ids %v, provenance %v: want the top provider mapped, the match's own name kept", r.Metadata.ExternalIDs, r.Provenance)
			}
			c, how, by, _ := e.App.Metadata.FindAtPrimary(e.Ctx, metadataagg.Known{IDs: map[string]string{"midmeta": "7"}})
			if c == nil || c.ID != "topmeta:Bagabondo" || how != metadataagg.FoundByID || by != "midmeta" {
				t.Fatalf("FindAtPrimary = %+v %q %q", c, how, by)
			}
			if c, how, _, _ := e.App.Metadata.FindAtPrimary(e.Ctx, metadataagg.Known{Names: []string{"Berserk"}}); c == nil || how != metadataagg.FoundByName {
				t.Fatalf("by name = %+v %q", c, how)
			}
			if err := e.App.Settings.Set(e.Ctx, settings.KeyMetadata, settings.Metadata{MapToPrimary: false}); err != nil {
				t.Fatal(err)
			}
			r, err = e.App.Metadata.Resolve(e.Ctx, metadataagg.Ref{ModuleID: low, Provider: "lowmeta", ID: "lowmeta:Vagabond"}, nil)
			if err != nil || r.Metadata.ExternalIDs["topmeta"] != "" {
				t.Fatalf("mapping off: %v %v", r.Metadata.ExternalIDs, err)
			}
		})
	}
}

// TestPickMetadata: a title moved to the primary provider keeps its other
// providers, fields picked from one of them stay through a refresh, and
// handing them back takes the providers' again.
func TestPickMetadata(t *testing.T) {
	for name, dsn := range dbtest.DSNs(t) {
		t.Run(name, func(t *testing.T) {
			e := newTestApp(t, dsn)
			ids := map[string]int64{}
			for i, impl := range []string{"topmeta", "lowmeta"} {
				def := &model.ProviderDefinition{Kind: "metadata", Implementation: impl, Name: impl, Enabled: true, Priority: i + 1}
				if err := e.App.Modules.Create(e.Ctx, def); err != nil {
					t.Fatal(err)
				}
				ids[impl] = def.ID
			}
			mod := e.addFakeModule(t, "pick-metadata")
			ser, err := e.App.Series.Add(e.Ctx, series.AddRequest{Title: "Kenpuu Denki", RootFolderID: e.RFID, Monitor: model.MonitorNone, NoRefresh: true,
				Sources: []series.SourceLink{{ModuleID: mod, SourceID: "A", URL: "/A/kenpuu", SourceName: "Source A", Lang: "en"}}})
			if err != nil {
				t.Fatal(err)
			}
			for _, impl := range []string{"lowmeta", "topmeta"} {
				if _, err := e.App.Series.AddMetadataRef(e.Ctx, ser.ID, metadataagg.Ref{ModuleID: ids[impl], Provider: impl, ID: impl + ":Berserk"}); err != nil {
					t.Fatal(err)
				}
			}
			got, _ := e.App.Series.Get(e.Ctx, ser.ID)
			if got.Metadata.ExternalIDs["lowmeta"] == "" || got.Metadata.ExternalIDs["topmeta"] == "" || got.Metadata.Provenance["title"] != "topmeta" {
				t.Fatalf("after the move: ids %v, provenance %v", got.Metadata.ExternalIDs, got.Metadata.Provenance)
			}
			if opts := e.App.Metadata.Options(e.Ctx, got.Metadata.ExternalIDs, ""); len(opts) != 2 || opts[0].Provider != "topmeta" {
				t.Fatalf("options %+v", opts)
			}
			mine := "Berserk (my pick)"
			if _, err := e.App.Series.PickMetadata(e.Ctx, ser.ID, series.MetadataPicks{Title: &mine, Genres: []string{"Dark Fantasy"}, From: map[string]string{"genres": "lowmeta"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.App.Series.RefreshMetadata(e.Ctx, ser.ID); err != nil {
				t.Fatal(err)
			}
			got, _ = e.App.Series.Get(e.Ctx, ser.ID)
			if got.Title != mine || len(got.Metadata.Genres) != 1 || got.Metadata.Provenance["genres"] != "lowmeta" || !got.Metadata.Locked("title") {
				t.Fatalf("picks lost: %q %v %v", got.Title, got.Metadata.Genres, got.Metadata.Provenance)
			}
			if _, err := e.App.Series.PickMetadata(e.Ctx, ser.ID, series.MetadataPicks{Automatic: []string{"title"}}); err != nil {
				t.Fatal(err)
			}
			got, _ = e.App.Series.Get(e.Ctx, ser.ID)
			if got.Title != "Berserk" || got.Metadata.Locked("title") || !got.Metadata.Locked("genres") {
				t.Fatalf("back to automatic: %q locks %v", got.Title, got.Metadata.Locks)
			}
		})
	}
}
