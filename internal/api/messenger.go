package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/messenger"
	"github.com/Asion001/mangarr/internal/settings"
)

// MessengerSettings are the messenger bots with their secrets masked.
type MessengerSettings struct {
	settings.Messenger
	// DiscordRedirectURL is what the Discord application must list as a
	// redirect (account linking signs people in with Discord).
	DiscordRedirectURL string `json:"discordRedirectUrl" required:"false" readOnly:"true"`
}

// maskMessenger hides the secrets the UI never gets back.
func maskMessenger(m settings.Messenger) settings.Messenger {
	for _, p := range []*string{&m.Telegram.BotToken, &m.Discord.ClientSecret, &m.Discord.BotToken} {
		if *p != "" {
			*p = secretMask
		}
	}
	return m
}

// unmaskMessenger keeps the stored secret where the UI sent the mask back.
func unmaskMessenger(m, stored settings.Messenger) settings.Messenger {
	keep := func(p *string, old string) {
		if *p == secretMask {
			*p = old
		}
		*p = strings.TrimSpace(*p)
	}
	keep(&m.Telegram.BotToken, stored.Telegram.BotToken)
	keep(&m.Discord.ClientSecret, stored.Discord.ClientSecret)
	keep(&m.Discord.BotToken, stored.Discord.BotToken)
	m.Telegram.APIURL = strings.TrimRight(strings.TrimSpace(m.Telegram.APIURL), "/")
	m.Discord.ClientID = strings.TrimSpace(m.Discord.ClientID)
	if m.Telegram.AnnounceEvents == nil {
		m.Telegram.AnnounceEvents = []string{}
	}
	if m.Discord.AnnounceEvents == nil {
		m.Discord.AnnounceEvents = []string{}
	}
	return m
}

func (s *Server) messengerSettings(ctx context.Context) (*struct{ Body MessengerSettings }, error) {
	m, err := s.app.Settings.Messenger(ctx)
	if err != nil {
		return nil, toHTTPError(err)
	}
	return &struct{ Body MessengerSettings }{MessengerSettings{Messenger: maskMessenger(m), DiscordRedirectURL: s.discordRedirectURL(ctx)}}, nil
}

// discordRedirectURL is where Discord sends people back after they link.
func (s *Server) discordRedirectURL(ctx context.Context) string {
	base := ""
	if g, err := s.app.Settings.General(ctx); err == nil {
		base = strings.TrimRight(g.PublicURL, "/")
	}
	if base == "" {
		return "" // Discord needs a full address: Settings › General › Public URL
	}
	return base + s.app.Cfg.URLBase + "/api/v1/me/messenger/discord/callback"
}

type MessengerTestInput struct {
	Body struct {
		Bot string `json:"bot" enum:"telegram,discord"`
		// Settings are the values on the page, which may not be saved yet.
		Settings MessengerSettings `json:"settings"`
	}
}

func (s *Server) registerMessengerSettings() {
	tags := []string{"Settings"}
	huma.Register(s.api, huma.Operation{OperationID: "settings-get-messenger", Method: http.MethodGet, Path: "/api/v1/settings/messenger", Tags: tags,
		Summary: "Messenger bots (secrets masked)"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body MessengerSettings }, error) {
			return s.messengerSettings(ctx)
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-put-messenger", Method: http.MethodPut, Path: "/api/v1/settings/messenger", Tags: tags,
		Summary: "Save the messenger bots (send a masked secret back to keep the stored one)"},
		func(ctx context.Context, in *struct{ Body MessengerSettings }) (*struct{ Body MessengerSettings }, error) {
			stored, err := s.app.Settings.Messenger(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			m := unmaskMessenger(in.Body.Messenger, stored)
			if err := m.Validate(); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			if err := s.app.Settings.Set(ctx, settings.KeyMessenger, m); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("settings", "updated", 0)
			return s.messengerSettings(ctx)
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-messenger-test", Method: http.MethodPost, Path: "/api/v1/settings/messenger/test", Tags: tags,
		Summary: "Check a bot's credentials without sending anything"},
		func(ctx context.Context, in *MessengerTestInput) (*struct{ Body messenger.Identity }, error) {
			stored, err := s.app.Settings.Messenger(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			m := unmaskMessenger(in.Body.Settings.Messenger, stored)
			ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()
			var id messenger.Identity
			switch in.Body.Bot {
			case "telegram":
				id, err = (&messenger.Telegram{APIURL: m.Telegram.APIURL, Token: m.Telegram.BotToken}).Me(ctx)
			default:
				id, err = (&messenger.Discord{Token: m.Discord.BotToken}).Me(ctx)
			}
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return &struct{ Body messenger.Identity }{id}, nil
		})
}
