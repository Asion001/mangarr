package messenger

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// LinkTTL is how long a link from My account stays usable.
const LinkTTL = 10 * time.Minute

// Service links people's messenger accounts to the server's bots and
// listens to the Telegram bot.
type Service struct {
	DB       *db.DB
	Settings *settings.Store
	Bus      *events.Bus
	Log      *slog.Logger
	// Username returns a user's name, for the bot's replies.
	Username func(ctx context.Context, userID int64) string

	mu          sync.Mutex
	botName     map[string]Identity // per token
	status      map[string]string   // per bot: the last listening error
	wake        chan struct{}
	digestRetry map[int64]time.Time // per link, after a failed digest
	pruned      time.Time
}

// Start listens to the Telegram bot while it is switched on.
func (s *Service) Start(ctx context.Context) error {
	go s.telegramLoop(ctx)
	go s.sendLoop(ctx)
	return nil
}

// Telegram returns a client for the configured bot, or nil while it is off.
func (s *Service) Telegram(ctx context.Context) *Telegram {
	m, err := s.Settings.Messenger(ctx)
	if err != nil || !m.Telegram.Enabled || m.Telegram.BotToken == "" {
		return nil
	}
	return &Telegram{APIURL: m.Telegram.APIURL, Token: m.Telegram.BotToken}
}

// Bot returns who the Telegram bot is (cached per token).
func (s *Service) Bot(ctx context.Context, tg *Telegram) (Identity, error) {
	s.mu.Lock()
	id, ok := s.botName[tg.Token]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	id, err := tg.Me(ctx)
	if err != nil {
		return Identity{}, err
	}
	s.mu.Lock()
	if s.botName == nil {
		s.botName = map[string]Identity{}
	}
	s.botName[tg.Token] = id
	s.mu.Unlock()
	return id, nil
}

// Status is the last error listening to a bot ("" when fine).
func (s *Service) Status(kind string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status[kind]
}

func (s *Service) setStatus(kind, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status == nil {
		s.status = map[string]string{}
	}
	s.status[kind] = msg
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewLinkToken makes a single-use token that links userID's account on
// kind when the bot gets it back. Older tokens of the same user and kind
// stop working.
func (s *Service) NewLinkToken(ctx context.Context, userID int64, kind string) (string, time.Time, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(b) // [A-Za-z0-9_-], what /start accepts
	now := time.Now().UTC()
	expires := now.Add(LinkTTL)
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewDelete().Model((*model.MessengerLinkToken)(nil)).
			Where("expires_at < ? OR (user_id = ? AND kind = ?)", now, userID, kind).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&model.MessengerLinkToken{TokenHash: hashToken(token), UserID: userID, Kind: kind, ExpiresAt: expires, CreatedAt: now}).Exec(ctx)
		return err
	})
	return token, expires, err
}

// ErrLinkExpired is a token that is unknown, used or too old.
var ErrLinkExpired = errors.New("this link has expired")

// Redeem links the account behind token to who, and returns the user.
func (s *Service) Redeem(ctx context.Context, kind, token string, who Identity) (int64, error) {
	var userID int64
	now := time.Now().UTC()
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var t model.MessengerLinkToken
		err := tx.NewSelect().Model(&t).Where("token_hash = ? AND kind = ?", hashToken(token), kind).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLinkExpired
		}
		if err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model(&t).WherePK().Exec(ctx); err != nil {
			return err
		}
		if t.ExpiresAt.Before(now) {
			return ErrLinkExpired
		}
		userID = t.UserID
		return s.link(ctx, tx, userID, kind, who, now)
	})
	if err != nil {
		return 0, err
	}
	s.Bus.Changed("messenger", "linked", userID)
	return userID, nil
}

// link stores who as userID's account on kind, replacing the user's old
// account there and taking the account away from anyone else who had it.
func (s *Service) link(ctx context.Context, tx bun.Tx, userID int64, kind string, who Identity, now time.Time) error {
	mode, evs, err := s.preferences(ctx, tx, userID)
	if err != nil {
		return err
	}
	if _, err := tx.NewDelete().Model((*model.MessengerLink)(nil)).
		Where("kind = ? AND (user_id = ? OR external_id = ?)", kind, userID, who.ID).Exec(ctx); err != nil {
		return err
	}
	_, err = tx.NewInsert().Model(&model.MessengerLink{UserID: userID, Kind: kind, ExternalID: who.ID, DisplayName: displayName(kind, who),
		Mode: mode, Events: evs, Status: model.LinkActive, CreatedAt: now, UpdatedAt: now}).Exec(ctx)
	return err
}

func displayName(kind string, who Identity) string {
	if who.Username != "" {
		if kind == model.MessengerTelegram {
			return "@" + who.Username
		}
		return who.Username
	}
	return who.Name
}

// preferences are what a new link starts with: the user's choices on
// another linked account, or instant (digest when instant is off) for
// every personal event.
func (s *Service) preferences(ctx context.Context, q bun.IDB, userID int64) (string, []string, error) {
	var other model.MessengerLink
	err := q.NewSelect().Model(&other).Where("user_id = ?", userID).Order("id").Limit(1).Scan(ctx)
	if err == nil {
		return other.Mode, other.Events, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", nil, err
	}
	mode := model.DeliveryInstant
	if m, err := s.Settings.Messenger(ctx); err == nil && !m.AllowInstant {
		mode = model.DeliveryDigest
	}
	return mode, slices.Clone(events.PersonalEvents), nil
}

// Links lists a user's linked accounts.
func (s *Service) Links(ctx context.Context, userID int64) ([]model.MessengerLink, error) {
	links := []model.MessengerLink{}
	err := s.DB.NewSelect().Model(&links).Where("user_id = ?", userID).Order("kind").Scan(ctx)
	return links, err
}

// Unlink removes a user's account on kind.
func (s *Service) Unlink(ctx context.Context, userID int64, kind string) error {
	_, err := s.DB.NewDelete().Model((*model.MessengerLink)(nil)).Where("user_id = ? AND kind = ?", userID, kind).Exec(ctx)
	if err == nil {
		s.Bus.Changed("messenger", "unlinked", userID)
	}
	return err
}

// SetPreferences sets what and how often a user hears, on every account.
func (s *Service) SetPreferences(ctx context.Context, userID int64, mode string, evs []string) error {
	switch mode {
	case model.DeliveryOff, model.DeliveryInstant, model.DeliveryDigest, model.DeliveryBoth:
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
	if mode == model.DeliveryInstant || mode == model.DeliveryBoth {
		if m, err := s.Settings.Messenger(ctx); err == nil && !m.AllowInstant {
			return errors.New("instant messages are switched off on this server")
		}
	}
	clean := []string{}
	for _, e := range evs {
		if slices.Contains(events.PersonalEvents, e) && !slices.Contains(clean, e) {
			clean = append(clean, e)
		}
	}
	now := time.Now().UTC()
	digestModes := bun.In([]string{model.DeliveryDigest, model.DeliveryBoth})
	// a digest starting now covers what comes from now on, not what was
	// already sent as it arrived
	_, err := s.DB.NewUpdate().Model((*model.MessengerLink)(nil)).
		Set("digest_through = CASE WHEN mode IN (?) THEN digest_through ELSE ? END", digestModes, now).
		Set("mode = ?", mode).Set("events = ?", clean).
		Set("updated_at = ?", now).Where("user_id = ?", userID).Exec(ctx)
	if err == nil {
		s.Bus.Changed("messenger", "updated", userID)
	}
	return err
}

// telegramLoop answers /start and /stop while the bot is on. A settings
// change ends the current long poll so a new token takes effect at once.
func (s *Service) telegramLoop(ctx context.Context) {
	changed := make(chan struct{}, 1)
	unsub := s.Bus.Subscribe(func(e events.Event) {
		if r, ok := e.Payload.(events.Resource); ok && r.Name == "settings" {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	}, events.ResourceChanged)
	defer unsub()
	var offset int64
	token := ""
	backoff := time.Second
	for ctx.Err() == nil {
		tg := s.Telegram(ctx)
		if tg == nil {
			s.setStatus(model.MessengerTelegram, "")
			select {
			case <-ctx.Done():
				return
			case <-changed:
			case <-time.After(time.Minute):
			}
			continue
		}
		if tg.Token != token {
			token, offset = tg.Token, 0
		}
		pollCtx, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-changed:
				cancel()
			case <-pollCtx.Done():
			}
		}()
		updates, err := tg.Updates(pollCtx, offset, 50*time.Second)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if pollCtx.Err() != nil {
				continue // settings changed
			}
			s.setStatus(model.MessengerTelegram, err.Error())
			s.Log.Warn("telegram bot: can't get messages", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 5*time.Minute)
			continue
		}
		backoff = time.Second
		s.setStatus(model.MessengerTelegram, "")
		for _, u := range updates {
			offset = max(offset, u.ID+1)
			s.handleTelegram(ctx, tg, u)
		}
	}
}

func (s *Service) handleTelegram(ctx context.Context, tg *Telegram, u Update) {
	if !u.Private || u.ChatID == "" {
		return
	}
	cmd, arg, _ := strings.Cut(strings.TrimSpace(u.Text), " ")
	cmd, _, _ = strings.Cut(cmd, "@") // /start@shelf_bot in some clients
	reply := func(msg string) {
		if err := tg.Send(ctx, u.ChatID, msg); err != nil {
			s.Log.Warn("telegram bot: can't reply", "err", err)
		}
	}
	switch cmd {
	case "/start":
		arg = strings.TrimSpace(arg)
		if arg == "" {
			reply("Hi! To get your mangarr updates here, open <b>My account › Notifications</b> in mangarr and press <b>Link Telegram</b>.")
			return
		}
		who := u.From
		who.ID = u.ChatID
		userID, err := s.Redeem(ctx, model.MessengerTelegram, arg, who)
		if errors.Is(err, ErrLinkExpired) {
			reply("This link has expired. Press <b>Link Telegram</b> in mangarr again.")
			return
		}
		if err != nil {
			s.Log.Warn("telegram bot: link failed", "err", err)
			reply("Something went wrong linking your account. Try again in a moment.")
			return
		}
		name := ""
		if s.Username != nil {
			name = s.Username(ctx, userID)
		}
		msg := "Linked. You'll get your mangarr updates here."
		if name != "" {
			msg = "Linked to mangarr as <b>" + html.EscapeString(name) + "</b>. You'll get your updates here."
		}
		reply(msg + " Send /stop to unlink.")
	case "/stop":
		var link model.MessengerLink
		err := s.DB.NewSelect().Model(&link).Where("kind = ? AND external_id = ?", model.MessengerTelegram, u.ChatID).Scan(ctx)
		if err != nil {
			reply("This chat isn't linked to mangarr.")
			return
		}
		if err := s.Unlink(ctx, link.UserID, model.MessengerTelegram); err != nil {
			s.Log.Warn("telegram bot: unlink failed", "err", err)
			return
		}
		reply("Unlinked. You won't get mangarr updates here any more.")
	}
}

// Preferences are what and how often a user hears (what a new link would
// start with when nothing is linked yet).
func (s *Service) Preferences(ctx context.Context, userID int64) (string, []string, error) {
	return s.preferences(ctx, s.DB, userID)
}

// SendTest sends a test message to a user's account on kind.
func (s *Service) SendTest(ctx context.Context, userID int64, kind string) error {
	var link model.MessengerLink
	if err := s.DB.NewSelect().Model(&link).Where("user_id = ? AND kind = ?", userID, kind).Scan(ctx); err != nil {
		return errors.New("that account isn't linked")
	}
	return s.Deliver(ctx, link, "This is a test message from mangarr. Your updates will arrive like this.")
}

// Deliver sends one message to a linked account and records how it went
// on the link: a chat that will never take messages again is marked broken.
func (s *Service) Deliver(ctx context.Context, link model.MessengerLink, htmlText string) error {
	var err error
	switch link.Kind {
	case model.MessengerTelegram:
		tg := s.Telegram(ctx)
		if tg == nil {
			return errors.New("the Telegram bot is switched off")
		}
		err = tg.Send(ctx, link.ExternalID, htmlText)
	default:
		err = s.deliverDiscord(ctx, link, htmlText)
	}
	now := time.Now().UTC()
	q := s.DB.NewUpdate().Model((*model.MessengerLink)(nil)).Set("last_attempt_at = ?", now).Where("id = ?", link.ID)
	switch {
	case err == nil:
		q = q.Set("status = ?", model.LinkActive).Set("last_error = ''")
	case Gone(err):
		q = q.Set("status = ?", model.LinkBroken).Set("last_error = ?", err.Error())
	default:
		q = q.Set("last_error = ?", err.Error())
	}
	if _, uerr := q.Exec(ctx); uerr == nil && (err != nil || link.Status != model.LinkActive) {
		s.Bus.Changed("messenger", "updated", link.UserID)
	}
	if err == nil && link.Status == model.LinkBroken {
		// fixed: what waited for it goes out now
		_, _ = s.DB.NewUpdate().Model((*model.NotificationDispatch)(nil)).Set("available_at = ?", now).
			Where("link_id = ? AND sent_at IS NULL", link.ID).Exec(ctx)
		s.Wake()
	}
	return err
}

// Discord returns a client for the configured bot, or nil while it is off.
func (s *Service) Discord(ctx context.Context) *Discord {
	m, err := s.Settings.Messenger(ctx)
	if err != nil || !m.Discord.Enabled || m.Discord.BotToken == "" {
		return nil
	}
	return &Discord{Token: m.Discord.BotToken}
}

// deliverDiscord sends a direct message; the DM channel is looked up
// each time (Discord returns the existing one).
func (s *Service) deliverDiscord(ctx context.Context, link model.MessengerLink, htmlText string) error {
	dc := s.Discord(ctx)
	if dc == nil {
		return errors.New("the Discord bot is switched off")
	}
	ch, err := dc.DM(ctx, link.ExternalID)
	if err != nil {
		return err
	}
	return dc.Send(ctx, ch, Markdown(htmlText))
}

// LinkDiscord finishes the Discord sign-in: state is the link token.
func (s *Service) LinkDiscord(ctx context.Context, state, code, redirectURI string) (int64, error) {
	m, err := s.Settings.Messenger(ctx)
	if err != nil {
		return 0, err
	}
	if !m.Discord.Enabled {
		return 0, errors.New("the Discord bot is switched off")
	}
	who, err := DiscordUser(ctx, nil, m.Discord.ClientID, m.Discord.ClientSecret, redirectURI, code)
	if err != nil {
		return 0, err
	}
	return s.Redeem(ctx, model.MessengerDiscord, state, who)
}
