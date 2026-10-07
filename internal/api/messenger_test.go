package api_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Asion001/mangarr/internal/messenger"
	"github.com/Asion001/mangarr/internal/settings"
)

func call(t *testing.T, method, url, body string) (int, map[string]any) {
	t.Helper()
	out := map[string]any{}
	code := doJSON(t, method, url, body, &out)
	return code, out
}

// TestMessengerSettings: bot secrets are never sent back, a masked value
// keeps the stored one, and Test checks the credentials on the page.
func TestMessengerSettings(t *testing.T) {
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botgood-token/getMe" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":401,"description":"Unauthorized"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":42,"is_bot":true,"first_name":"Shelf","username":"shelf_bot"}}`)
	}))
	defer tg.Close()
	dc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bot dc-token" || r.URL.Path != "/users/@me" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"message":"401: Unauthorized","code":0}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":"77","username":"shelf"}`)
	}))
	defer dc.Close()
	old := messenger.DiscordAPI
	messenger.DiscordAPI = dc.URL
	defer func() { messenger.DiscordAPI = old }()

	srv, a := newServer(t, true)
	base := srv.URL + "/api/v1/settings/messenger"
	g, _ := a.Settings.General(context.Background())
	g.PublicURL = "https://manga.example.com"
	_ = a.Settings.Set(context.Background(), settings.KeyGeneral, g)

	code, _ := call(t, http.MethodPut, base, `{"telegram":{"enabled":true,"botToken":"","apiUrl":"`+tg.URL+`","announceChat":"","announceEvents":[]},"discord":{"enabled":false,"clientId":"","clientSecret":"","botToken":"","announceChannel":"","announceEvents":[]},"allowInstant":true,"digestHour":9}`)
	if code != http.StatusBadRequest {
		t.Fatalf("enabled without a token: %d", code)
	}
	body := `{"telegram":{"enabled":true,"botToken":"good-token","apiUrl":"` + tg.URL + `/","announceChat":"","announceEvents":[]},"discord":{"enabled":false,"clientId":"1","clientSecret":"","botToken":"dc-token","announceChannel":"","announceEvents":[]},"allowInstant":false,"digestHour":7,"discordRedirectUrl":""}`
	code, got := call(t, http.MethodPut, base, body)
	if code != 200 {
		t.Fatalf("save: %d %v", code, got)
	}
	tgOut := got["telegram"].(map[string]any)
	if tgOut["botToken"] != "••••••••" || tgOut["apiUrl"] != tg.URL || got["digestHour"] != float64(7) {
		t.Fatalf("saved %v", got)
	}
	if got["discordRedirectUrl"] != "https://manga.example.com/api/v1/me/messenger/discord/callback" {
		t.Fatalf("redirect %v", got["discordRedirectUrl"])
	}
	// the mask keeps the stored token
	code, _ = call(t, http.MethodPut, base, strings.Replace(body, `"good-token"`, `"••••••••"`, 1))
	if m, _ := a.Settings.Messenger(context.Background()); code != 200 || m.Telegram.BotToken != "good-token" || m.Discord.BotToken != "dc-token" {
		t.Fatalf("mask kept %d %+v", code, m)
	}

	code, got = call(t, http.MethodPost, base+"/test", `{"bot":"telegram","settings":`+strings.Replace(body, `"good-token"`, `"••••••••"`, 1)+`}`)
	if code != 200 || got["username"] != "shelf_bot" || got["id"] != "42" {
		t.Fatalf("telegram test %d %v", code, got)
	}
	code, got = call(t, http.MethodPost, base+"/test", `{"bot":"telegram","settings":`+strings.Replace(body, `"good-token"`, `"bad"`, 1)+`}`)
	if code != 400 || !strings.Contains(got["detail"].(string), "Unauthorized") || strings.Contains(got["detail"].(string), "bad") {
		t.Fatalf("telegram bad token %d %v", code, got)
	}
	code, got = call(t, http.MethodPost, base+"/test", `{"bot":"discord","settings":`+body+`}`)
	if code != 200 || got["username"] != "shelf" {
		t.Fatalf("discord test %d %v", code, got)
	}
	// secrets reach the redactor (the Telegram token is part of every URL)
	if r := a.Redactor(context.Background()).String("GET /botgood-token/getMe dc-token"); strings.Contains(r, "good-token") || strings.Contains(r, "dc-token") {
		t.Fatalf("not redacted: %s", r)
	}
}
