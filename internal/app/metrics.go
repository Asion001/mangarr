package app

import (
	"context"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/metrics"
	"github.com/Asion001/mangarr/internal/model"
)

// metricsEvery is how often the gauges on the performance page refresh.
const metricsEvery = 30 * time.Second

// sampleMetrics refreshes the gauges (memory, database pool, download
// queue) until ctx ends.
func (a *App) sampleMetrics(ctx context.Context) {
	a.sampleGauges(ctx)
	t := time.NewTicker(metricsEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sampleGauges(ctx)
		}
	}
}

func (a *App) sampleGauges(ctx context.Context) {
	g := metrics.Gauges{At: time.Now().UTC()}
	metrics.RuntimeGauges(&g)
	st := a.DB.Stats()
	g.DBOpen, g.DBInUse, g.DBMaxOpen, g.DBWaitCount = st.OpenConnections, st.InUse, st.MaxOpenConnections, st.WaitCount
	g.DBWaitMs = float64(st.WaitDuration.Microseconds()) / 1000
	var rows []struct {
		Status string `bun:"status"`
		N      int    `bun:"n"`
	}
	if err := a.DB.NewSelect().Model((*model.DownloadJob)(nil)).Column("status").ColumnExpr("count(*) AS n").
		Where("status NOT IN (?)", bun.In([]string{model.JobCompleted, model.JobFailed})).Group("status").Scan(ctx, &rows); err == nil {
		for _, r := range rows {
			switch r.Status {
			case model.JobQueued, model.JobPaused:
				g.QueueWaiting += r.N
			default:
				g.QueueRunning += r.N
			}
		}
	}
	a.Metrics.SetGauges(g)
}
