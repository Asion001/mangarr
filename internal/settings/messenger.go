package settings

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/Asion001/mangarr/internal/events"
)

// KeyMessenger holds the server's own Telegram and Discord bots. People
// link their account to these bots to get private messages; they never
// enter tokens or URLs themselves.
const KeyMessenger = "messenger_bots"

// Messenger configures the messenger bots and how people's messages go out.
type Messenger struct {
	Telegram TelegramBot `json:"telegram"`
	Discord  DiscordBot  `json:"discord"`
	// AllowInstant lets people get a message as chapters arrive; off, they
	// can only pick the daily digest.
	AllowInstant bool `json:"allowInstant" desc:"Let people get messages as chapters arrive (off: daily digest only)."`
	// DigestHour is when the daily digest goes out (server time, 0-23).
	DigestHour int `json:"digestHour" minimum:"0" maximum:"23" desc:"Hour of the daily digest (server time, 0-23)."`
}

type TelegramBot struct {
	Enabled  bool   `json:"enabled" desc:"Send messages through the Telegram bot."`
	BotToken string `json:"botToken" secret:"true" desc:"Telegram bot token (from @BotFather)."`
	// APIURL is the Bot API server (a self-hosted one, or a proxy).
	APIURL string `json:"apiUrl" desc:"Telegram Bot API server."`
	// AnnounceChat is a chat or channel (@name or id) for install-wide events.
	AnnounceChat   string   `json:"announceChat" desc:"Chat or channel for announcements (@name or id; empty = none)."`
	AnnounceEvents []string `json:"announceEvents" desc:"Events announced in the chat."`
}

type DiscordBot struct {
	Enabled      bool   `json:"enabled" desc:"Send messages through the Discord bot."`
	ClientID     string `json:"clientId" desc:"Discord application (client) id."`
	ClientSecret string `json:"clientSecret" secret:"true" desc:"Discord application client secret."`
	BotToken     string `json:"botToken" secret:"true" desc:"Discord bot token."`
	// AnnounceChannel is a server channel id for install-wide events.
	AnnounceChannel string   `json:"announceChannel" desc:"Channel id for announcements (empty = none)."`
	AnnounceEvents  []string `json:"announceEvents" desc:"Events announced in the channel."`
}

func DefaultMessenger() Messenger {
	return Messenger{
		Telegram:     TelegramBot{APIURL: "https://api.telegram.org", AnnounceEvents: []string{}},
		Discord:      DiscordBot{AnnounceEvents: []string{}},
		AllowInstant: true,
		DigestHour:   9,
	}
}

// Validate rejects a bot switched on without what it needs.
func (m Messenger) Validate() error {
	var errs []error
	if u, err := url.Parse(strings.TrimSpace(m.Telegram.APIURL)); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		errs = append(errs, errors.New("the Telegram API server must be an http or https URL"))
	}
	if m.Telegram.Enabled && strings.TrimSpace(m.Telegram.BotToken) == "" {
		errs = append(errs, errors.New("the Telegram bot needs a token"))
	}
	if m.Discord.Enabled && (strings.TrimSpace(m.Discord.ClientID) == "" || strings.TrimSpace(m.Discord.ClientSecret) == "" || strings.TrimSpace(m.Discord.BotToken) == "") {
		errs = append(errs, errors.New("the Discord bot needs a client id, a client secret and a bot token"))
	}
	if m.DigestHour < 0 || m.DigestHour > 23 {
		errs = append(errs, errors.New("the digest hour must be between 0 and 23"))
	}
	for name, list := range map[string][]string{"Telegram": m.Telegram.AnnounceEvents, "Discord": m.Discord.AnnounceEvents} {
		for _, e := range list {
			if !slices.Contains(events.NotificationEvents, e) {
				errs = append(errs, fmt.Errorf("%s: %q can't be announced", name, e))
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Store) Messenger(ctx context.Context) (Messenger, error) {
	v := DefaultMessenger()
	return v, s.Get(ctx, KeyMessenger, &v)
}
