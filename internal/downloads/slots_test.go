package downloads

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcegov"
)

type workersProcessor struct {
	Processor
	on bool
}

func (p workersProcessor) OnWorkers(context.Context) bool { return p.on }

// TestPagesInFreesDownloadSlots: a chapter whose pages are in and go to the
// workers stops holding its download slots and this server's own slot, so
// the next chapter of the same source downloads meanwhile. Processed here,
// it keeps them.
func TestPagesInFreesDownloadSlots(t *testing.T) {
	for _, on := range []bool{true, false} {
		m := &Manager{queue: NewQueue(nil, nil), running: map[int64]context.CancelFunc{}, runningKind: map[int64]string{},
			runningSrc: map[string]int{}, slots: map[int64]*jobSlots{}, Processor: workersProcessor{on: on}}
		m.mu.Lock()
		slots := m.hold(1, model.JobKindDownload, "6:src", func() {})
		m.mu.Unlock()
		m.local++
		slots.local = m.releaseLocal

		m.pagesIn(t.Context(), 1)
		m.mu.Lock()
		free := m.runningSrc["6:src"] == 0 && m.runningOfKind(model.JobKindDownload) == 0 && m.local == 0
		processing := m.runningOfKind(stageProcessing)
		m.mu.Unlock()
		if on && (!free || processing != 1) {
			t.Fatalf("on workers: slots still held (src %d, local %d, processing %d)", m.runningSrc["6:src"], m.local, processing)
		}
		if !on && (free || processing != 0) {
			t.Fatal("processed here: the chapter must keep its slots")
		}

		m.pagesIn(t.Context(), 1) // twice frees nothing more
		m.drop(1, slots)
		if m.runningSrc["6:src"] != 0 || m.local != 0 || len(m.running) != 0 || len(m.slots) != 0 {
			t.Fatalf("after the job: src %d, local %d, running %d", m.runningSrc["6:src"], m.local, len(m.running))
		}
	}
}

// TestBlockedSources: catalogs at their limit or cooling down are left out
// of the dispatch window, so their backlog can't hide other catalogs' jobs.
func TestBlockedSources(t *testing.T) {
	m := &Manager{runningSrc: map[string]int{"6:full": 2, "6:free": 1, "": 5}}
	got := m.blockedSources(2)
	if len(got) != 1 || got[0] != (sourcegov.Key{ModuleID: 6, SourceID: "full"}) {
		t.Fatalf("blocked = %v", got)
	}
	gov := sourcegov.New(func(sourcegov.Key) model.ThrottleConfig { return model.ThrottleConfig{} }, nil)
	gov.Restore(sourcegov.Key{ModuleID: 7, SourceID: "slow"}, time.Now().Add(time.Hour), 1, "429")
	m.Gov = gov
	if got := m.blockedSources(2); len(got) != 2 {
		t.Fatalf("cooling catalog not blocked: %v", got)
	}
}

// TestDispatchQueryWithBlockedSources: the window query with catalogs left
// out runs on both databases.
func TestDispatchQueryWithBlockedSources(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		q, _ := rankFixture(t, d, 1)
		var logs bytes.Buffer
		m := NewManager(d, q.bus, nil, settings.NewStore(d), nil, q, nil, slog.New(slog.NewTextHandler(&logs, nil)), t.TempDir())
		m.runningSrc["6:full"] = 5
		m.held = true // read the window, start nothing
		m.dispatch(t.Context())
		if strings.Contains(logs.String(), "download queue") {
			t.Fatalf("window query failed: %s", logs.String())
		}
	})
}
