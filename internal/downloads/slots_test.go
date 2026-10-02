package downloads

import (
	"context"
	"testing"

	"github.com/Asion001/mangarr/internal/model"
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
