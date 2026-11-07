package settings_test

import (
	"encoding/json"
	"testing"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/settings"
)

// TestOverlayOnColdRead: a field pinned by the environment applies on the
// first read of a document that was never stored, before the cache is warm.
func TestOverlayOnColdRead(t *testing.T) {
	st := settings.NewStore(dbtest.SQLite(t))
	st.SetOverlay("test.doc", json.RawMessage(`{"name":"pinned"}`), []settings.Lock{{Path: "name", Env: "TEST_NAME"}})
	for i := range 2 {
		doc := struct {
			Name string `json:"name"`
		}{Name: "default"}
		if err := st.Get(t.Context(), "test.doc", &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Name != "pinned" {
			t.Fatalf("read %d: name = %q, want the pinned value", i+1, doc.Name)
		}
	}
}
