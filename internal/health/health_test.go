package health

import (
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/events"
)

func TestOutageNotifiesOnceAfterGrace(t *testing.T) {
	bus := events.NewBus()
	var got []events.Event
	bus.Subscribe(func(e events.Event) { got = append(got, e) }, events.HealthIssue, events.HealthRestored)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := New(nil, bus, nil, nil, nil)
	c.now = func() time.Time { return now }
	down := func(msg string) []Check {
		return []Check{{Source: "Metadata", Type: Error, Message: msg, Key: "unavailable-1", Grace: outageGrace}}
	}

	res := c.record(down("Example is unavailable: example: HTTP 522, next try at 12:01"))
	if len(got) != 0 || res[0].Type != Notice {
		t.Fatalf("within grace: events %v, type %s", got, res[0].Type)
	}
	now = now.Add(20 * time.Minute)
	c.record(down("Example is unavailable: example: HTTP 522, next try at 12:25"))
	if len(got) != 0 {
		t.Fatalf("still within grace: %v", got)
	}
	now = now.Add(15 * time.Minute)
	res = c.record(down("Example is unavailable: example: HTTP 522, next try at 13:00"))
	if len(got) != 1 || got[0].Type != events.HealthIssue || res[0].Type != Error {
		t.Fatalf("after grace: events %v, type %s", got, res[0].Type)
	}
	now = now.Add(5 * time.Minute)
	c.record(down("Example is unavailable: example: HTTP 503, next try at 14:00"))
	if len(got) != 1 {
		t.Fatalf("repeated the alert while still down: %v", got)
	}
	c.record(nil)
	if len(got) != 2 || got[1].Type != events.HealthRestored {
		t.Fatalf("restore: %v", got)
	}
}

func TestShortOutageStaysQuiet(t *testing.T) {
	bus := events.NewBus()
	var got []events.Event
	bus.Subscribe(func(e events.Event) { got = append(got, e) }, events.HealthIssue, events.HealthRestored)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	c := New(nil, bus, nil, nil, nil)
	c.now = func() time.Time { return now }
	c.record([]Check{{Source: "Metadata", Type: Error, Message: "down", Key: "unavailable-1", Grace: outageGrace}})
	now = now.Add(10 * time.Minute)
	c.record(nil)
	if len(got) != 0 {
		t.Fatalf("a short outage notified: %v", got)
	}
}

func TestPlainIssueNotifiesAtOnce(t *testing.T) {
	bus := events.NewBus()
	var got []events.Event
	bus.Subscribe(func(e events.Event) { got = append(got, e) }, events.HealthIssue)
	c := New(nil, bus, nil, nil, nil)
	c.record([]Check{{Source: "Root folders", Type: Error, Message: "/data is not writable"}})
	c.record([]Check{{Source: "Root folders", Type: Error, Message: "/data is not writable"}})
	if len(got) != 1 {
		t.Fatalf("events: %v", got)
	}
}
