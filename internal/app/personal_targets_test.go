package app_test

import (
	"testing"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// TestRetirePersonalTargets: people's own targets are turned off; one that
// was a chat with the server's Telegram bot becomes a linked account, once.
func TestRetirePersonalTargets(t *testing.T) {
	e := newTestApp(t, dbtest.DSNs(t)["sqlite"])
	ctx := e.Ctx
	ann, _ := e.App.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"})
	defs := []model.ProviderDefinition{
		{Kind: "notify", Implementation: "telegram", Name: "My Telegram", Enabled: true, UserID: &ann.ID, Settings: map[string]any{"botToken": "server-bot", "chatId": "4242"}, Events: []string{}, Tags: []int64{}},
		{Kind: "notify", Implementation: "webhook", Name: "My hook", Enabled: true, UserID: &ann.ID, Settings: map[string]any{"url": "https://hooks.example.com/x"}, Events: []string{}, Tags: []int64{}},
	}
	if _, err := e.App.DB.NewInsert().Model(&defs).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	// no bot yet: turned off, nothing linked
	if err := e.App.RetirePersonalTargets(ctx); err != nil {
		t.Fatal(err)
	}
	var on int
	on, _ = e.App.DB.NewSelect().Model((*model.ProviderDefinition)(nil)).Where("user_id IS NOT NULL AND enabled = ?", true).Count(ctx)
	if links, _ := e.App.Messenger.Links(ctx, ann.ID); on != 0 || len(links) != 0 {
		t.Fatalf("after retiring: %d on, links %+v", on, links)
	}
	// the admin sets up the same bot: the chat becomes a link
	m := settings.DefaultMessenger()
	m.Telegram.Enabled, m.Telegram.BotToken = true, "server-bot"
	_ = e.App.Settings.Set(ctx, settings.KeyMessenger, m)
	if err := e.App.RetirePersonalTargets(ctx); err != nil {
		t.Fatal(err)
	}
	links, _ := e.App.Messenger.Links(ctx, ann.ID)
	if len(links) != 1 || links[0].ExternalID != "4242" {
		t.Fatalf("links %+v", links)
	}
	list, _ := e.App.PersonalTargets(ctx)
	if len(list) != 2 || !list[0].Linked || list[1].Linked || list[0].Username != "ann" {
		t.Fatalf("list %+v", list)
	}
	// unlinking sticks
	_ = e.App.Messenger.Unlink(ctx, ann.ID, model.MessengerTelegram)
	_ = e.App.RetirePersonalTargets(ctx)
	if links, _ := e.App.Messenger.Links(ctx, ann.ID); len(links) != 0 {
		t.Fatalf("re-linked after unlinking: %+v", links)
	}
	if n, err := e.App.RemovePersonalTargets(ctx); err != nil || n != 2 {
		t.Fatalf("removed %d %v", n, err)
	}
}
