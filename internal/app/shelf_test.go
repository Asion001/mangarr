package app_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/dbtest"
)

// TestReadingShelf: "Continue reading" lists the next chapter of started
// series (chapter 2, left on page 5); unstarted series aren't on it.
func TestReadingShelf(t *testing.T) {
	f := newKomgaFixture(t, dbtest.DSNs(t)["sqlite"])
	srv := httptest.NewServer(api.New(f.e.App))
	defer srv.Close()
	g, _ := f.e.App.Settings.General(f.e.Ctx)
	req, _ := http.NewRequest("GET", srv.URL+"/api/v1/reading/shelf", nil)
	req.Header.Set("X-Api-Key", g.APIKey)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("shelf: %v %d", err, resp.StatusCode)
	}
	defer resp.Body.Close()
	var shelf api.Shelf
	_ = json.NewDecoder(resp.Body).Decode(&shelf)
	if shelf.UserID != f.reader || len(shelf.Items) != 1 {
		t.Fatalf("shelf %+v", shelf)
	}
	it := shelf.Items[0]
	if it.SeriesID != f.ser.ID || it.Next.ChapterID != f.chs[1].ID || it.Page != 5 || !it.Next.Available || it.Read != 1 || it.Total != 4 ||
		it.CoverURL == "" || it.LastReadAt == nil {
		t.Fatalf("item %+v", it)
	}
}
