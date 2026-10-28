package app_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
)

// knownMeta is a provider that knows some titles and counts its searches.
type knownMeta struct {
	provider string
	titles   []string
	calls    *atomic.Int32
}

func (knownMeta) Test(context.Context) error { return nil }
func (k knownMeta) Search(_ context.Context, q string, _ int) ([]metadata.SeriesMetadata, error) {
	k.calls.Add(1)
	for _, t := range k.titles {
		if t == q {
			return []metadata.SeriesMetadata{{Provider: k.provider, ID: k.provider + ":" + t, Title: t, Format: "manga"}}, nil
		}
	}
	return []metadata.SeriesMetadata{{Provider: k.provider, ID: k.provider + ":other", Title: "Something Else Entirely"}}, nil
}
func (k knownMeta) Get(_ context.Context, id string) (*metadata.SeriesMetadata, error) {
	return nil, errors.New("not found")
}

var topCalls, lowCalls atomic.Int32

func init() {
	for _, k := range []knownMeta{
		{provider: "topmeta", titles: []string{"Berserk"}, calls: &topCalls},
		{provider: "lowmeta", titles: []string{"Berserk", "Vagabond"}, calls: &lowCalls},
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
			if c, _ := imports.MatchTitleCandidate(e.Ctx, e.App.Metadata, "Vagabond", []string{"Vagabond (2)"}); c == nil || c.Provider != "lowmeta" {
				t.Fatalf("Vagabond matched %+v, want the lower provider", c)
			}
			if topCalls.Load() != 3 {
				t.Fatalf("top provider searched %d times, want once for Berserk and once per name of Vagabond", topCalls.Load())
			}
		})
	}
}
