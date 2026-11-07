package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/soundtrack"
)

// fakeSpotify answers like Spotify's accounts service and Web API.
type fakeSpotify struct {
	mu        sync.Mutex
	challenge string
	plays     []string
	transfers []string
	woke      bool
	shuffles  []string
	volumes   []string
	searches  []url.Values
	refreshes int
	skips     int
}

func (f *fakeSpotify) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("client_id") != "abc123def456" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if r.Form.Get("code") != "good-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge || !strings.HasSuffix(r.Form.Get("redirect_uri"), "/api/v1/me/spotify/callback") {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"bad code"}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-1","refresh_token":"refresh-secret","expires_in":30,"scope":"streaming"}`))
		case "refresh_token":
			if r.Form.Get("refresh_token") != "refresh-secret" {
				w.WriteHeader(400)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			f.refreshes++
			_, _ = w.Write([]byte(`{"access_token":"access-2","expires_in":3600}`))
		}
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-") {
				w.WriteHeader(401)
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			h(w, r)
		}
	}
	mux.HandleFunc("GET /v1/me", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"listener","display_name":"Listener","product":"premium"}`))
	}))
	mux.HandleFunc("GET /v1/me/player/devices", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"devices":[{"id":"phone-1","name":"Phone","type":"Smartphone","is_active":true,"volume_percent":40},{"id":"tv","name":"TV","type":"TV","is_restricted":true}]}`))
	}))
	mux.HandleFunc("PUT /v1/me/player", authed(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.transfers = append(f.transfers, string(b))
		if strings.Contains(string(b), "gone-1") {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"status":404,"message":"Device not found"}}`))
			return
		}
		f.woke = true
		w.WriteHeader(204)
	}))
	mux.HandleFunc("PUT /v1/me/player/play", authed(func(w http.ResponseWriter, r *http.Request) {
		// a browser's player that just started plays once playback moved to it
		if d := r.URL.Query().Get("device_id"); d == "gone-1" || d == "browser-1" && !f.woke {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":{"status":404,"message":"Device not found"}}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		f.plays = append(f.plays, r.URL.Query().Get("device_id")+" "+string(b))
		w.WriteHeader(204)
	}))
	mux.HandleFunc("PUT /v1/me/player/shuffle", authed(func(w http.ResponseWriter, r *http.Request) {
		f.shuffles = append(f.shuffles, r.URL.Query().Get("state"))
		w.WriteHeader(204)
	}))
	mux.HandleFunc("PUT /v1/me/player/volume", authed(func(w http.ResponseWriter, r *http.Request) {
		f.volumes = append(f.volumes, r.URL.Query().Get("volume_percent"))
		if r.URL.Query().Get("device_id") == "phone-1" {
			// phones' Spotify apps refuse remote volume changes
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":{"status":403,"message":"Player command failed: Cannot control device volume","reason":"VOLUME_CONTROL_DISALLOW"}}`))
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /v1/me/player/next", authed(func(w http.ResponseWriter, r *http.Request) {
		f.skips++
		w.WriteHeader(204)
	}))
	mux.HandleFunc("PUT /v1/me/player/pause", authed(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"status":403,"message":"Premium required","reason":"PREMIUM_REQUIRED"}}`))
	}))
	mux.HandleFunc("GET /v1/search", authed(func(w http.ResponseWriter, r *http.Request) {
		f.searches = append(f.searches, r.URL.Query())
		_, _ = w.Write([]byte(`{
			"playlists":{"items":[null,{"uri":"spotify:playlist:37i9dQZF1DX4sWSpwq3LiO","name":"Rainy focus","owner":{"display_name":"Someone"},"items":{"total":42},
				"images":[{"url":"https://i.scdn.co/big","width":640},{"url":"https://i.scdn.co/mid","width":300},{"url":"https://i.scdn.co/small","width":60}]}]},
			"albums":{"items":[{"uri":"spotify:album:4aawyAB9vmqN3uQ7FjRGTy","name":"Storms","artists":[{"name":"A"},{"name":"B"}],"total_tracks":9,"images":[{"url":"https://i.scdn.co/a","width":300}]}]},
			"tracks":{"items":[{"uri":"spotify:track:6rqhFgbbKwnb9MLmUQDhG6","name":"Thunder","artists":[{"name":"C"}],"album":{"images":[]}}]}
		}`))
	}))
	return mux
}

// TestSpotifyConnect: an admin sets the Client ID; a reader connects their
// account (PKCE), the refresh token is kept encrypted, and the server plays,
// searches and lists devices with it.
func TestSpotifyConnect(t *testing.T) {
	f := &fakeSpotify{}
	fake := httptest.NewServer(f.handler())
	t.Cleanup(fake.Close)
	accounts, apiURL := soundtrack.AccountsURL, soundtrack.APIURL
	soundtrack.AccountsURL, soundtrack.APIURL = fake.URL, fake.URL+"/v1"
	t.Cleanup(func() { soundtrack.AccountsURL, soundtrack.APIURL = accounts, apiURL })

	srv, a := newServer(t, false)
	ctx := context.Background()
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	annUser, _ := a.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	boss := caller{t, login(t, srv.URL, "boss", "boss-pass-1"), srv.URL}
	ann := caller{t, login(t, srv.URL, "ann", "ann-pass-1"), srv.URL}

	var mine struct{ Available, Connected bool }
	if code := ann.do("GET", "/api/v1/me/spotify", "", &mine); code != 200 || mine.Available || mine.Connected {
		t.Fatalf("before setup: %d %+v", code, mine)
	}
	if code := ann.do("POST", "/api/v1/me/spotify/connect", `{}`, nil); code != 409 {
		t.Fatalf("connect without a Client ID: %d", code)
	}
	if code := ann.do("PUT", "/api/v1/settings/spotify", `{"clientId":"abc123def456"}`, nil); code != 403 {
		t.Fatalf("a reader setting the Client ID: %d", code)
	}
	if code := boss.do("PUT", "/api/v1/settings/spotify", `{"clientId":"not/an id"}`, nil); code != 400 {
		t.Fatalf("a bad Client ID: %d", code)
	}
	var set struct {
		ClientID, RedirectURI string
		Secure                bool
		MaxUsers              int
	}
	if code := boss.do("PUT", "/api/v1/settings/spotify", `{"clientId":"abc123def456"}`, &set); code != 200 || set.ClientID != "abc123def456" || set.MaxUsers != 5 ||
		set.RedirectURI != srv.URL+"/api/v1/me/spotify/callback" || !set.Secure {
		t.Fatalf("set the Client ID: %d %+v", code, set)
	}

	var start struct{ URL string }
	if code := ann.do("POST", "/api/v1/me/spotify/connect", `{"returnTo":"/read/7"}`, &start); code != 200 {
		t.Fatalf("connect: %d", code)
	}
	u, _ := url.Parse(start.URL)
	q := u.Query()
	if u.Path != "/authorize" || q.Get("client_id") != "abc123def456" || q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != set.RedirectURI ||
		!strings.Contains(q.Get("scope"), "streaming") || q.Get("state") == "" {
		t.Fatalf("authorize link %s", start.URL)
	}
	f.challenge = q.Get("code_challenge")

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	back := func(query string) *url.URL {
		t.Helper()
		resp, err := noFollow.Get(srv.URL + "/api/v1/me/spotify/callback?" + query)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("callback %s: %d", query, resp.StatusCode)
		}
		loc, _ := url.Parse(resp.Header.Get("Location"))
		return loc
	}
	if loc := back("state=unknown&code=good-code"); loc.Query().Get("spotify_error") == "" {
		t.Fatalf("an unknown state went back to %s", loc)
	}
	if loc := back("state=" + url.QueryEscape(q.Get("state")) + "&code=good-code"); loc.Path != "/read/7" || loc.Query().Get("spotify") != "connected" {
		t.Fatalf("connected, back to %s", loc)
	}
	if loc := back("state=" + url.QueryEscape(q.Get("state")) + "&code=good-code"); loc.Query().Get("spotify_error") == "" {
		t.Fatalf("a state used twice went back to %s", loc)
	}

	var acc model.SpotifyAccount
	if err := a.DB.NewSelect().Model(&acc).Where("user_id = ?", annUser.ID).Scan(ctx); err != nil {
		t.Fatal(err)
	}
	if acc.SpotifyUserID != "listener" || acc.Product != "premium" || acc.RefreshTokenEnc == "" || strings.Contains(acc.RefreshTokenEnc, "refresh-secret") {
		t.Fatalf("stored account %+v", acc)
	}
	if code := ann.do("GET", "/api/v1/me/spotify", "", &mine); code != 200 || !mine.Available || !mine.Connected {
		t.Fatalf("after connecting: %d %+v", code, mine)
	}
	var users struct {
		Users []struct {
			Username string
			Premium  bool
		}
	}
	if boss.do("GET", "/api/v1/settings/spotify", "", &users); len(users.Users) != 1 || users.Users[0].Username != "ann" || !users.Users[0].Premium {
		t.Fatalf("connected users %+v", users)
	}

	// the first token runs out within a minute: the server refreshes it
	var tok struct {
		AccessToken string
		ExpiresAt   time.Time
	}
	// readers' pages ask for it from several places at once: one refresh
	// serves them all (Spotify rotates refresh tokens, so a second refresh
	// with the same one could sign the reader out)
	codes := make([]int, 4)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Go(func() { codes[i] = ann.do("GET", "/api/v1/me/spotify/token", "", nil) })
	}
	wg.Wait()
	if code := ann.do("GET", "/api/v1/me/spotify/token", "", &tok); code != 200 || tok.AccessToken != "access-2" || f.refreshes != 1 {
		t.Fatalf("token: %d %+v (%d refreshes, parallel %v)", code, tok, f.refreshes, codes)
	}
	ann.do("GET", "/api/v1/me/spotify/token", "", &tok)
	if f.refreshes != 1 {
		t.Fatalf("a fresh token was refreshed again: %d", f.refreshes)
	}
	if code := boss.do("GET", "/api/v1/me/spotify/token", "", nil); code != 409 {
		t.Fatalf("token without an account: %d", code)
	}

	var devices []struct{ ID, Name string }
	if code := ann.do("GET", "/api/v1/me/spotify/devices", "", &devices); code != 200 || len(devices) != 1 || devices[0].ID != "phone-1" {
		t.Fatalf("devices: %d %+v", code, devices)
	}
	if code := ann.do("PUT", "/api/v1/me/spotify/play", `{"deviceId":"phone-1","uri":"spotify:playlist:37i9dQZF1DX4sWSpwq3LiO","shuffle":true,"volume":55}`, nil); code != 204 && code != 200 {
		t.Fatalf("play: %d", code)
	}
	if code := ann.do("PUT", "/api/v1/me/spotify/play", `{"uri":"spotify:track:6rqhFgbbKwnb9MLmUQDhG6"}`, nil); code != 204 && code != 200 {
		t.Fatalf("play a track: %d", code)
	}
	if code := ann.do("PUT", "/api/v1/me/spotify/play", `{"uri":"https://example.com"}`, nil); code != 400 {
		t.Fatalf("play a link: %d", code)
	}
	if len(f.plays) != 2 || f.plays[0] != `phone-1 {"context_uri":"spotify:playlist:37i9dQZF1DX4sWSpwq3LiO"}` || f.plays[1] != ` {"uris":["spotify:track:6rqhFgbbKwnb9MLmUQDhG6"]}` {
		t.Fatalf("plays %q", f.plays)
	}
	if len(f.shuffles) != 1 || f.shuffles[0] != "true" || len(f.volumes) != 1 || f.volumes[0] != "55" { // refused, and played anyway
		t.Fatalf("shuffles %q volumes %q", f.shuffles, f.volumes)
	}
	// this browser's new player takes the playback over, then plays; a
	// device that went away says so
	if code := ann.do("PUT", "/api/v1/me/spotify/play", `{"deviceId":"browser-1","uri":"spotify:track:6rqhFgbbKwnb9MLmUQDhG6"}`, nil); code != 204 && code != 200 {
		t.Fatalf("play in a new browser player: %d", code)
	}
	if len(f.transfers) != 1 || !strings.Contains(f.transfers[0], `"device_ids":["browser-1"]`) || !strings.Contains(f.transfers[0], `"play":false`) || len(f.plays) != 3 {
		t.Fatalf("transfers %q plays %q", f.transfers, f.plays)
	}
	if code := ann.do("PUT", "/api/v1/me/spotify/play", `{"deviceId":"gone-1","uri":"spotify:track:6rqhFgbbKwnb9MLmUQDhG6"}`, nil); code != 410 {
		t.Fatalf("play on a device that went away: %d", code)
	}
	// a device that won't take a volume still plays (the volume was only a wish)
	if code := ann.do("PUT", "/api/v1/me/spotify/volume", `{"volume":30,"deviceId":"phone-1"}`, nil); code != 409 {
		t.Fatalf("volume on a phone: %d", code)
	}
	if code := ann.do("POST", "/api/v1/me/spotify/next", `{}`, nil); code != 204 || f.skips != 1 {
		t.Fatalf("next: %d (%d skips)", code, f.skips)
	}
	if code := ann.do("PUT", "/api/v1/me/spotify/pause", `{}`, nil); code != 403 {
		t.Fatalf("pause without Premium: %d", code)
	}

	var found []struct{ URI, Type, Name, Subtitle, ImageURL string }
	if code := ann.do("GET", "/api/v1/spotify/search?q=rain&type=playlist,album,track", "", &found); code != 200 || len(found) != 3 {
		t.Fatalf("search: %d %+v", code, found)
	}
	if found[0].Type != "playlist" || found[0].ImageURL != "https://i.scdn.co/mid" || found[1].Subtitle != "A, B" || found[2].Type != "track" {
		t.Fatalf("search results %+v", found)
	}
	if s := f.searches[0]; s.Get("limit") != "10" || s.Get("q") != "rain" {
		t.Fatalf("search asked %v", s)
	}
	if code := ann.do("GET", "/api/v1/spotify/search?q=rain&type=artist", "", nil); code != 400 {
		t.Fatalf("search artists: %d", code)
	}

	if code := ann.do("DELETE", "/api/v1/me/spotify", "", nil); code != 204 {
		t.Fatalf("disconnect: %d", code)
	}
	if ann.do("GET", "/api/v1/me/spotify", "", &mine); mine.Connected {
		t.Fatal("still connected after disconnecting")
	}
}

// TestSoundtracks: built-in moods are seeded once; admins edit moods and a
// series' shared soundtrack; readers keep their own version, mute it and
// reset it; the reader endpoint resolves what plays in a chapter.
func TestSoundtracks(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	_, _ = a.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	_, _ = a.Auth.CreateUser(ctx, auth.NewUser{Username: "bob", Password: "bob-pass-1"})
	boss := caller{t, login(t, srv.URL, "boss", "boss-pass-1"), srv.URL}
	ann := caller{t, login(t, srv.URL, "ann", "ann-pass-1"), srv.URL}
	bob := caller{t, login(t, srv.URL, "bob", "bob-pass-1"), srv.URL}

	type mood struct {
		ID         int64
		Slug, Name string
		Color      string
		Icon       string
		SpotifyURI string
	}
	var moods []mood
	if code := ann.do("GET", "/api/v1/audio/moods", "", &moods); code != 200 || len(moods) != 8 || moods[0].Slug != "calm" || moods[0].Color != "#3fb98a" || moods[0].Icon != "leaf" ||
		moods[7].Slug != "mystery" || moods[7].SpotifyURI != "" {
		t.Fatalf("built-in moods: %d %+v", code, moods)
	}
	calm, tense := moods[0], moods[2]
	if code := ann.do("POST", "/api/v1/audio/moods", `{"name":"Sea","color":"#123456","icon":"waves"}`, nil); code != 403 {
		t.Fatalf("a reader adding a mood: %d", code)
	}
	var sea mood
	if code := boss.do("POST", "/api/v1/audio/moods", `{"name":"Sea","color":"#12AB56","icon":"waves","spotifyUri":"spotify:playlist:37i9dQZF1DX4sWSpwq3LiO","spotifyName":"Waves"}`, &sea); code != 200 || sea.ID == 0 || sea.Color != "#12ab56" {
		t.Fatalf("add a mood: %d %+v", code, sea)
	}
	for _, bad := range []string{
		`{"name":"X","color":"#12AB56","icon":"no-such-icon"}`,
		`{"name":"X","color":"#12AB56","icon":"waves","spotifyUri":"https://open.spotify.com/playlist/1"}`,
	} {
		if code := boss.do("POST", "/api/v1/audio/moods", bad, nil); code != 400 {
			t.Fatalf("bad mood %s: %d", bad, code)
		}
	}
	calmID := strconv.FormatInt(calm.ID, 10)
	var edited mood
	if code := boss.do("PUT", "/api/v1/audio/moods/"+calmID, `{"name":"Calm","color":"#3fb98a","icon":"leaf","spotifyUri":"spotify:album:4aawyAB9vmqN3uQ7FjRGTy","shuffle":true}`, &edited); code != 200 ||
		edited.Slug != "calm" || edited.SpotifyURI != "spotify:album:4aawyAB9vmqN3uQ7FjRGTy" {
		t.Fatalf("edit a built-in mood: %d %+v", code, edited)
	}
	tenseID := strconv.FormatInt(tense.ID, 10)
	if code := boss.do("DELETE", "/api/v1/audio/moods/"+tenseID, "", nil); code != 204 {
		t.Fatalf("delete a mood: %d", code)
	}
	if ann.do("GET", "/api/v1/audio/moods", "", &moods); len(moods) != 8 || moods[7].Name != "Sea" {
		t.Fatalf("moods after edits (a deleted built-in stays deleted): %+v", moods)
	}

	now := time.Now().UTC()
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := a.DB.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	ser := &model.Series{Title: "Harbor", SortTitle: "harbor", Status: model.StatusOngoing, RootFolderID: rf.ID, Path: "Harbor", ProfileID: 1, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	if _, err := a.DB.NewInsert().Model(ser).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	chapters := map[int]int64{}
	for _, n := range []int{1, 5, 12} {
		ch := &model.Chapter{SeriesID: ser.ID, NumberKey: strconv.Itoa(n), NumberSort: float64(n), Monitored: true, State: model.ChapterMissing, FirstSeenAt: now, UpdatedAt: now}
		if _, err := a.DB.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		chapters[n] = ch.ID
	}
	sid := strconv.FormatInt(ser.ID, 10)
	seaID := strconv.FormatInt(sea.ID, 10)

	type doc struct {
		Cues []struct {
			ChapterNumber float64
			Page          *int
			MoodID        *int64
		}
		Muted bool
	}
	var st struct{ Shared, Personal *doc }
	if code := ann.do("GET", "/api/v1/series/"+sid+"/soundtrack", "", &st); code != 200 || st.Shared != nil || st.Personal != nil {
		t.Fatalf("no soundtrack yet: %d %+v", code, st)
	}
	type sound struct {
		Source string
		Mood   *mood
		Music  *struct{ URI string }
	}
	type resolved struct {
		Source string
		Muted  bool
		Start  *sound
		Pages  []struct {
			Page  int
			Sound *sound
		}
	}
	moodOf := func(r resolved) *mood {
		if r.Start == nil {
			return nil
		}
		return r.Start.Mood
	}
	read := func(c caller, n int) resolved {
		t.Helper()
		var r resolved
		if code := c.do("GET", "/api/v1/read/chapters/"+strconv.FormatInt(chapters[n], 10)+"/soundtrack", "", &r); code != 200 {
			t.Fatalf("resolve chapter %d: %d", n, code)
		}
		return r
	}
	if r := read(ann, 1); r.Source != "none" || moodOf(r) != nil {
		t.Fatalf("nothing set: %+v", r)
	}

	shared := `{"cues":[{"chapterNumber":0,"moodId":` + calmID + `},{"chapterNumber":5,"moodId":` + seaID + `},{"chapterNumber":12,"page":8,"moodId":` + calmID + `}]}`
	if code := ann.do("PUT", "/api/v1/series/"+sid+"/soundtrack", shared, nil); code != 403 {
		t.Fatalf("a reader setting the shared soundtrack: %d", code)
	}
	if code := boss.do("PUT", "/api/v1/series/"+sid+"/soundtrack", `{"cues":[{"chapterNumber":0,"moodId":999999}]}`, nil); code != 404 {
		t.Fatalf("an unknown mood: %d", code)
	}
	if code := boss.do("PUT", "/api/v1/series/"+sid+"/soundtrack", `{"cues":[{"chapterNumber":0},{"chapterNumber":0}]}`, nil); code != 400 {
		t.Fatalf("two cues in one place: %d", code)
	}
	if code := boss.do("PUT", "/api/v1/series/"+sid+"/soundtrack", shared, &st); code != 200 || st.Shared == nil || len(st.Shared.Cues) != 3 {
		t.Fatalf("set the shared soundtrack: %d %+v", code, st)
	}
	if r := read(ann, 1); r.Source != "shared" || moodOf(r) == nil || moodOf(r).ID != calm.ID {
		t.Fatalf("chapter 1 plays the default: %+v", r)
	}
	if r := read(ann, 5); moodOf(r) == nil || moodOf(r).ID != sea.ID {
		t.Fatalf("chapter 5 starts its own cue: %+v", r)
	}
	if r := read(ann, 12); moodOf(r) == nil || moodOf(r).ID != sea.ID || len(r.Pages) != 1 || r.Pages[0].Page != 8 || r.Pages[0].Sound.Mood.ID != calm.ID {
		t.Fatalf("chapter 12 carries on and changes on page 8: %+v", r)
	}

	// ann's own version: Sea for the whole title, muted later
	mine := `{"cues":[{"chapterNumber":0,"moodId":` + seaID + `}]}`
	if code := ann.do("PUT", "/api/v1/me/series/"+sid+"/soundtrack", mine, &st); code != 200 || st.Personal == nil || st.Shared == nil {
		t.Fatalf("ann's version: %d %+v", code, st)
	}
	if r := read(ann, 1); r.Source != "personal" || moodOf(r) == nil || moodOf(r).ID != sea.ID {
		t.Fatalf("ann hears her version: %+v", r)
	}
	if r := read(bob, 1); r.Source != "shared" || moodOf(r).ID != calm.ID {
		t.Fatalf("bob still hears the shared one: %+v", r)
	}
	ann.do("PUT", "/api/v1/me/series/"+sid+"/soundtrack", `{"cues":[{"chapterNumber":0,"moodId":`+seaID+`}],"muted":true}`, nil)
	if r := read(ann, 1); !r.Muted {
		t.Fatalf("muted: %+v", r)
	}
	st.Shared, st.Personal = nil, nil
	if code := ann.do("DELETE", "/api/v1/me/series/"+sid+"/soundtrack", "", &st); code != 200 || st.Personal != nil || st.Shared == nil {
		t.Fatalf("reset: %d %+v", code, st)
	}
	if r := read(ann, 1); r.Source != "shared" || r.Muted {
		t.Fatalf("after reset: %+v", r)
	}

	// deleting a mood leaves its cues silent rather than failing
	if code := boss.do("DELETE", "/api/v1/audio/moods/"+seaID, "", nil); code != 204 {
		t.Fatalf("delete sea: %d", code)
	}
	if r := read(ann, 5); r.Source != "shared" || moodOf(r) != nil {
		t.Fatalf("a deleted mood's chapter: %+v", r)
	}
}
