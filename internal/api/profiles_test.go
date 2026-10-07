package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/api"
	"github.com/Asion001/mangarr/internal/model"
)

func doJSON(t *testing.T, method, url, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func TestSingleDefaultProfile(t *testing.T) {
	srv, _ := newServer(t, true)
	var list []model.Profile
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/profiles", "", &list)
	p := list[0]
	p.ID, p.Name, p.IsDefault = 0, "Webtoons", true
	p.Config.Upscale.UpscalerID = 999 // worker selection is installation-wide
	body, _ := json.Marshal(p)
	var created model.Profile
	if code := doJSON(t, http.MethodPost, srv.URL+"/api/v1/profiles", string(body), &created); code != 200 {
		t.Fatalf("create: %d", code)
	}
	if created.Config.Upscale.UpscalerID != 0 {
		t.Fatalf("profile pinned an upscaler: %+v", created.Config.Upscale)
	}
	list = nil
	doJSON(t, http.MethodGet, srv.URL+"/api/v1/profiles", "", &list)
	defaults := 0
	for _, p := range list {
		if p.IsDefault {
			defaults++
			if p.ID != created.ID {
				t.Fatalf("old profile %q still default", p.Name)
			}
		}
	}
	if len(list) != 2 || defaults != 1 {
		t.Fatalf("want 2 profiles with 1 default, got %+v", list)
	}
	// the default can't be un-defaulted without choosing another one
	created.IsDefault = false
	body, _ = json.Marshal(created)
	if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/profiles/"+itoa(created.ID), string(body), nil); code != 400 {
		t.Fatalf("un-default: %d", code)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// TestProfileProcessingChangeAsksFirst keeps a profile change from
// re-processing chapters already downloaded: the estimate counts them, the
// person opts in once, and the next change waits for them to ask again.
func TestProfileProcessingChangeAsksFirst(t *testing.T) {
	srv, a := newServer(t, true)
	ctx := context.Background()
	past := time.Now().UTC().Add(-time.Hour)
	root := &model.RootFolder{Path: t.TempDir(), CreatedAt: past}
	if _, err := a.DB.NewInsert().Model(root).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	var p model.Profile
	if err := a.DB.NewSelect().Model(&p).Where("is_default = ?", true).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	ser := &model.Series{Title: "Reprocess", SortTitle: "reprocess", RootFolderID: root.ID, ProfileID: p.ID, Path: "Reprocess", Tags: []int64{}, AddedAt: past, UpdatedAt: past}
	if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		ch := &model.Chapter{SeriesID: ser.ID, NumberKey: fmt.Sprint(i), NumberSort: float64(i), FirstSeenAt: past, UpdatedAt: past}
		if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: ser.ID, RelativePath: fmt.Sprintf("%d.cbz", i), Size: 100, ImportedAt: past}
		if _, err := a.DB.NewInsert().Model(f).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	put := func(cfg func(*model.ProfileConfig)) model.Profile {
		t.Helper()
		cfg(&p.Config)
		body, _ := json.Marshal(p)
		var saved model.Profile
		if code := doJSON(t, http.MethodPut, srv.URL+"/api/v1/profiles/"+itoa(p.ID), string(body), &saved); code != 200 {
			t.Fatalf("update: %d", code)
		}
		p = saved
		return saved
	}
	estimate := func() int {
		t.Helper()
		var est api.ProcessEstimate
		if code := doJSON(t, http.MethodGet, srv.URL+"/api/v1/profiles/"+itoa(p.ID)+"/process-estimate", "", &est); code != 200 {
			t.Fatalf("estimate: %d", code)
		}
		return est.Files
	}

	put(func(c *model.ProfileConfig) { c.Encode = model.EncodeConfig{Format: "avif", Preset: "fast"} })
	if n := estimate(); n != 3 {
		t.Fatalf("estimate after change = %d, want 3", n)
	}
	if saved := put(func(c *model.ProfileConfig) { c.ProcessExisting = true }); !saved.Config.ProcessExisting {
		t.Fatal("opting in to existing chapters did not stick")
	}
	if saved := put(func(c *model.ProfileConfig) { c.Encode.Preset = "max" }); saved.Config.ProcessExisting {
		t.Fatal("a later processing change kept processing existing chapters without asking")
	}
	if saved := put(func(c *model.ProfileConfig) { c.ProcessExisting = true; c.MinPages = 2 }); !saved.Config.ProcessExisting {
		t.Fatal("a change outside processing cleared the opt-in")
	}
}
