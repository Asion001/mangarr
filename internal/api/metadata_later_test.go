package api_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/config"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/logging"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/series"
)

// A series imported while its metadata provider is down keeps the link and
// gets its metadata once the provider answers again.
func TestMetadataLaterWhileProviderIsDown(t *testing.T) {
	for dialect, dsn := range dbtest.DSNs(t) {
		t.Run(dialect, func(t *testing.T) {
			ctx := context.Background()
			_, ring := logging.Setup("error", io.Discard)
			a, err := app.New(ctx, &config.Config{DataDir: t.TempDir(), DB: dsn, AuthDisabled: true}, slog.New(slog.NewTextHandler(io.Discard, nil)), ring)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			var up atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !up.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":{"Media":{"id":700001,"title":{"english":"Cloud Lantern","romaji":"Kumo no Rantan"},"description":"About lanterns","relations":{"edges":[]}}}}`)
			}))
			defer upstream.Close()
			def := &model.ProviderDefinition{Kind: "metadata", Implementation: "anilist", Name: "Test metadata", Enabled: true,
				Settings: map[string]any{"endpoint": upstream.URL}}
			if err := a.Modules.Create(ctx, def); err != nil {
				t.Fatal(err)
			}
			root := &model.RootFolder{Path: t.TempDir(), Language: "en", CreatedAt: time.Now().UTC()}
			if _, err := a.DB.NewInsert().Model(root).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			req := series.AddRequest{Title: "Lantern (source)", RootFolderID: root.ID, Monitor: model.MonitorAll, NoRefresh: true,
				Metadata: &metadataagg.Ref{ModuleID: def.ID, Provider: "anilist", ID: "700001"},
				Sources:  []series.SourceLink{{ModuleID: 999, SourceID: "src", URL: "/m/1", SourceName: "Source", Lang: "en"}}}
			if _, err := a.Series.Add(ctx, req); err == nil {
				t.Fatal("without MetadataLater a provider outage fails the add")
			}
			req.MetadataLater = true
			ser, err := a.Series.Add(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			if ser.Title != "Lantern (source)" || ser.Metadata.ExternalIDs["anilist"] != "700001" {
				t.Fatalf("added: %q %v", ser.Title, ser.Metadata.ExternalIDs)
			}
			pending, err := a.Series.PendingMetadata(ctx, time.Now().Add(-time.Hour))
			if err != nil || len(pending) != 1 || pending[0] != ser.ID {
				t.Fatalf("pending: %v %v", pending, err)
			}
			up.Store(true)
			if err := a.Modules.Update(ctx, def); err != nil { // a fresh instance, without the outage pause
				t.Fatal(err)
			}
			if changed, err := a.Series.RefreshMetadata(ctx, ser.ID); err != nil || !changed {
				t.Fatalf("refresh: %v %v", changed, err)
			}
			got, err := a.Series.Get(ctx, ser.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Title != "Cloud Lantern" || got.Metadata.Description != "About lanterns" || got.Metadata.Titles["romanized"] != "Kumo no Rantan" {
				t.Fatalf("filled in: %q %+v", got.Title, got.Metadata)
			}
			if pending, _ := a.Series.PendingMetadata(ctx, time.Now().Add(-time.Hour)); len(pending) != 0 {
				t.Fatalf("still pending: %v", pending)
			}
		})
	}
}
