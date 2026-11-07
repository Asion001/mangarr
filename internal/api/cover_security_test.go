package api_test

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/app"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/netguard"
)

// seriesWithCover adds a series whose metadata cover is coverURL.
func seriesWithCover(t *testing.T, a *app.App, coverURL string) int64 {
	t.Helper()
	ctx := context.Background()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: time.Now()}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var pid int64
	_ = a.DB.NewSelect().Table("profiles").Column("id").Limit(1).Scan(ctx, &pid)
	now := time.Now()
	ser := &model.Series{Title: "x", SortTitle: "x", Status: "ongoing", MonitorNew: "all", RootFolderID: rf.ID, Path: "x", ProfileID: pid,
		Tags: []int64{}, BlockedScanlators: []string{}, AddedAt: now, UpdatedAt: now}
	ser.Metadata.CoverURL = coverURL
	if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return ser.ID
}

func coverOf(t *testing.T, srv *httptest.Server, id int64) *http.Response {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/series/" + strconv.FormatInt(id, 10) + "/cover?v=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// A cover link that answers with HTML (or SVG) is never served back from
// mangarr's origin, where it would run script.
func TestCoverIsNeverAPage(t *testing.T) {
	defer netguard.AllowPrivateForTests()()
	for _, body := range []string{"<script>alert(document.domain)</script>", `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"/>`} {
		evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte(body))
		}))
		srv, a := newServer(t, true)
		resp := coverOf(t, srv, seriesWithCover(t, a, evil.URL+"/c"))
		evil.Close()
		if ct := resp.Header.Get("Content-Type"); resp.StatusCode == http.StatusOK || strings.Contains(ct, "html") || strings.Contains(ct, "svg") {
			t.Fatalf("%q served as %d %q", body, resp.StatusCode, ct)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("no nosniff")
		}
	}
}

// Cover links never reach the server's own network.
func TestCoverLinkCantReachLocalNetwork(t *testing.T) {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 6)))
	hits := 0
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write(buf.Bytes())
	}))
	defer local.Close()
	srv, a := newServer(t, true)
	if resp := coverOf(t, srv, seriesWithCover(t, a, local.URL+"/admin")); resp.StatusCode == http.StatusOK || hits > 0 {
		t.Fatalf("status %d, %d requests reached the local server", resp.StatusCode, hits)
	}
}

// A cover is picked from what the providers offer, not given as a link.
func TestCoverPickMustBeOffered(t *testing.T) {
	srv, a := newServer(t, true)
	id := seriesWithCover(t, a, "")
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/v1/series/"+strconv.FormatInt(id, 10)+"/metadata/picks",
		strings.NewReader(`{"coverUrl":"http://169.254.169.254/latest/meta-data/"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
