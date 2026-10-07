package messenger_test

import (
	"context"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/messenger"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func TestDelivery(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		fake := &fakeTelegram{blocked: map[string]bool{}}
		api := httptest.NewServer(fake)
		defer api.Close()
		st := settings.NewStore(d)
		m := settings.DefaultMessenger()
		m.Telegram.Enabled, m.Telegram.BotToken, m.Telegram.APIURL = true, "token", api.URL
		m.DigestHour = 9
		_ = st.Set(ctx, settings.KeyMessenger, m)
		now := time.Now().UTC()
		users := []model.User{{Username: "instant", CreatedAt: now}, {Username: "digest", CreatedAt: now}, {Username: "off", CreatedAt: now}, {Username: "unlinked", CreatedAt: now}}
		if _, err := d.NewInsert().Model(&users).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		at := "'2024-01-02T03:04:05Z'"
		for _, q := range []string{
			`INSERT INTO root_folders (id, path, language, created_at) VALUES (1, '/library', 'en', ` + at + `)`,
			`INSERT INTO profiles (id, name, created_at, updated_at) VALUES (1, 'Default', ` + at + `, ` + at + `)`,
			`INSERT INTO series (id, title, sort_title, root_folder_id, path, profile_id, added_at, updated_at) VALUES (7, 'Night Garden', 'night garden', 1, 'a', 1, ` + at + `, ` + at + `), (8, 'Ash Tower', 'ash tower', 1, 'b', 1, ` + at + `, ` + at + `), (9, 'Late Bloom', 'late bloom', 1, 'c', 1, ` + at + `, ` + at + `)`,
		} {
			if _, err := d.ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		svc := &messenger.Service{DB: d, Settings: st, Bus: events.NewBus(), Log: slog.New(slog.DiscardHandler)}
		for i, mode := range []string{model.DeliveryInstant, model.DeliveryDigest, model.DeliveryOff} {
			token, _, _ := svc.NewLinkToken(ctx, users[i].ID, model.MessengerTelegram)
			if _, err := svc.Redeem(ctx, model.MessengerTelegram, token, messenger.Identity{ID: "chat-" + users[i].Username, Username: users[i].Username}); err != nil {
				t.Fatal(err)
			}
			if err := svc.SetPreferences(ctx, users[i].ID, mode, events.PersonalEvents); err != nil {
				t.Fatal(err)
			}
		}
		all := []int64{users[0].ID, users[1].ID, users[2].ID, users[3].ID}
		chapters := messenger.Message{Title: "Night Garden: 2 new chapters", Body: "Chapters 11–12", Items: []string{"Ch. 11 <Bloom>", "Ch. 12"}, URL: "https://manga.example.com/series/7", Series: "Night Garden"}
		if err := svc.Enqueue(ctx, all, events.ChapterImported, 7, chapters); err != nil {
			t.Fatal(err)
		}
		_ = svc.Enqueue(ctx, all, events.ChapterImported, 7, chapters) // replayed: once only
		svc.SendDue(ctx)
		if got := fake.sentTo("chat-instant"); len(got) != 1 || !strings.Contains(got[0], "<b>Night Garden: 2 new chapters</b>") ||
			!strings.Contains(got[0], "• Ch. 11 &lt;Bloom&gt;") || !strings.Contains(got[0], `<a href="https://manga.example.com/series/7">`) {
			t.Fatalf("instant got %q", got)
		}
		if len(fake.sentTo("chat-digest")) != 0 || len(fake.sentTo("chat-off")) != 0 {
			t.Fatal("digest or off account got an instant message")
		}

		// request news goes out right away, also to digest accounts
		_ = svc.Enqueue(ctx, []int64{users[1].ID}, events.RequestUpdated, 0, messenger.Message{Title: "Request approved", Body: "Night Garden was added."})
		svc.SendDue(ctx)
		if got := fake.sentTo("chat-digest"); len(got) != 1 || !strings.Contains(got[0], "Request approved") {
			t.Fatalf("digest account request news %q", got)
		}

		// the digest: everything since the link, once, after the digest hour
		_ = svc.Enqueue(ctx, all, events.ChapterImported, 8, messenger.Message{Title: "Ash Tower: 1 new chapter", Body: "Chapter 3", Series: "Ash Tower"})
		svc.SendDue(ctx)
		next := time.Now().Add(24 * time.Hour)
		svc.SendDigests(ctx, time.Date(next.Year(), next.Month(), next.Day(), 9, 30, 0, 0, time.Local))
		got := fake.sentTo("chat-digest")
		if len(got) != 2 || !strings.Contains(got[1], "Night Garden") || !strings.Contains(got[1], "Chapters 11–12") || !strings.Contains(got[1], "Ash Tower") {
			t.Fatalf("digest %q", got)
		}
		svc.SendDigests(ctx, time.Date(next.Year(), next.Month(), next.Day(), 10, 0, 0, 0, time.Local))
		if n := len(fake.sentTo("chat-digest")); n != 2 {
			t.Fatalf("digest sent twice (%d)", n)
		}
		if len(fake.sentTo("chat-instant")) != 2 { // two chapter batches, no digest
			t.Fatalf("instant account: %q", fake.sentTo("chat-instant"))
		}

		// switching to the digest doesn't repeat what was already sent
		if err := svc.SetPreferences(ctx, users[0].ID, model.DeliveryDigest, events.PersonalEvents); err != nil {
			t.Fatal(err)
		}
		later := next.Add(24 * time.Hour)
		svc.SendDigests(ctx, time.Date(later.Year(), later.Month(), later.Day(), 9, 30, 0, 0, time.Local))
		if n := len(fake.sentTo("chat-instant")); n != 2 {
			t.Fatalf("old chapters repeated in a digest (%d messages)", n)
		}

		// a blocked bot: the message waits and the link is paused
		if err := svc.SetPreferences(ctx, users[0].ID, model.DeliveryInstant, events.PersonalEvents); err != nil {
			t.Fatal(err)
		}
		fake.mu.Lock()
		fake.blocked["chat-instant"] = true
		fake.mu.Unlock()
		_ = svc.Enqueue(ctx, []int64{users[0].ID}, events.ChapterImported, 9, messenger.Message{Title: "Late Bloom: 1 new chapter"})
		svc.SendDue(ctx)
		links, _ := svc.Links(ctx, users[0].ID)
		if links[0].Status != model.LinkBroken {
			t.Fatalf("link after block %+v", links[0])
		}
		var pending []model.NotificationDispatch
		_ = d.NewSelect().Model(&pending).Where("link_id = ? AND sent_at IS NULL", links[0].ID).Scan(ctx)
		if len(pending) != 1 || pending[0].Attempts != 1 || !pending[0].AvailableAt.After(time.Now()) {
			t.Fatalf("pending %+v", pending)
		}

		// pressing Try again after unblocking sends what waited
		fake.mu.Lock()
		fake.blocked["chat-instant"] = false
		fake.mu.Unlock()
		if err := svc.SendTest(ctx, users[0].ID, model.MessengerTelegram); err != nil {
			t.Fatal(err)
		}
		svc.SendDue(ctx)
		if got := fake.sentTo("chat-instant"); len(got) != 4 || !strings.Contains(got[3], "Late Bloom") {
			t.Fatalf("after unblocking %q", got)
		}
	})
}
