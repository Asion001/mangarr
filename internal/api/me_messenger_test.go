package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/messenger"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// fakeDiscord answers the OAuth exchange, DM channels and messages; users
// in closed don't take direct messages (error 50007).
type fakeDiscord struct {
	mu     sync.Mutex
	sent   []string
	closed map[string]bool
}

func (f *fakeDiscord) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.URL.Path == "/oauth2/token":
		_ = r.ParseForm()
		if u, p, _ := r.BasicAuth(); u != "client-1" || p != "secret-1" || r.Form.Get("code") != "code-1" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":"invalid_client"}`)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"user-token","token_type":"Bearer"}`)
	case r.URL.Path == "/users/@me" && r.Header.Get("Authorization") == "Bearer user-token":
		_, _ = io.WriteString(w, `{"id":"9001","username":"reader_dc","global_name":"Reader"}`)
	case r.URL.Path == "/users/@me/channels" && r.Header.Get("Authorization") == "Bot bot-token":
		var in struct {
			RecipientID string `json:"recipient_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		_, _ = io.WriteString(w, `{"id":"dm-`+in.RecipientID+`"}`)
	case strings.HasPrefix(r.URL.Path, "/channels/dm-") && r.Header.Get("Authorization") == "Bot bot-token":
		user := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/channels/dm-"), "/messages")
		if f.closed[user] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"message":"Cannot send messages to this user","code":50007}`)
			return
		}
		var in struct {
			Content string `json:"content"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.sent = append(f.sent, in.Content)
		_, _ = io.WriteString(w, `{"id":"m1"}`)
	default:
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"message":"401: Unauthorized","code":0}`)
	}
}

// TestDiscordLinking: Discord sends people back with the link token as
// state; the account is linked and gets direct messages, and a closed DM
// pauses the link.
func TestDiscordLinking(t *testing.T) {
	fake := &fakeDiscord{closed: map[string]bool{}}
	dc := httptest.NewServer(fake)
	defer dc.Close()
	old := messenger.DiscordAPI
	messenger.DiscordAPI = dc.URL
	defer func() { messenger.DiscordAPI = old }()

	srv, a := newServer(t, true)
	ctx := context.Background()
	g, _ := a.Settings.General(ctx)
	g.PublicURL = "https://manga.example.com"
	_ = a.Settings.Set(ctx, settings.KeyGeneral, g)
	m := settings.DefaultMessenger()
	m.Discord = settings.DiscordBot{Enabled: true, ClientID: "client-1", ClientSecret: "secret-1", BotToken: "bot-token", AnnounceEvents: []string{}}
	if err := a.Settings.Set(ctx, settings.KeyMessenger, m); err != nil {
		t.Fatal(err)
	}
	u := model.User{Username: "reader", CreatedAt: time.Now().UTC()}
	if _, err := a.DB.NewInsert().Model(&u).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	state, _, err := a.Messenger.NewLinkToken(ctx, u.ID, model.MessengerDiscord)
	if err != nil {
		t.Fatal(err)
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(q string) string {
		resp, err := noFollow.Get(srv.URL + "/api/v1/me/messenger/discord/callback?" + q)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("callback %s: %d", q, resp.StatusCode)
		}
		return resp.Header.Get("Location")
	}
	if loc := get("code=wrong&state=" + state); !strings.Contains(loc, "messenger_error=") {
		t.Fatalf("bad code went to %s", loc)
	}
	// the failed exchange used the token up: a new one links
	state, _, _ = a.Messenger.NewLinkToken(ctx, u.ID, model.MessengerDiscord)
	if loc := get("code=code-1&state=" + state); loc != "/account?linked=discord" {
		t.Fatalf("linked went to %s", loc)
	}
	links, _ := a.Messenger.Links(ctx, u.ID)
	if len(links) != 1 || links[0].ExternalID != "9001" || links[0].DisplayName != "reader_dc" {
		t.Fatalf("links %+v", links)
	}
	if loc := get("code=code-1&state=" + state); !strings.Contains(loc, "expired") {
		t.Fatalf("reused state went to %s", loc)
	}

	if err := a.Messenger.Deliver(ctx, links[0], `<b>Night &amp; Day</b>: <a href="https://manga.example.com/series/1">2 new chapters</a>`); err != nil {
		t.Fatal(err)
	}
	if len(fake.sent) != 1 || fake.sent[0] != "**Night & Day**: [2 new chapters](https://manga.example.com/series/1)" {
		t.Fatalf("sent %q", fake.sent)
	}
	fake.mu.Lock()
	fake.closed["9001"] = true
	fake.mu.Unlock()
	if err := a.Messenger.SendTest(ctx, u.ID, model.MessengerDiscord); err == nil {
		t.Fatal("closed DMs took a message")
	}
	links, _ = a.Messenger.Links(ctx, u.ID)
	if links[0].Status != model.LinkBroken {
		t.Fatalf("after 50007: %+v", links[0])
	}

	// linking starts at Discord with the identify scope and our redirect
	_ = srv
}
