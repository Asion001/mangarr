package api_test

import (
	"context"
	"encoding/json"
	"github.com/Asion001/mangarr/internal/modules"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/model"
)

func TestSeriesAdaptationsRefreshAndAPI(t *testing.T) {
	fixture, err := os.ReadFile("../modules/metadata/anilist/testdata/adaptations.json")
	if err != nil {
		t.Fatal(err)
	}
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, ring := logging.Setup("error", io.Discard)
			a, err := app.New(ctx, &config.Config{DataDir: t.TempDir(), DB: dsn, AuthDisabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			srv := httptest.NewServer(api.New(a))
			defer srv.Close()
			var phase atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch phase.Load() {
				case 0:
					_, _ = w.Write(append(append([]byte(`{"data":{"Media":`), fixture...), []byte(`}}`)...))
				case 1:
					_, _ = io.WriteString(w, `{"data":{"Media":{"id":700001,"idMal":800001,"title":{"english":"Cloud Lantern"},"relations":{"edges":[{"relationType":"ADAPTATION","node":{"id":700002,"type":"ANIME","format":"MOVIE","title":{"english":"Cloud Lantern Revised"},"startDate":{"year":2024}}}]}}}}`)
				case 2:
					_, _ = io.WriteString(w, `{"errors":[{"message":"temporarily unavailable","status":503}]}`)
				case 3:
					_, _ = io.WriteString(w, `{"data":{"Media":{"id":700001,"idMal":800001,"title":{"english":"Cloud Lantern"},"relations":{"edges":[]}}}}`)
				}
			}))
			defer upstream.Close()
			def := &model.ProviderDefinition{Kind: "metadata", Implementation: "anilist", Name: "Test metadata", Enabled: true,
				Settings: map[string]any{"endpoint": upstream.URL}}
			if err := a.Modules.Create(ctx, def); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			root := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: now}
			if _, err := a.DB.NewInsert().Model(root).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			var profile model.Profile
			if err := a.DB.NewSelect().Model(&profile).Order("id").Limit(1).Scan(ctx); err != nil {
				t.Fatal(err)
			}
			ser := &model.Series{Title: "Cloud Lantern", SortTitle: "cloud lantern", Status: model.StatusOngoing,
				RootFolderID: root.ID, ProfileID: profile.ID, Path: "Cloud Lantern", Language: "en", Tags: []int64{},
				AddedAt: now, UpdatedAt: now}
			if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			detail := func() api.SeriesResource {
				t.Helper()
				var got api.SeriesResource
				if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/series/"+itoa(ser.ID), "", &got); code != 200 {
					t.Fatalf("detail: %d", code)
				}
				if got.Adaptations == nil {
					t.Fatal("adaptations must be an array, not null or omitted")
				}
				return got
			}
			if len(detail().Adaptations) != 0 {
				t.Fatal("series without AniList should have no adaptations")
			}
			ser.Metadata.ExternalIDs = map[string]string{"anilist": "700001"}
			if _, err := a.DB.NewUpdate().Model(ser).Column("metadata").WherePK().Exec(ctx); err != nil {
				t.Fatal(err)
			}
			if changed, err := a.Series.RefreshMetadata(ctx, ser.ID); err != nil || !changed {
				t.Fatalf("initial refresh: %v %v", changed, err)
			}
			got := detail()
			if len(got.Adaptations) != 6 {
				t.Fatalf("persisted adaptations: %+v", got.Adaptations)
			}
			for i, adaptation := range got.Adaptations {
				if !reflect.DeepEqual(adaptation.Adaptation, got.Metadata.Adaptations[i]) || adaptation.WatchLinks == nil || len(adaptation.WatchLinks) != 0 {
					t.Fatalf("adaptation resource: %+v", adaptation)
				}
			}
			if got.LastMetadataRefresh == nil {
				t.Fatal("missing refresh timestamp")
			}
			if changed, err := a.Series.RefreshMetadata(ctx, ser.ID); err != nil || changed {
				t.Fatalf("unchanged refresh: %v %v", changed, err)
			}
			checkAdaptationWatchLinks(t, ctx, a, srv.URL, ser, detail)
			phase.Store(1)
			if changed, err := a.Series.RefreshMetadata(ctx, ser.ID); err != nil || !changed {
				t.Fatalf("updated refresh: %v %v", changed, err)
			}
			got = detail()
			if len(got.Adaptations) != 1 || got.Adaptations[0].Title != "Cloud Lantern Revised" || got.Adaptations[0].Year != 2024 || got.Adaptations[0].Format != "movie" {
				t.Fatalf("refresh: %+v", got.Adaptations)
			}
			phase.Store(2)
			if _, err := a.Series.RefreshMetadata(ctx, ser.ID); err == nil {
				t.Fatal("expected failed refresh")
			}
			if !reflect.DeepEqual(detail().Adaptations, got.Adaptations) {
				t.Fatal("failed refresh changed adaptations")
			}
			phase.Store(3)
			if changed, err := a.Series.RefreshMetadata(ctx, ser.ID); err != nil || !changed {
				t.Fatalf("empty refresh: %v %v", changed, err)
			}
			if len(detail().Adaptations) != 0 {
				t.Fatal("removed adaptations retained")
			}
		})
	}
}

func checkAdaptationWatchLinks(t *testing.T, ctx context.Context, a *app.App, base string, ser *model.Series, detail func() api.SeriesResource) {
	t.Helper()
	var jellyfinHits, siloHits, failedHits atomic.Int32
	jellyfin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jellyfinHits.Add(1)
		if r.Header.Get("X-Emby-Token") != "test-key" {
			t.Error("Jellyfin credential")
		}
		io.WriteString(w, `{"Items":[{"Id":"screen","Name":"Alternate Screen Name","Type":"Series","ProviderIds":{"AniList":"700002"}}],"TotalRecordCount":1}`)
	}))
	defer jellyfin.Close()
	silo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		siloHits.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("Silo credential")
		}
		io.WriteString(w, `{"items":[{"content_id":"series:screen","title":"Cloud Lantern Screen","year":2021,"type":"series"}],"has_more":false}`)
	}))
	defer silo.Close()
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failedHits.Add(1)
		http.Error(w, "test-key", http.StatusUnauthorized)
	}))
	defer failed.Close()
	var defs []api.ModuleResource
	for _, input := range []struct {
		kind, name, address string
		enabled             bool
	}{
		{"jellyfin", "Screen room", jellyfin.URL, true},
		{"silo", "Archive room", silo.URL, true},
		{"jellyfin", "Offline room", failed.URL, true},
		{"silo", "Disabled room", failed.URL, false},
	} {
		body, _ := json.Marshal(api.ModuleInput{Kind: "mediaserver", Implementation: input.kind, Name: input.name, Enabled: input.enabled,
			Settings: map[string]any{"url": input.address, "apiKey": "test-key"}})
		var result api.ModuleResource
		if code := doJSON(t, http.MethodPost, base+"/api/v1/modules", string(body), &result); code != 200 {
			t.Fatalf("create media server: %d", code)
		}
		if result.Settings["apiKey"] != modules.SecretMask {
			t.Fatalf("unmasked secret: %+v", result.Settings)
		}
		defs = append(defs, result)
	}
	first := detail()
	if len(first.Adaptations[0].WatchLinks) != 2 {
		t.Fatalf("watch links: %+v", first.Adaptations)
	}
	links := first.Adaptations[0].WatchLinks
	if links[0].ServerName != "Screen room" || links[0].Kind != "jellyfin" || links[0].URL != jellyfin.URL+"/web/index.html#!/details?id=screen" ||
		links[1].ServerName != "Archive room" || links[1].Kind != "silo" || links[1].URL != silo.URL+"/item/series:screen" {
		t.Fatal(links)
	}
	for _, adaptation := range first.Adaptations[1:] {
		if len(adaptation.WatchLinks) != 0 {
			t.Fatal(adaptation)
		}
	}
	if !reflect.DeepEqual(detail().Adaptations, first.Adaptations) {
		t.Fatal("cached response differs")
	}
	if jellyfinHits.Load() != 1 || siloHits.Load() != 1 || failedHits.Load() != 1 {
		t.Fatal("unexpected upstream calls", jellyfinHits.Load(), siloHits.Load(), failedHits.Load())
	}
	// Lists and stored metadata remain independent of watch-link enrichment.
	var list []api.SeriesResource
	if code := doJSON(t, http.MethodGet, base+"/api/v1/series", "", &list); code != 200 {
		t.Fatal(code)
	}
	for _, row := range list {
		for _, adaptation := range row.Adaptations {
			if len(adaptation.WatchLinks) != 0 {
				t.Fatal("list enriched")
			}
		}
	}
	stored, err := a.Series.Get(ctx, ser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Metadata.Adaptations, first.Metadata.Adaptations) {
		t.Fatal("metadata changed")
	}
	var configured []api.ModuleResource
	if code := doJSON(t, http.MethodGet, base+"/api/v1/modules?kind=mediaserver", "", &configured); code != 200 {
		t.Fatal(code)
	}
	raw, _ := json.Marshal(configured)
	if strings.Contains(string(raw), "test-key") {
		t.Fatal("settings leak")
	}
	// Reload after an admin edit invalidates matches, including failed lookups.
	def := defs[0].ProviderDefinition
	def.Enabled = false
	if err := a.Modules.Update(ctx, &def); err != nil {
		t.Fatal(err)
	}
	if got := detail().Adaptations[0].WatchLinks; len(got) != 1 || got[0].Kind != "silo" {
		t.Fatal(got)
	}
	def.Enabled = true
	def.Name = "Renamed room"
	if err := a.Modules.Update(ctx, &def); err != nil {
		t.Fatal(err)
	}
	if got := detail().Adaptations[0].WatchLinks; len(got) != 2 || got[0].ServerName != "Renamed room" {
		t.Fatal(got)
	}
	if jellyfinHits.Load() != 2 {
		t.Fatal("cache survived reload", jellyfinHits.Load())
	}
	for _, def := range defs {
		if err := a.Modules.Delete(ctx, def.ID); err != nil {
			t.Fatal(err)
		}
	}
	if len(detail().Adaptations[0].WatchLinks) != 0 {
		t.Fatal("deleted server retained")
	}
}
