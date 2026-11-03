package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/testutil/tonepack"
)

// TestSoundPacksAPI: admins upload a pack and make moods with its sounds; readers stream
// its sounds (with Range and ETag), place their own cues, and export and
// import soundtracks; leftovers show in the cleanup plan.
func TestSoundPacksAPI(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	bossC := login(t, srv.URL, "boss", "boss-pass-1")
	boss := caller{t, bossC, srv.URL}
	annC := login(t, srv.URL, "ann", "ann-pass-1")
	ann := caller{t, annC, srv.URL}

	upload := func(c *http.Client, path string) (int, []byte) {
		t.Helper()
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		resp, err := c.Post(srv.URL+"/api/v1/audio/packs/upload", "application/zip", f)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	zipPath := tonepack.Zip(t, "test-tones", "1.0.0")
	if code, _ := upload(annC, zipPath); code != 403 {
		t.Fatalf("a reader uploading a pack: %d", code)
	}
	code, body := upload(bossC, zipPath)
	var pack struct {
		ID   int64
		Slug string
	}
	_ = json.Unmarshal(body, &pack)
	if code != 200 || pack.Slug != "test-tones" {
		t.Fatalf("upload: %d %s", code, body)
	}
	bad := filepath.Join(t.TempDir(), "bad.zip")
	_ = os.WriteFile(bad, []byte("not a zip"), 0o644)
	if code, _ := upload(bossC, bad); code != 400 {
		t.Fatalf("a broken zip: %d", code)
	}

	var packs struct {
		Packs []struct{ Ambience, SFX int }
		Usage struct {
			Size      int64
			QuotaSize int64
			Over      bool
		}
	}
	if code := boss.do("GET", "/api/v1/audio/packs", "", &packs); code != 200 || len(packs.Packs) != 1 || packs.Packs[0].Ambience != 2 || packs.Usage.QuotaSize != 2048<<20 {
		t.Fatalf("packs %d %+v", code, packs)
	}
	if code := boss.do("PUT", "/api/v1/settings/sound", `{"catalogUrl":"ftp://nope","quotaMb":1,"suggestFromGenres":false}`, nil); code != 400 {
		t.Fatalf("a bad catalog URL: %d", code)
	}
	if code := boss.do("PUT", "/api/v1/settings/sound", `{"catalogUrl":"","quotaMb":0,"suggestFromGenres":true}`, nil); code != 200 {
		t.Fatalf("sound settings: %d", code)
	}
	var catalog []any
	if code := boss.do("GET", "/api/v1/audio/catalog", "", &catalog); code != 200 || len(catalog) != 0 {
		t.Fatalf("no catalog: %d %v", code, catalog)
	}

	var tracks []struct {
		ID     int64
		Title  string
		Kind   string
		SHA256 string
		Pack   string
	}
	if code := ann.do("GET", "/api/v1/audio/tracks?kind=ambience", "", &tracks); code != 200 || len(tracks) != 2 || tracks[0].Pack != "Test tones" {
		t.Fatalf("tracks %d %+v", code, tracks)
	}
	hum := tracks[1]
	if hum.Title != "Low hum" {
		hum = tracks[0]
	}
	stream := "/api/v1/audio/tracks/" + strconv.FormatInt(hum.ID, 10) + "/stream"
	req, _ := http.NewRequest("GET", srv.URL+stream, nil)
	req.Header.Set("Range", "bytes=0-11")
	resp, err := annC.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	head, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(head) != "RIFF"+string(head[4:8])+"WAVE" || resp.Header.Get("ETag") != `"`+hum.SHA256+`"` || resp.Header.Get("Content-Type") != "audio/wav" {
		t.Fatalf("range: %d %q %v", resp.StatusCode, head, resp.Header)
	}
	req, _ = http.NewRequest("GET", srv.URL+stream, nil)
	req.Header.Set("If-None-Match", `"`+hum.SHA256+`"`)
	if resp, err = annC.Do(req); err != nil || resp.StatusCode != http.StatusNotModified {
		t.Fatalf("if-none-match: %v %d", err, resp.StatusCode)
	}
	resp.Body.Close()

	// moods with sounds: readers see them, admins edit them
	type moodView struct {
		ID       int64
		Name     string
		Ambience []struct{ TrackID int64 }
	}
	var moods []moodView
	if code := ann.do("GET", "/api/v1/audio/moods", "", &moods); code != 200 || !slices.ContainsFunc(moods, func(m moodView) bool {
		return m.Name == "Humming room" && len(m.Ambience) == 1
	}) {
		t.Fatalf("pack moods: %d %+v", code, moods)
	}
	moodBody := `{"name":"Hum","color":"#3fb98a","icon":"music","ambience":[{"trackId":` + strconv.FormatInt(hum.ID, 10) + `,"volume":0.4}]}`
	if code := ann.do("POST", "/api/v1/audio/moods", moodBody, nil); code != 403 {
		t.Fatalf("a reader making a mood: %d", code)
	}
	var made moodView
	if code := boss.do("POST", "/api/v1/audio/moods", moodBody, &made); code != 200 || made.ID == 0 || len(made.Ambience) != 1 {
		t.Fatalf("make a mood: %d %+v", code, made)
	}
	// a change that leaves the sounds out keeps them
	if code := boss.do("PUT", "/api/v1/audio/moods/"+strconv.FormatInt(made.ID, 10), `{"name":"Hum","color":"#3fb98a","icon":"wind"}`, &made); code != 200 || len(made.Ambience) != 1 {
		t.Fatalf("change a mood: %d %+v", code, made)
	}
	moodID := strconv.FormatInt(made.ID, 10)

	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	_, _ = a.DB.NewInsert().Model(rf).Exec(ctx)
	ser := &model.Series{Title: "Harbor", SortTitle: "harbor", Status: model.StatusOngoing, RootFolderID: rf.ID, Path: "Harbor", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ch := &model.Chapter{SeriesID: ser.ID, NumberKey: "4", NumberSort: 4, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
	_, _ = a.DB.NewInsert().Model(ch).Exec(ctx)
	sid := strconv.FormatInt(ser.ID, 10)

	type stView struct {
		Shared, Personal *struct {
			Cues []struct {
				ChapterNumber float64
				Page          *int
				MoodID        *int64
			}
		}
		Credits  []struct{ Title, License string }
		Chapters []struct{ Number float64 }
	}
	var st stView
	if code := ann.do("PUT", "/api/v1/series/"+sid+"/soundtrack/cue", `{"chapterNumber":4,"moodId":`+moodID+`}`, nil); code != 403 {
		t.Fatalf("a reader changing the shared cues: %d", code)
	}
	if code := ann.do("PUT", "/api/v1/me/series/"+sid+"/soundtrack/cue", `{"chapterNumber":4,"page":3,"moodId":`+moodID+`}`, &st); code != 200 ||
		st.Personal == nil || len(st.Personal.Cues) != 1 || len(st.Credits) != 1 || st.Credits[0].Title != "Low hum" || len(st.Chapters) != 1 {
		t.Fatalf("my cue: %d %+v", code, st)
	}
	var r struct {
		Source string
		Pages  []struct {
			Page  int
			Sound struct {
				Source string
				Mood   struct{ ID int64 }
			}
		}
	}
	if code := ann.do("GET", "/api/v1/read/chapters/"+strconv.FormatInt(ch.ID, 10)+"/soundtrack", "", &r); code != 200 || len(r.Pages) != 1 || r.Pages[0].Sound.Mood.ID != made.ID || r.Pages[0].Sound.Source != "personal" {
		t.Fatalf("resolved: %d %+v", code, r)
	}

	// export mine, import it as the shared one
	resp, err = annC.Get(srv.URL + "/api/v1/series/" + sid + "/soundtrack/export?version=personal")
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Disposition"), "Harbor.soundtrack.json") || !strings.Contains(string(exported), hum.SHA256) {
		t.Fatalf("export: %d %v %s", resp.StatusCode, resp.Header, exported)
	}
	var imp struct {
		Result     struct{ Cues, MoodsCreated, MissingSounds int }
		Soundtrack stView
	}
	if code := ann.do("POST", "/api/v1/series/"+sid+"/soundtrack/import", string(exported), nil); code != 403 {
		t.Fatalf("a reader importing the shared soundtrack: %d", code)
	}
	if code := boss.do("POST", "/api/v1/series/"+sid+"/soundtrack/import", string(exported), &imp); code != 200 || imp.Result.Cues != 1 || imp.Soundtrack.Shared == nil {
		t.Fatalf("import: %d %+v", code, imp)
	}
	if code := ann.do("POST", "/api/v1/me/series/"+sid+"/soundtrack/import", `{"format":"something","version":1,"cues":[]}`, nil); code != 422 {
		t.Fatalf("import another kind of file: %d", code)
	}

	// moods found from chapter titles: admins only, as automatic cues
	if _, err := a.DB.NewUpdate().Model((*model.Chapter)(nil)).Set("title = ?", "The Battle Begins").Where("id = ?", ch.ID).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if code := ann.do("POST", "/api/v1/series/"+sid+"/soundtrack/detect", "", nil); code != 403 {
		t.Fatalf("a reader finding moods: %d", code)
	}
	var det struct {
		Detection  struct{ Chapters, Cues int }
		Soundtrack struct {
			Shared *struct {
				Cues []struct {
					ChapterNumber float64
					Source        string
					Confidence    *float64
				}
			}
		}
	}
	if code := boss.do("POST", "/api/v1/series/"+sid+"/soundtrack/detect", "", &det); code != 200 || det.Detection.Cues != 1 {
		t.Fatalf("detect: %d %+v", code, det)
	}
	auto := 0
	for _, c := range det.Soundtrack.Shared.Cues {
		if c.Source == "auto" && c.ChapterNumber == 4 && c.Confidence != nil {
			auto++
		}
	}
	if auto != 1 {
		t.Fatalf("auto cue: %+v", det.Soundtrack.Shared)
	}
	if code := boss.do("POST", "/api/v1/soundtracks/detect", "", nil); code != 200 {
		t.Fatalf("detect the library: %d", code)
	}

	// files nothing uses show in the cleanup plan
	junk := filepath.Join(a.Packs.Dir, "test-tones", "0000.ogg")
	if err := os.WriteFile(junk, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	var plan struct {
		Leftovers    []struct{ Path string }
		LeftoverSize int64
	}
	if code := boss.do("GET", "/api/v1/cleanup/preview", "", &plan); code != 200 || len(plan.Leftovers) != 1 || plan.Leftovers[0].Path != junk || plan.LeftoverSize != 1 {
		t.Fatalf("cleanup plan: %d %+v", code, plan)
	}

	if code := boss.do("DELETE", "/api/v1/audio/packs/"+strconv.FormatInt(pack.ID, 10), "", nil); code != 204 {
		t.Fatalf("delete the pack: %d", code)
	}
	if code := ann.do("GET", stream, "", nil); code != 404 {
		t.Fatalf("a deleted pack's sound: %d", code)
	}
}
