package messenger_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/messenger"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// fakeTelegram is a Bot API server: queued updates go out on getUpdates,
// sent messages are recorded, and chats in blocked answer 403.
type fakeTelegram struct {
	mu      sync.Mutex
	updates []map[string]any
	nextID  int64
	sent    []map[string]string
	blocked map[string]bool
}

func (f *fakeTelegram) message(chat int64, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	f.updates = append(f.updates, map[string]any{"update_id": f.nextID, "message": map[string]any{
		"text": text, "chat": map[string]any{"id": chat, "type": "private"},
		"from": map[string]any{"id": chat, "username": "reader_one", "first_name": "Reader"}}})
}

func (f *fakeTelegram) sentTo(chat string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, m := range f.sent {
		if m["chat_id"] == chat {
			out = append(out, m["text"])
		}
	}
	return out
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case strings.HasSuffix(r.URL.Path, "/getMe"):
		_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"shelf_bot"}}`)
	case strings.HasSuffix(r.URL.Path, "/getUpdates"):
		var off int64
		_ = json.Unmarshal([]byte(r.Form.Get("offset")), &off)
		var out []map[string]any
		for _, u := range f.updates {
			if u["update_id"].(int64) >= off {
				out = append(out, u)
			}
		}
		if out == nil {
			f.mu.Unlock()
			time.Sleep(50 * time.Millisecond) // a short long-poll
			f.mu.Lock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": out})
	case strings.HasSuffix(r.URL.Path, "/sendMessage"):
		if f.blocked[r.Form.Get("chat_id")] {
			w.WriteHeader(http.StatusForbidden)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`)
			return
		}
		f.sent = append(f.sent, map[string]string{"chat_id": r.Form.Get("chat_id"), "text": r.Form.Get("text")})
		_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTelegramLinking(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		fake := &fakeTelegram{blocked: map[string]bool{}}
		api := httptest.NewServer(fake)
		defer api.Close()

		st := settings.NewStore(d)
		m := settings.DefaultMessenger()
		m.Telegram.Enabled, m.Telegram.BotToken, m.Telegram.APIURL = true, "token", api.URL
		m.AllowInstant = false
		if err := st.Set(ctx, settings.KeyMessenger, m); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		users := []model.User{{Username: "reader-a", CreatedAt: now}, {Username: "reader-b", CreatedAt: now}}
		if _, err := d.NewInsert().Model(&users).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		svc := &messenger.Service{DB: d, Settings: st, Bus: events.NewBus(), Log: slog.New(slog.DiscardHandler),
			Username: func(context.Context, int64) string { return "Reader <A>" }}
		if err := svc.Start(ctx); err != nil {
			t.Fatal(err)
		}

		// /start without a token explains where to link
		fake.message(555, "/start")
		waitUntil(t, "hint", func() bool { return len(fake.sentTo("555")) == 1 })

		token, _, err := svc.NewLinkToken(ctx, users[0].ID, model.MessengerTelegram)
		if err != nil || len(token) > 64 || strings.ContainsAny(token, "+/=") {
			t.Fatalf("token %q %v", token, err)
		}
		fake.message(555, "/start "+token)
		waitUntil(t, "link", func() bool { l, _ := svc.Links(ctx, users[0].ID); return len(l) == 1 })
		links, _ := svc.Links(ctx, users[0].ID)
		if l := links[0]; l.ExternalID != "555" || l.DisplayName != "@reader_one" || l.Mode != model.DeliveryDigest || len(l.Events) != len(events.PersonalEvents) {
			t.Fatalf("link %+v", l)
		}
		waitUntil(t, "linked reply", func() bool { return len(fake.sentTo("555")) == 2 })
		if got := fake.sentTo("555")[1]; !strings.Contains(got, "Reader &lt;A&gt;") {
			t.Fatalf("reply %q", got)
		}

		// a token works once
		fake.message(555, "/start "+token)
		waitUntil(t, "expired reply", func() bool { return len(fake.sentTo("555")) == 3 })
		if !strings.Contains(fake.sentTo("555")[2], "expired") {
			t.Fatalf("reused token: %q", fake.sentTo("555")[2])
		}

		// the same chat linked by someone else moves to them
		tokenB, _, _ := svc.NewLinkToken(ctx, users[1].ID, model.MessengerTelegram)
		fake.message(555, "/start "+tokenB)
		waitUntil(t, "relink", func() bool { l, _ := svc.Links(ctx, users[1].ID); return len(l) == 1 })
		if l, _ := svc.Links(ctx, users[0].ID); len(l) != 0 {
			t.Fatalf("old owner still linked: %+v", l)
		}

		// instant is off on this server
		if err := svc.SetPreferences(ctx, users[1].ID, model.DeliveryInstant, nil); err == nil {
			t.Fatal("instant allowed while switched off")
		}
		if err := svc.SetPreferences(ctx, users[1].ID, model.DeliveryBoth, nil); err == nil {
			t.Fatal("instant+digest allowed while switched off")
		}
		if err := svc.SetPreferences(ctx, users[1].ID, model.DeliveryDigest, []string{events.RequestUpdated, "health.issue"}); err != nil {
			t.Fatal(err)
		}
		links, _ = svc.Links(ctx, users[1].ID)
		if len(links[0].Events) != 1 || links[0].Events[0] != events.RequestUpdated {
			t.Fatalf("events %v", links[0].Events)
		}

		// a test message arrives; a blocked bot marks the link broken
		if err := svc.SendTest(ctx, users[1].ID, model.MessengerTelegram); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		fake.blocked["555"] = true
		fake.mu.Unlock()
		if err := svc.SendTest(ctx, users[1].ID, model.MessengerTelegram); err == nil {
			t.Fatal("blocked chat accepted a message")
		}
		links, _ = svc.Links(ctx, users[1].ID)
		if links[0].Status != model.LinkBroken || !strings.Contains(links[0].LastError, "blocked") {
			t.Fatalf("after block %+v", links[0])
		}

		// /stop unlinks
		fake.message(555, "/stop")
		waitUntil(t, "unlink", func() bool { l, _ := svc.Links(ctx, users[1].ID); return len(l) == 0 })
	})
}
