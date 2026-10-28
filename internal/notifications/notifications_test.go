package notifications

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/notify"
	"github.com/Asion001/mangarr/internal/settings"
)

type recorder struct {
	mu   sync.Mutex
	msgs []notify.Message
}

var rec = &recorder{}

type recModule struct{}

func (recModule) Test(ctx context.Context) error { return nil }
func (recModule) Send(ctx context.Context, m notify.Message) error {
	rec.mu.Lock()
	rec.msgs = append(rec.msgs, m)
	rec.mu.Unlock()
	return nil
}

func init() {
	modules.Register(&modules.Implementation{Kind: modules.KindNotify, Name: "recorder-test",
		Settings: func() any { return &struct{}{} },
		New:      func(modules.Deps, any) (modules.Instance, error) { return recModule{}, nil }})
}

func TestDigestAndFiltering(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mods := modules.NewManager(d, nil, log, t.TempDir())
	// one instance for all events, one only for failures and tag 99
	if err := mods.Create(ctx, &model.ProviderDefinition{Kind: "notify", Implementation: "recorder-test", Name: "all", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := mods.Create(ctx, &model.ProviderDefinition{Kind: "notify", Implementation: "recorder-test", Name: "tagged", Enabled: true,
		Events: []string{events.DownloadFailed}, Tags: []int64{99}}); err != nil {
		t.Fatal(err)
	}
	bus := events.NewBus()
	disp := New(d, bus, mods, settings.NewStore(d), log)
	disp.DigestQuiet = 100 * time.Millisecond
	_ = disp.Start(ctx)

	for _, n := range []string{"12", "10", "11"} {
		bus.Publish(events.Event{Type: events.ChapterImported, SeriesID: 1, Payload: events.ChapterImportedPayload{SeriesTitle: "One Piece", Chapter: n, NumberSort: map[string]float64{"10": 10, "11": 11, "12": 12}[n]}})
	}
	time.Sleep(400 * time.Millisecond)
	rec.mu.Lock()
	if len(rec.msgs) != 1 || rec.msgs[0].Title != "One Piece: 3 new chapters" || rec.msgs[0].Body != "Chapters 10–12" {
		t.Fatalf("digest: %+v", rec.msgs)
	}
	rec.msgs = nil
	rec.mu.Unlock()

	bus.Publish(events.Event{Type: events.DownloadFailed, SeriesID: 1, Payload: events.MessagePayload{Title: "Download failed", Message: "x"}})
	time.Sleep(200 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	// "tagged" wants the event but series 1 has no tag 99 → only "all" sends
	if len(rec.msgs) != 1 || rec.msgs[0].Title != "Download failed" {
		t.Fatalf("filtering: %+v", rec.msgs)
	}
}

func TestNewReleases(t *testing.T) {
	items := []events.ChapterImportedPayload{{Chapter: "1", Backlog: true}, {Chapter: "2"}, {Chapter: "3", Backlog: true}}
	if got := NewReleases(items); len(got) != 1 || got[0].Chapter != "2" {
		t.Fatalf("new releases = %+v", got)
	}
	if got := NewReleases(items[:1]); len(got) != 0 {
		t.Fatalf("a series catching up = %+v", got)
	}
}
