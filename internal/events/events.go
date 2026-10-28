// Package events is a small typed in-process pub/sub bus. Domain services
// publish events; notifications, the SSE hub and other listeners subscribe.
package events

import (
	"log/slog"
	"sync"
	"time"
)

// Event types. Notification providers subscribe to a subset of these.
const (
	ChapterImported   = "chapter.imported"
	ChapterUpgraded   = "chapter.upgraded"
	SeriesAdded       = "series.added"
	SeriesDeleted     = "series.deleted"
	DownloadFailed    = "download.failed"
	CleanupDone       = "cleanup.done"
	HealthIssue       = "health.issue"
	HealthRestored    = "health.restored"
	ExtensionUpdate   = "extension.update"
	ManualInteraction = "manual.required"
	RequestCreated    = "request.created"
	// RequestUpdated goes to the people who asked (their own targets only).
	RequestUpdated = "request.updated"
	Test           = "test"

	// ResourceChanged is emitted for UI cache invalidation (SSE), not notifications.
	ResourceChanged = "resource.changed"
)

// NotificationEvents lists events a notification provider can opt into.
var NotificationEvents = []string{
	ChapterImported, ChapterUpgraded, SeriesAdded, SeriesDeleted, DownloadFailed,
	CleanupDone, HealthIssue, HealthRestored, ExtensionUpdate, ManualInteraction, RequestCreated,
}

// PersonalEvents are what a user's own notification targets can get: new
// chapters of series they follow, and news about their requests.
var PersonalEvents = []string{ChapterImported, RequestUpdated}

type Event struct {
	Type string    `json:"type"`
	Time time.Time `json:"time"`
	// SeriesID is set for series-scoped events (used for tag filtering).
	SeriesID int64 `json:"seriesId,omitempty"`
	// Payload is event specific (see the payload types below).
	Payload any `json:"payload,omitempty"`
}

// Resource is the payload of ResourceChanged.
type Resource struct {
	Name   string `json:"name"`   // "series", "chapter", "queue", "command", "health", ...
	Action string `json:"action"` // "updated", "deleted", "created", "sync"
	ID     int64  `json:"id,omitempty"`
}

type ChapterImportedPayload struct {
	SeriesTitle string  `json:"seriesTitle"`
	Chapter     string  `json:"chapter"`
	NumberSort  float64 `json:"numberSort"`
	Title       string  `json:"title"`
	Source      string  `json:"source"`
	Scanlator   string  `json:"scanlator"`
	Upgrade     bool    `json:"upgrade"`
	Upscaled    bool    `json:"upscaled"`
	CoverURL    string  `json:"coverUrl"`
	// Backlog: the chapter was already out when the series was added, so
	// it's catching up rather than a new release.
	Backlog bool `json:"backlog,omitempty"`
}

type MessagePayload struct {
	Title   string   `json:"title"`
	Message string   `json:"message"`
	Items   []string `json:"items,omitempty"`
}

type Handler func(Event)

type Bus struct {
	mu   sync.RWMutex
	subs map[int]sub
	next int
}

type sub struct {
	types map[string]bool // nil = all
	fn    Handler
}

func NewBus() *Bus { return &Bus{subs: map[int]sub{}} }

// Subscribe registers fn for the given event types (all when empty). Handlers
// run synchronously on the publisher goroutine; long work must be offloaded.
func (b *Bus) Subscribe(fn Handler, types ...string) (unsubscribe func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	var set map[string]bool
	if len(types) > 0 {
		set = map[string]bool{}
		for _, t := range types {
			set[t] = true
		}
	}
	b.subs[id] = sub{types: set, fn: fn}
	return func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.subs))
	for _, s := range b.subs {
		if s.types == nil || s.types[e.Type] {
			handlers = append(handlers, s.fn)
		}
	}
	b.mu.RUnlock()
	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("event handler panic", "type", e.Type, "panic", r)
				}
			}()
			h(e)
		}()
	}
}

// Changed is a helper for ResourceChanged events.
func (b *Bus) Changed(name, action string, id int64) {
	b.Publish(Event{Type: ResourceChanged, Payload: Resource{Name: name, Action: action, ID: id}})
}
