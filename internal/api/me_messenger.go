package api

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerMyMessenger) }

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
