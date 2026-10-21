package catalogs

import (
	"context"
	"sort"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/sourcegov"
)

// statsEvery is how often request counts are written to the database.
const statsEvery = time.Minute

// statsKeep is how long hourly request counts are kept.
const statsKeep = 8 * 24 * time.Hour

// RunStats writes the request counts every minute until ctx ends.
func (s *Service) RunStats(ctx context.Context) {
	t := time.NewTicker(statsEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.FlushStats(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("store source request counts", "err", err)
			}
		}
	}
}

// FlushStats adds the request counts gathered since the last flush to the
// database and drops counts older than statsKeep.
func (s *Service) FlushStats(ctx context.Context) error {
	st := s.Gov.Stats()
	pending := st.Drain()
	if len(pending) > 0 {
		rows := make([]model.SourceStat, 0, len(pending))
		for hk, t := range pending {
			rows = append(rows, statRow(hk, t))
		}
		_, err := s.db.NewInsert().Model(&rows).
			On("CONFLICT (module_id, source_id, hour) DO UPDATE").
			Set("requests = ?TableAlias.requests + EXCLUDED.requests").
			Set("failures = ?TableAlias.failures + EXCLUDED.failures").
			Set("throttled = ?TableAlias.throttled + EXCLUDED.throttled").
			Set("fallbacks = ?TableAlias.fallbacks + EXCLUDED.fallbacks").
			Set("total_ms = ?TableAlias.total_ms + EXCLUDED.total_ms").
			Set("lat_0 = ?TableAlias.lat_0 + EXCLUDED.lat_0").
			Set("lat_1 = ?TableAlias.lat_1 + EXCLUDED.lat_1").
			Set("lat_2 = ?TableAlias.lat_2 + EXCLUDED.lat_2").
			Set("lat_3 = ?TableAlias.lat_3 + EXCLUDED.lat_3").
			Set("lat_4 = ?TableAlias.lat_4 + EXCLUDED.lat_4").
			Set("lat_5 = ?TableAlias.lat_5 + EXCLUDED.lat_5").
			Set("lat_6 = ?TableAlias.lat_6 + EXCLUDED.lat_6").
			Set("lat_7 = ?TableAlias.lat_7 + EXCLUDED.lat_7").
			Exec(ctx)
		if err != nil {
			st.Restore(pending)
			return err
		}
	}
	_, err := s.db.NewDelete().Model((*model.SourceStat)(nil)).Where("hour < ?", time.Now().UTC().Add(-statsKeep)).Exec(ctx)
	return err
}

func statRow(hk sourcegov.HourKey, t sourcegov.Tally) model.SourceStat {
	l := t.Latency
	return model.SourceStat{ModuleID: hk.Key.ModuleID, SourceID: hk.Key.SourceID, Hour: hk.Hour,
		Requests: t.Requests, Failures: t.Failures, Throttled: t.Throttled, Fallbacks: t.Fallbacks, TotalMs: t.TotalMs,
		Lat0: l[0], Lat1: l[1], Lat2: l[2], Lat3: l[3], Lat4: l[4], Lat5: l[5], Lat6: l[6], Lat7: l[7]}
}

func rowTally(r model.SourceStat) sourcegov.Tally {
	return sourcegov.Tally{Requests: r.Requests, Failures: r.Failures, Throttled: r.Throttled, Fallbacks: r.Fallbacks, TotalMs: r.TotalMs,
		Latency: sourcegov.Latencies{r.Lat0, r.Lat1, r.Lat2, r.Lat3, r.Lat4, r.Lat5, r.Lat6, r.Lat7}}
}

// ReliabilityPoint is one slice of a catalog's requests over time.
type ReliabilityPoint struct {
	At       time.Time `json:"at"`
	Requests int       `json:"requests"`
	Failures int       `json:"failures"`
}

// Reliability is how a catalog answered over a period.
type Reliability struct {
	ModuleID   int64  `json:"moduleId"`
	SourceID   string `json:"sourceId"`
	Name       string `json:"name"`
	Lang       string `json:"lang"`
	ModuleName string `json:"moduleName"`
	Enabled    bool   `json:"enabled"`
	Requests   int    `json:"requests"`
	Failures   int    `json:"failures"`
	Throttled  int    `json:"throttled"`
	Fallbacks  int    `json:"fallbacks"`
	MedianMs   int64  `json:"medianMs"`
	P90Ms      int64  `json:"p90Ms"`
	// Series links this catalog; FailingSeries of them failed 3+ checks in a row.
	Series         int                 `json:"series"`
	FailingSeries  int                 `json:"failingSeries"`
	CooldownUntil  *time.Time          `json:"cooldownUntil,omitempty"`
	CooldownReason string              `json:"cooldownReason,omitempty"`
	Points         []ReliabilityPoint  `json:"points"`
	RecentFailures []sourcegov.Failure `json:"recentFailures"`
}

// Failing reports whether the catalog answered too few requests to trust:
// at least 5 requests and under 80% of them worked.
func (r Reliability) Failing() bool {
	return r.Requests >= 5 && float64(r.Requests-r.Failures) < 0.8*float64(r.Requests)
}

// Reliability returns every catalog that was used since `since`, is linked
// to a series or is cooling down, busiest first. Points split the period in
// steps of `step`.
func (s *Service) Reliability(ctx context.Context, since time.Time, step time.Duration) ([]Reliability, error) {
	if err := s.FlushStats(ctx); err != nil {
		return nil, err
	}
	since = since.UTC().Truncate(time.Hour)
	var rows []model.SourceStat
	if err := s.db.NewSelect().Model(&rows).Where("hour >= ?", since).Scan(ctx); err != nil {
		return nil, err
	}
	var links []struct {
		ModuleID int64  `bun:"module_id"`
		SourceID string `bun:"source_id"`
		Name     string `bun:"name"`
		Series   int    `bun:"series"`
		Failing  int    `bun:"failing"`
	}
	if err := s.db.NewSelect().TableExpr("series_sources").
		ColumnExpr("module_id, source_id, MAX(source_name) AS name, COUNT(DISTINCT series_id) AS series").
		ColumnExpr("COUNT(DISTINCT CASE WHEN enabled AND consecutive_failures >= 3 THEN series_id END) AS failing").
		Group("module_id", "source_id").Scan(ctx, &links); err != nil {
		return nil, err
	}

	steps := max(1, int(time.Since(since)/step)+1)
	by := map[sourcegov.Key]*Reliability{}
	tallies := map[sourcegov.Key]*sourcegov.Tally{}
	get := func(k sourcegov.Key) *Reliability {
		r, ok := by[k]
		if !ok {
			r = &Reliability{ModuleID: k.ModuleID, SourceID: k.SourceID, Name: k.SourceID, Enabled: true, Points: make([]ReliabilityPoint, steps)}
			for i := range r.Points {
				r.Points[i].At = since.Add(time.Duration(i) * step)
			}
			by[k], tallies[k] = r, &sourcegov.Tally{}
		}
		return r
	}
	for _, row := range rows {
		k := sourcegov.Key{ModuleID: row.ModuleID, SourceID: row.SourceID}
		r := get(k)
		tallies[k].Add(rowTally(row))
		if i := int(row.Hour.Sub(since) / step); i >= 0 && i < steps {
			r.Points[i].Requests += row.Requests
			r.Points[i].Failures += row.Failures
		}
	}
	for _, l := range links {
		r := get(sourcegov.Key{ModuleID: l.ModuleID, SourceID: l.SourceID})
		r.Series, r.FailingSeries = l.Series, l.Failing
		if l.Name != "" {
			r.Name = l.Name // until the catalog list says better
		}
	}
	for _, k := range s.Gov.CoolingDown() {
		get(k)
	}
	list, _ := s.List(ctx, false)
	known := map[sourcegov.Key]Catalog{}
	for _, c := range list {
		known[sourcegov.Key{ModuleID: c.ModuleID, SourceID: c.ID}] = c
	}
	out := make([]Reliability, 0, len(by))
	for k, r := range by {
		t := tallies[k]
		r.Requests, r.Failures, r.Throttled, r.Fallbacks = t.Requests, t.Failures, t.Throttled, t.Fallbacks
		r.MedianMs, r.P90Ms = t.Latency.Percentile(0.5), t.Latency.Percentile(0.9)
		if c, ok := known[k]; ok {
			r.Lang, r.ModuleName, r.Enabled = c.Lang, c.ModuleName, c.Enabled
			if r.Name = c.DisplayName; r.Name == "" {
				r.Name = c.Name
			}
		}
		if until, reason := s.Gov.Cooldown(k); !until.IsZero() {
			r.CooldownUntil, r.CooldownReason = &until, reason
		}
		r.RecentFailures = s.Gov.Stats().Failures(k)
		if r.RecentFailures == nil {
			r.RecentFailures = []sourcegov.Failure{}
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Requests != out[j].Requests {
			return out[i].Requests > out[j].Requests
		}
		if out[i].Series != out[j].Series {
			return out[i].Series > out[j].Series
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
