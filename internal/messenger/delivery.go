package messenger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
)

// Message is what a person is told: a title, a line or two, optional
// list items and a link back to mangarr.
type Message struct {
	Title  string   `json:"title"`
	Body   string   `json:"body,omitempty"`
	Items  []string `json:"items,omitempty"`
	URL    string   `json:"url,omitempty"`
	Series string   `json:"series,omitempty"`
}

const (
	// giveUpAfter drops messages that could not go out for this long.
	giveUpAfter = 3 * 24 * time.Hour
	// keepInbox is how long sent messages stay in the inbox table.
	keepInbox = 30 * 24 * time.Hour
	maxItems  = 10
)

var retrySteps = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour}

func retryAfter(attempts int) time.Duration {
	return retrySteps[min(max(attempts-1, 0), len(retrySteps)-1)]
}

func instantFor(mode, event string) bool {
	if event != events.ChapterImported {
		return mode != model.DeliveryOff // request news never waits for the digest
	}
	return mode == model.DeliveryInstant || mode == model.DeliveryBoth
}

func digestFor(mode string) bool {
	return mode == model.DeliveryDigest || mode == model.DeliveryBoth
}

// Enqueue records a message for each of users who has an account linked
// that wants event, and queues it for the accounts that get it right
// away. Chapters for digest accounts wait in the inbox for the digest.
func (s *Service) Enqueue(ctx context.Context, users []int64, event string, seriesID int64, msg Message) error {
	if len(users) == 0 {
		return nil
	}
	var links []model.MessengerLink
	if err := s.DB.NewSelect().Model(&links).Where("user_id IN (?)", bun.In(users)).Where("mode <> ?", model.DeliveryOff).Scan(ctx); err != nil {
		return err
	}
	byUser := map[int64][]model.MessengerLink{}
	for _, l := range links {
		if slices.Contains(l.Events, event) {
			byUser[l.UserID] = append(byUser[l.UserID], l)
		}
	}
	if len(byUser) == 0 {
		return nil
	}
	now := time.Now().UTC()
	sum := sha256.Sum256([]byte(msg.Title + "\x00" + msg.Body + "\x00" + strings.Join(msg.Items, "\x00")))
	key := fmt.Sprintf("%s:%d:%s:%d", event, seriesID, hex.EncodeToString(sum[:8]), now.Unix()/60)
	payload := map[string]any{"title": msg.Title, "body": msg.Body, "items": msg.Items, "url": msg.URL, "series": msg.Series}
	var sid *int64
	if seriesID > 0 {
		sid = &seriesID
	}
	queued := false
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for userID, ls := range byUser {
			d := model.NotificationDelivery{UserID: userID, DedupeKey: key, EventType: event, SeriesID: sid, Payload: payload, CreatedAt: now}
			res, err := tx.NewInsert().Model(&d).On("CONFLICT (user_id, dedupe_key) DO NOTHING").Exec(ctx)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 || d.ID == 0 {
				continue // the same event again
			}
			for _, l := range ls {
				if !instantFor(l.Mode, event) {
					continue
				}
				if _, err := tx.NewInsert().Model(&model.NotificationDispatch{DeliveryID: d.ID, LinkID: l.ID, AvailableAt: now}).
					On("CONFLICT (delivery_id, link_id) DO NOTHING").Exec(ctx); err != nil {
					return err
				}
				queued = true
			}
		}
		return nil
	})
	if err == nil && queued {
		s.Wake()
	}
	return err
}

// Wake makes the sender look for due messages now.
func (s *Service) Wake() {
	s.mu.Lock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	ch := s.wake
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default:
	}
}

func (s *Service) wakeChan() chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake == nil {
		s.wake = make(chan struct{}, 1)
	}
	return s.wake
}

// sendLoop sends due messages and the daily digests.
func (s *Service) sendLoop(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	wake := s.wakeChan()
	for {
		s.SendDue(ctx)
		s.SendDigests(ctx, time.Now())
		s.prune(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-wake:
		}
	}
}

type dueRow struct {
	model.NotificationDispatch `bun:",extend"`
	DeliveryCreated            time.Time      `bun:"delivery_created"`
	Payload                    map[string]any `bun:"payload"`
}

// SendDue sends every queued message that is due.
func (s *Service) SendDue(ctx context.Context) {
	now := time.Now().UTC()
	var rows []dueRow
	err := s.DB.NewSelect().TableExpr("notification_dispatches AS d").
		ColumnExpr("d.*").ColumnExpr("n.created_at AS delivery_created").ColumnExpr("n.payload AS payload").
		Join("JOIN notification_deliveries AS n ON n.id = d.delivery_id").
		Where("d.sent_at IS NULL AND d.available_at <= ?", now).
		OrderExpr("d.available_at, d.id").Limit(100).Scan(ctx, &rows)
	if err != nil {
		s.Log.Warn("messenger: can't read the queue", "err", err)
		return
	}
	links := map[int64]*model.MessengerLink{}
	for _, r := range rows {
		if ctx.Err() != nil {
			return
		}
		l, ok := links[r.LinkID]
		if !ok {
			l = &model.MessengerLink{}
			if err := s.DB.NewSelect().Model(l).Where("id = ?", r.LinkID).Scan(ctx); err != nil {
				l = nil
			}
			links[r.LinkID] = l
		}
		q := s.DB.NewUpdate().Model((*model.NotificationDispatch)(nil)).Where("id = ?", r.ID)
		if l == nil || now.Sub(r.DeliveryCreated) > giveUpAfter {
			_, _ = q.Set("sent_at = ?", now).Set("last_error = ?", "gave up").Exec(ctx)
			continue
		}
		err := s.Deliver(ctx, *l, Render(payloadMessage(r.Payload)))
		if err == nil {
			_, _ = q.Set("sent_at = ?", now).Set("attempts = attempts + 1").Set("last_error = ''").Exec(ctx)
			l.Status = model.LinkActive
			continue
		}
		wait := retryAfter(r.Attempts + 1)
		if Gone(err) {
			l.Status = model.LinkBroken
			wait = time.Hour // until the person fixes it and presses Try again
		}
		_, _ = q.Set("attempts = attempts + 1").Set("available_at = ?", now.Add(wait)).Set("last_error = ?", err.Error()).Exec(ctx)
	}
}

func payloadMessage(p map[string]any) Message {
	str := func(k string) string { v, _ := p[k].(string); return v }
	m := Message{Title: str("title"), Body: str("body"), URL: str("url"), Series: str("series")}
	if items, ok := p["items"].([]any); ok {
		for _, it := range items {
			if v, ok := it.(string); ok {
				m.Items = append(m.Items, v)
			}
		}
	}
	return m
}

// Render formats a message as Telegram HTML (Discord gets it as markdown).
func Render(m Message) string {
	var b strings.Builder
	b.WriteString("<b>" + html.EscapeString(m.Title) + "</b>")
	if m.Body != "" {
		b.WriteString("\n" + html.EscapeString(m.Body))
	}
	for i, it := range m.Items {
		if i == maxItems {
			fmt.Fprintf(&b, "\n… and %d more", len(m.Items)-maxItems)
			break
		}
		b.WriteString("\n• " + html.EscapeString(it))
	}
	if m.URL != "" {
		b.WriteString("\n\n<a href=\"" + html.EscapeString(m.URL) + "\">Open in mangarr</a>")
	}
	return b.String()
}

// digestTime is the most recent digest hour at or before now (local time).
func digestTime(now time.Time, hour int) time.Time {
	t := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, now.Location())
	if t.After(now) {
		t = t.AddDate(0, 0, -1)
	}
	return t
}

// SendDigests sends each digest account the chapters since its last
// digest, once the digest hour has passed.
func (s *Service) SendDigests(ctx context.Context, now time.Time) {
	m, err := s.Settings.Messenger(ctx)
	if err != nil {
		return
	}
	due := digestTime(now, m.DigestHour).UTC()
	var links []model.MessengerLink
	if err := s.DB.NewSelect().Model(&links).Where("mode IN (?)", bun.In([]string{model.DeliveryDigest, model.DeliveryBoth})).Scan(ctx); err != nil {
		return
	}
	for _, l := range links {
		from := l.CreatedAt
		if l.DigestThrough != nil {
			from = *l.DigestThrough
		}
		if !from.Before(due) || !slices.Contains(l.Events, events.ChapterImported) {
			continue
		}
		s.mu.Lock()
		retry := s.digestRetry[l.ID]
		s.mu.Unlock()
		if now.Before(retry) {
			continue
		}
		var rows []model.NotificationDelivery
		if err := s.DB.NewSelect().Model(&rows).Where("user_id = ? AND event_type = ?", l.UserID, events.ChapterImported).
			Where("created_at > ? AND created_at <= ?", from, due).Order("created_at", "id").Scan(ctx); err != nil {
			continue
		}
		if len(rows) > 0 {
			if err := s.Deliver(ctx, l, digest(rows)); err != nil {
				s.mu.Lock()
				if s.digestRetry == nil {
					s.digestRetry = map[int64]time.Time{}
				}
				s.digestRetry[l.ID] = now.Add(15 * time.Minute)
				s.mu.Unlock()
				continue
			}
		}
		_, _ = s.DB.NewUpdate().Model((*model.MessengerLink)(nil)).Set("digest_through = ?", due).Where("id = ?", l.ID).Exec(ctx)
	}
}

// digest is one message for the day: each series with what arrived.
func digest(rows []model.NotificationDelivery) string {
	type entry struct {
		series, url string
		lines       []string
	}
	var order []string
	by := map[string]*entry{}
	for _, r := range rows {
		m := payloadMessage(r.Payload)
		key := m.Series
		if r.SeriesID != nil {
			key = strconv.FormatInt(*r.SeriesID, 10)
		}
		e := by[key]
		if e == nil {
			e = &entry{series: m.Series, url: m.URL}
			if e.series == "" {
				e.series = m.Title
			}
			by[key] = e
			order = append(order, key)
		}
		if m.Body != "" {
			e.lines = append(e.lines, m.Body)
		}
	}
	var b strings.Builder
	b.WriteString("<b>New chapters today</b>")
	for i, k := range order {
		if i == 30 {
			fmt.Fprintf(&b, "\n\n… and %d more series", len(order)-30)
			break
		}
		e := by[k]
		name := "<b>" + html.EscapeString(e.series) + "</b>"
		if e.url != "" {
			name = "<a href=\"" + html.EscapeString(e.url) + "\">" + name + "</a>"
		}
		b.WriteString("\n\n" + name)
		if len(e.lines) > 0 {
			b.WriteString("\n" + html.EscapeString(strings.Join(e.lines, "; ")))
		}
	}
	return b.String()
}

// prune drops old inbox entries now and then.
func (s *Service) prune(ctx context.Context) {
	s.mu.Lock()
	if time.Since(s.pruned) < time.Hour {
		s.mu.Unlock()
		return
	}
	s.pruned = time.Now()
	s.mu.Unlock()
	_, _ = s.DB.NewDelete().Model((*model.NotificationDelivery)(nil)).Where("created_at < ?", time.Now().UTC().Add(-keepInbox)).Exec(ctx)
	_, _ = s.DB.NewDelete().Model((*model.MessengerLinkToken)(nil)).Where("expires_at < ?", time.Now().UTC()).Exec(ctx)
}
