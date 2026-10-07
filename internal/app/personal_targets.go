package app

import (
	"context"
	"fmt"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
)

// adoptedKey marks an old Telegram target that was turned into a link.
const adoptedKey = "linkedToBot"

// PersonalTarget is a notification target a user set up for themselves
// before messenger bots replaced them. They are turned off; the admin
// removes them.
type PersonalTarget struct {
	ID             int64  `json:"id"`
	Username       string `json:"username"`
	Name           string `json:"name"`
	Implementation string `json:"implementation"`
	// Linked is true when it was a chat with the server's Telegram bot and
	// became a linked account.
	Linked bool `json:"linked"`
}

// RetirePersonalTargets turns off the notification targets users made for
// themselves. A Telegram target that used the server's own bot becomes a
// linked account for its chat, also when the bot is set up later. Safe to
// run again.
func (a *App) RetirePersonalTargets(ctx context.Context) error {
	var defs []model.ProviderDefinition
	if err := a.DB.NewSelect().Model(&defs).Where("kind = ? AND user_id IS NOT NULL", string(modules.KindNotify)).Scan(ctx); err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}
	m, err := a.Settings.Messenger(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	off := 0
	for _, d := range defs {
		token, _ := d.Settings["botToken"].(string)
		chat := fmt.Sprint(d.Settings["chatId"])
		// once only: someone who unlinks later stays unlinked
		if d.Implementation == "telegram" && m.Telegram.BotToken != "" && token == m.Telegram.BotToken && chat != "" && chat != "<nil>" && d.Settings[adoptedKey] != true {
			if err := a.Messenger.Adopt(ctx, *d.UserID, model.MessengerTelegram, chat, now); err != nil {
				return err
			}
			d.Settings[adoptedKey] = true
			if _, err := a.DB.NewUpdate().Model(&d).Column("settings").WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		if !d.Enabled {
			continue
		}
		if _, err := a.DB.NewUpdate().Model((*model.ProviderDefinition)(nil)).Set("enabled = ?", false).Where("id = ?", d.ID).Exec(ctx); err != nil {
			return err
		}
		off++
	}
	if off == 0 {
		return nil
	}
	a.Log.Info("turned off people's own notification targets; they link Telegram or Discord now", "targets", off)
	return a.Modules.Reload(ctx)
}

// PersonalTargets lists the users' own notification targets left over.
func (a *App) PersonalTargets(ctx context.Context) ([]PersonalTarget, error) {
	var defs []model.ProviderDefinition
	if err := a.DB.NewSelect().Model(&defs).Where("kind = ? AND user_id IS NOT NULL", string(modules.KindNotify)).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	out := []PersonalTarget{}
	for _, d := range defs {
		var u model.User
		_ = a.DB.NewSelect().Model(&u).Column("username").Where("id = ?", *d.UserID).Scan(ctx)
		out = append(out, PersonalTarget{ID: d.ID, Username: u.Username, Name: d.Name, Implementation: d.Implementation,
			Linked: d.Settings[adoptedKey] == true})
	}
	return out, nil
}

// RemovePersonalTargets deletes the users' own notification targets.
func (a *App) RemovePersonalTargets(ctx context.Context) (int, error) {
	res, err := a.DB.NewDelete().Model((*model.ProviderDefinition)(nil)).Where("kind = ? AND user_id IS NOT NULL", string(modules.KindNotify)).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), a.Modules.Reload(ctx)
}
