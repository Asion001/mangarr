package reading

import (
	"context"
	"sort"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/model"
)

const (
	// eventMergeWindow folds a device's page turns in one chapter into its
	// last event.
	eventMergeWindow = 10 * time.Minute
	// EventRetention is how long read events are kept.
	EventRetention = 30 * 24 * time.Hour
)

// Observe logs progress changes made elsewhere (a library server's sync, an
// import) and announces them like Record does, so reading apps and the
// other servers hear about them.
func (s *Service) Observe(ctx context.Context, readerID int64, out []Outcome, by By) {
	if len(out) == 0 {
		return
	}
	s.inReadingOrder(ctx, out)
	s.logOutcomes(ctx, readerID, out, by, time.Now().UTC())
	s.announce(readerID, out, by)
	for sid := range s.announceEditions(ctx, readerID, out) {
		s.Bus.Changed("series", "updated", sid)
	}
}

// inReadingOrder sorts outcomes by chapter number.
func (s *Service) inReadingOrder(ctx context.Context, out []Outcome) {
	ids := map[int64]bool{}
	for _, o := range out {
		ids[o.ChapterID] = true
	}
	var chs []model.Chapter
	_ = s.DB.NewSelect().Model(&chs).Column("id", "number_sort").Where("id IN (?)", bun.In(keys(ids))).Scan(ctx)
	num := map[int64]float64{}
	for _, c := range chs {
		num[c.ID] = c.NumberSort
	}
	sort.SliceStable(out, func(i, j int) bool { return num[out[i].ChapterID] < num[out[j].ChapterID] })
}

// announce publishes ProgressChanged per series for applied changes and unreads.
func (s *Service) announce(readerID int64, out []Outcome, by By) {
	type key struct {
		series int64
		unread bool
	}
	chapters := map[key][]int64{}
	var order []key
	for _, o := range out {
		if o.Result != model.OutcomeApplied && o.Result != model.OutcomeUnread {
			continue
		}
		k := key{o.SeriesID, o.Result == model.OutcomeUnread}
		if _, ok := chapters[k]; !ok {
			order = append(order, k)
		}
		chapters[k] = append(chapters[k], o.ChapterID)
	}
	for _, k := range order {
		s.Bus.Publish(events.Event{Type: ProgressChanged, SeriesID: k.series,
			Payload: ProgressPayload{ReaderID: readerID, ChapterIDs: chapters[k], Deleted: k.unread, Origin: by.Origin}})
	}
}

// logOutcomes writes read events: one per series, outcome and state (a
// series marked read is one event), except that a device's page turns in
// one chapter update its last event.
func (s *Service) logOutcomes(ctx context.Context, readerID int64, out []Outcome, by By, now time.Time) {
	type key struct {
		series    int64
		result    string
		completed bool
	}
	groups := map[key][]Outcome{}
	var order []key
	for _, o := range out {
		if o.Result == model.OutcomeUnchanged {
			continue
		}
		k := key{o.SeriesID, o.Result, o.Completed && o.Result != model.OutcomeUnread}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], o)
	}
	for _, k := range order {
		g := groups[k]
		last := g[len(g)-1] // changes come in reading order
		ev := &model.ReadEvent{ReaderID: readerID, SeriesID: k.series, ChapterID: last.ChapterID, Chapters: len(g), Completed: k.completed,
			Page: last.Page, Origin: by.Origin, Client: by.Client, Device: by.Device, Outcome: k.result, At: now}
		if (k.result == model.OutcomeKept || len(g) == 1 && k.result == model.OutcomeApplied) && s.mergeEvent(ctx, ev) {
			continue
		}
		if _, err := s.DB.NewInsert().Model(ev).Exec(ctx); err != nil {
			s.Log.Warn("read event not saved", "error", err)
		}
	}
}

// mergeEvent folds ev into the same reporter's last event for its chapter:
// page turns within eventMergeWindow, and the same lower report a server
// sends on every sync (once a day stays one "kept" event).
func (s *Service) mergeEvent(ctx context.Context, ev *model.ReadEvent) bool {
	var last model.ReadEvent
	if err := s.DB.NewSelect().Model(&last).Where("reader_id = ? AND chapter_id = ?", ev.ReaderID, ev.ChapterID).
		OrderExpr("id DESC").Limit(1).Scan(ctx); err != nil {
		return false
	}
	if last.Origin != ev.Origin || last.Client != ev.Client || last.Device != ev.Device || last.Outcome != ev.Outcome {
		return false
	}
	switch ev.Outcome {
	case model.OutcomeKept:
		if ev.At.Sub(last.At) > 24*time.Hour {
			return false
		}
	case model.OutcomeApplied:
		if last.Completed || last.Chapters != 1 || ev.At.Sub(last.At) > eventMergeWindow {
			return false
		}
	default:
		return false
	}
	last.Page, last.Completed, last.Chapters, last.At = ev.Page, ev.Completed, ev.Chapters, ev.At
	_, err := s.DB.NewUpdate().Model(&last).Column("page", "completed", "chapters", "at").WherePK().Exec(ctx)
	return err == nil
}

// PruneEvents deletes read events older than EventRetention.
func (s *Service) PruneEvents(ctx context.Context) (int64, error) {
	res, err := s.DB.NewDelete().Model((*model.ReadEvent)(nil)).Where("at < ?", time.Now().UTC().Add(-EventRetention)).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// EventView is a read event with what it's about.
type EventView struct {
	model.ReadEvent
	SeriesTitle string `json:"seriesTitle"`
	// Chapter is the chapter's number ("12.5").
	Chapter string `json:"chapter"`
}

// DeviceSync summarizes what one app, device or server reported.
type DeviceSync struct {
	Origin   string    `json:"origin"`
	Client   string    `json:"client"`
	Device   string    `json:"device"`
	LastSeen time.Time `json:"lastSeen"`
	// Events and Kept count reports in the last 30 days; Kept ones were
	// lower than mangarr's progress, so mangarr kept its own.
	Events int        `json:"events"`
	Kept   int        `json:"kept"`
	Last   *EventView `json:"last,omitempty"`
}

// SyncHealth is a reader's sync health: who reports progress, and the
// latest reports.
type SyncHealth struct {
	Devices []DeviceSync `json:"devices"`
	Events  []EventView  `json:"events"`
}

// SyncHealth summarizes readerID's recent progress reports.
func (s *Service) SyncHealth(ctx context.Context, readerID int64, limit int) (SyncHealth, error) {
	var groups []struct {
		Origin   string       `bun:"origin"`
		Client   string       `bun:"client"`
		Device   string       `bun:"device"`
		LastSeen bun.NullTime `bun:"last_seen"`
		Events   int          `bun:"events"`
		Kept     int          `bun:"kept"`
	}
	if err := s.DB.NewSelect().Model((*model.ReadEvent)(nil)).
		ColumnExpr("origin, client, device, MAX(at) AS last_seen, COUNT(*) AS events").
		ColumnExpr("SUM(CASE WHEN outcome = ? THEN 1 ELSE 0 END) AS kept", model.OutcomeKept).
		Where("reader_id = ?", readerID).GroupExpr("origin, client, device").Scan(ctx, &groups); err != nil {
		return SyncHealth{}, err
	}
	recent, err := s.recentEvents(ctx, readerID, max(limit, 200))
	if err != nil {
		return SyncHealth{}, err
	}
	out := SyncHealth{Devices: make([]DeviceSync, 0, len(groups)), Events: recent[:min(limit, len(recent))]}
	for _, g := range groups {
		d := DeviceSync{Origin: g.Origin, Client: g.Client, Device: g.Device, LastSeen: g.LastSeen.Time.UTC(), Events: g.Events, Kept: g.Kept}
		for i := range recent {
			if e := &recent[i]; e.Origin == g.Origin && e.Client == g.Client && e.Device == g.Device {
				d.Last = e
				break
			}
		}
		out.Devices = append(out.Devices, d)
	}
	sort.Slice(out.Devices, func(i, j int) bool { return out.Devices[i].LastSeen.After(out.Devices[j].LastSeen) })
	return out, nil
}

func (s *Service) recentEvents(ctx context.Context, readerID int64, limit int) ([]EventView, error) {
	var evs []model.ReadEvent
	if err := s.DB.NewSelect().Model(&evs).Where("reader_id = ?", readerID).OrderExpr("at DESC, id DESC").Limit(limit).Scan(ctx); err != nil {
		return nil, err
	}
	seriesIDs, chapterIDs := map[int64]bool{}, map[int64]bool{}
	for _, e := range evs {
		seriesIDs[e.SeriesID], chapterIDs[e.ChapterID] = true, true
	}
	titles, numbers := map[int64]string{}, map[int64]string{}
	if len(evs) > 0 {
		var sers []model.Series
		_ = s.DB.NewSelect().Model(&sers).Column("id", "title").Where("id IN (?)", bun.In(keys(seriesIDs))).Scan(ctx)
		for _, x := range sers {
			titles[x.ID] = x.Title
		}
		var chs []model.Chapter
		_ = s.DB.NewSelect().Model(&chs).Column("id", "number_key").Where("id IN (?)", bun.In(keys(chapterIDs))).Scan(ctx)
		for _, c := range chs {
			numbers[c.ID] = c.NumberKey
		}
	}
	out := make([]EventView, len(evs))
	for i, e := range evs {
		e.At = e.At.UTC()
		out[i] = EventView{ReadEvent: e, SeriesTitle: titles[e.SeriesID], Chapter: numbers[e.ChapterID]}
	}
	return out, nil
}

func keys(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
