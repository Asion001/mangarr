package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/messenger"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerMyMessenger) }

// me is the signed-in user (not the API key or logins off).
func me(ctx context.Context) (*access.Principal, error) {
	p := access.From(ctx)
	if p == nil || p.Kind != access.KindUser {
		return nil, huma.Error400BadRequest("sign in as a user for this")
	}
	return p, nil
}

// MessengerBot is whether people can link an account on a bot.
type MessengerBot struct {
	Available bool `json:"available"`
	// Bot is the bot's @username (Telegram).
	Bot string `json:"bot,omitempty"`
}

// MyMessenger is your linked messenger accounts and what you hear.
type MyMessenger struct {
	Telegram MessengerBot          `json:"telegram"`
	Discord  MessengerBot          `json:"discord"`
	Links    []model.MessengerLink `json:"links" nullable:"false"`
	Mode     string                `json:"mode" enum:"off,instant,daily_digest,instant_and_digest"`
	Events   []string              `json:"events" nullable:"false"`
	// AllowInstant is false when the server only sends the daily digest.
	AllowInstant bool `json:"allowInstant"`
	DigestHour   int  `json:"digestHour"`
}

type MessengerLinkStart struct {
	// URL opens a chat with the bot that links the account.
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type messengerKindPath struct {
	Kind string `path:"kind" enum:"telegram,discord"`
}

func (s *Server) myMessenger(ctx context.Context) (*struct{ Body MyMessenger }, error) {
	p, err := me(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.app.Settings.Messenger(ctx)
	if err != nil {
		return nil, toHTTPError(err)
	}
	out := MyMessenger{AllowInstant: m.AllowInstant, DigestHour: m.DigestHour}
	if tg := s.app.Messenger.Telegram(ctx); tg != nil {
		out.Telegram.Available = true
		if id, err := s.app.Messenger.Bot(ctx, tg); err == nil {
			out.Telegram.Bot = id.Username
		}
	}
	out.Discord.Available = m.Discord.Enabled && m.Discord.ClientID != "" && m.Discord.ClientSecret != "" && m.Discord.BotToken != "" && s.discordRedirectURL(ctx) != ""
	if out.Links, err = s.app.Messenger.Links(ctx, p.UserID); err != nil {
		return nil, toHTTPError(err)
	}
	if out.Mode, out.Events, err = s.app.Messenger.Preferences(ctx, p.UserID); err != nil {
		return nil, toHTTPError(err)
	}
	return &struct{ Body MyMessenger }{out}, nil
}

func (s *Server) registerMyMessenger() {
	tags := []string{"Account"}
	huma.Register(s.api, huma.Operation{OperationID: "me-messenger", Method: http.MethodGet, Path: "/api/v1/me/messenger", Tags: tags,
		Summary: "Your linked Telegram and Discord accounts"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body MyMessenger }, error) {
			return s.myMessenger(ctx)
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-messenger-preferences", Method: http.MethodPut, Path: "/api/v1/me/messenger/preferences", Tags: tags,
		Summary: "Set what you hear about and how often"},
		func(ctx context.Context, in *struct {
			Body struct {
				Mode   string   `json:"mode" enum:"off,instant,daily_digest,instant_and_digest"`
				Events []string `json:"events"`
			}
		}) (*struct{ Body MyMessenger }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			if err := s.app.Messenger.SetPreferences(ctx, p.UserID, in.Body.Mode, in.Body.Events); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return s.myMessenger(ctx)
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-messenger-telegram-link", Method: http.MethodPost, Path: "/api/v1/me/messenger/telegram/link", Tags: tags,
		Summary: "Start linking Telegram: open the returned link and press Start"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body MessengerLinkStart }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			tg := s.app.Messenger.Telegram(ctx)
			if tg == nil {
				return nil, huma.Error409Conflict("the Telegram bot isn't set up on this server")
			}
			bot, err := s.app.Messenger.Bot(ctx, tg)
			if err != nil || bot.Username == "" {
				return nil, huma.Error502BadGateway("the Telegram bot can't be reached")
			}
			token, expires, err := s.app.Messenger.NewLinkToken(ctx, p.UserID, model.MessengerTelegram)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &struct{ Body MessengerLinkStart }{MessengerLinkStart{URL: "https://t.me/" + bot.Username + "?start=" + token, ExpiresAt: expires}}, nil
		})

	// Discord: sign in with the identify scope; the link token is the OAuth state
	huma.Register(s.api, huma.Operation{OperationID: "me-messenger-discord-link", Method: http.MethodGet, Path: "/api/v1/me/messenger/discord/link", Tags: tags,
		Summary: "Start linking Discord (redirects to Discord)"},
		func(ctx context.Context, _ *struct{}) (*messengerRedirect, error) {
			p, err := me(ctx)
			if err != nil {
				return s.messengerBack("", err), nil
			}
			m, err := s.app.Settings.Messenger(ctx)
			redirect := s.discordRedirectURL(ctx)
			if err != nil || !m.Discord.Enabled || m.Discord.ClientID == "" || redirect == "" {
				return s.messengerBack("", errors.New("the Discord bot isn't set up on this server")), nil
			}
			state, _, err := s.app.Messenger.NewLinkToken(ctx, p.UserID, model.MessengerDiscord)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return &messengerRedirect{Status: http.StatusSeeOther, Location: messenger.DiscordAuthorizeURL(m.Discord.ClientID, redirect, state)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-messenger-discord-callback", Method: http.MethodGet, Path: "/api/v1/me/messenger/discord/callback", Tags: tags,
		Security: []map[string][]string{}, Summary: "Where Discord sends people back after they sign in"},
		func(ctx context.Context, in *struct {
			Code  string `query:"code"`
			State string `query:"state"`
			Error string `query:"error"`
		}) (*messengerRedirect, error) {
			if in.Error != "" || in.Code == "" || in.State == "" {
				return s.messengerBack("", errors.New("Discord didn't link the account")), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := s.app.Messenger.LinkDiscord(ctx, in.State, in.Code, s.discordRedirectURL(ctx)); err != nil {
				s.app.Log.Info("discord link failed", "err", err)
				return s.messengerBack("", err), nil
			}
			return s.messengerBack(model.MessengerDiscord, nil), nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-messenger-test", Method: http.MethodPost, Path: "/api/v1/me/messenger/{kind}/test", Tags: tags,
		Summary: "Send yourself a test message"},
		func(ctx context.Context, in *messengerKindPath) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			if err := s.app.Messenger.SendTest(ctx, p.UserID, in.Kind); err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			return nil, nil
		})

	huma.Register(s.api, huma.Operation{OperationID: "me-messenger-unlink", Method: http.MethodDelete, Path: "/api/v1/me/messenger/{kind}", Tags: tags,
		Summary: "Unlink an account", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *messengerKindPath) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, toHTTPError(s.app.Messenger.Unlink(ctx, p.UserID, in.Kind))
		})
}

type messengerRedirect struct {
	Status   int
	Location string `header:"Location"`
}

// messengerBack returns to My account, saying what was linked or why not.
func (s *Server) messengerBack(linked string, err error) *messengerRedirect {
	q := url.Values{}
	if err != nil {
		q.Set("messenger_error", err.Error())
	} else {
		q.Set("linked", linked)
	}
	return &messengerRedirect{Status: http.StatusSeeOther, Location: s.app.Cfg.URLBase + "/account?" + q.Encode()}
}
