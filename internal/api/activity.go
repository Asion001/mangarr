package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/downloads"
	"github.com/Asion001/mangarr/internal/history"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/quiet"
	"github.com/Asion001/mangarr/internal/settings"
)

func init() { register((*Server).registerActivity) }

type WantedItem struct {
	model.Chapter
	SeriesTitle string `json:"seriesTitle"`
	Releases    int    `json:"releases"`
}

type WantedPage struct {
	Items    []WantedItem `json:"items"`
	Total    int          `json:"total"`
	Page     int          `json:"page"`
	PageSize int          `json:"pageSize"`
}

type BlocklistView struct {
	model.Blocklist
	SeriesTitle string `json:"seriesTitle"`
	SourceName  string `json:"sourceName"`
}

// QueueState is the queue's global pause plus the quiet-hour effects in force.
type QueueState struct {
	Paused      bool          `json:"paused"`
	PausedUntil *time.Time    `json:"pausedUntil,omitempty"`
	Quiet       quiet.Effects `json:"quiet"`
}

type QueueResponse struct {
	downloads.QueuePage
	State QueueState `json:"state"`
}

type QueueBulkInput struct {
	IDs      []int64               `json:"ids,omitempty"`
	Filter   *downloads.ListFilter `json:"filter,omitempty" doc:"Select every entry matching this filter instead of ids"`
	Action   string                `json:"action" enum:"pause,resume,retry,remove,blocklist,top,bottom,before,after,sort"`
	AnchorID int64                 `json:"anchorId,omitempty" doc:"Pending job to move before/after; must not be selected"`
}

func (s *Server) queueState(ctx context.Context) QueueState {
	qs, _ := s.app.Settings.QueueState(ctx)
	sched, _ := s.app.Settings.Schedule(ctx)
	st := QueueState{Paused: qs.Active(time.Now()), Quiet: quiet.Evaluate(sched, time.Now())}
	if st.Paused {
		st.PausedUntil = qs.PausedUntil
	}
	return st
}

func (s *Server) registerActivity() {
	tags := []string{"Activity"}
	huma.Register(s.api, huma.Operation{OperationID: "queue-list", Method: http.MethodGet, Path: "/api/v1/queue", Tags: tags,
		Summary: "Queue entries: running first, then by durable rank; filter by status, kind, series or title"},
		func(ctx context.Context, in *struct {
			Status      []string `query:"status,explode" doc:"Statuses (repeat the parameter or separate with commas); empty = active (plus recent when includeDone)"`
			Kind        string   `query:"kind" enum:",download,reprocess"`
			SeriesID    int64    `query:"seriesId"`
			Query       string   `query:"q"`
			IncludeDone bool     `query:"includeDone"`
			Page        int      `query:"page" default:"1"`
			PageSize    int      `query:"pageSize" default:"100"`
			Revision    int64    `query:"revision" default:"-1" minimum:"-1" doc:"Rank revision from the first page; a changed rank order returns 409; -1 disables the check"`
		}) (*struct{ Body QueueResponse }, error) {
			var revision *int64
			if in.Revision >= 0 {
				revision = &in.Revision
			}
			p, err := s.app.DLQueue.ListPageAt(ctx, downloads.ListFilter{Statuses: splitList(in.Status), Kind: in.Kind, SeriesID: in.SeriesID, Query: in.Query, IncludeDone: in.IncludeDone}, in.Page, in.PageSize, revision)
			if errors.Is(err, downloads.ErrQueueOrderChanged) {
				return nil, huma.Error409Conflict(err.Error())
			}
			if err != nil {
				return nil, toHTTPError(err)
			}
			for i := range p.Items {
				if lp, ok := s.app.Downloads.Live.Get(p.Items[i].ID); ok {
					p.Items[i].Live = &lp
				}
			}
			s.app.NameWorkers(ctx, p.Items)
			return &struct{ Body QueueResponse }{QueueResponse{QueuePage: *p, State: s.queueState(ctx)}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "queue-bulk", Method: http.MethodPost, Path: "/api/v1/queue/bulk", Tags: tags,
		Summary: "Apply an action to selected entries (ids) or to every entry matching a filter"},
		func(ctx context.Context, in *struct{ Body QueueBulkInput }) (*struct {
			Body struct {
				Affected int `json:"affected"`
			}
		}, error) {
			ids := in.Body.IDs
			if in.Body.Filter != nil {
				var err error
				if ids, err = s.app.DLQueue.IDs(ctx, *in.Body.Filter); err != nil {
					return nil, toHTTPError(err)
				}
			}
			var n int
			var err error
			switch in.Body.Action {
			case "top", "bottom", "before", "after":
				n, err = s.app.DLQueue.Move(ctx, ids, in.Body.Action, in.Body.AnchorID)
			case "sort":
				n, err = s.app.DLQueue.SortByChapter(ctx, ids)
			default:
				n, err = s.app.Downloads.Bulk(ctx, ids, in.Body.Action)
			}
			if err != nil {
				return nil, huma.Error400BadRequest(err.Error())
			}
			out := &struct {
				Body struct {
					Affected int `json:"affected"`
				}
			}{}
			out.Body.Affected = n
			return out, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "queue-pause", Method: http.MethodPost, Path: "/api/v1/queue/pause", Tags: tags,
		Summary: "Pause the whole queue (optionally for some minutes)"},
		func(ctx context.Context, in *struct {
			Body struct {
				Minutes int `json:"minutes,omitempty" doc:"0 = until resumed"`
			}
		}) (*struct{ Body QueueState }, error) {
			st := settings.QueueState{Paused: true}
			if in.Body.Minutes > 0 {
				until := time.Now().UTC().Add(time.Duration(in.Body.Minutes) * time.Minute)
				st.PausedUntil = &until
			}
			if err := s.app.Settings.Set(ctx, settings.KeyQueueState, st); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("queue", "state", 0)
			return &struct{ Body QueueState }{s.queueState(ctx)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "queue-resume", Method: http.MethodPost, Path: "/api/v1/queue/resume", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{ Body QueueState }, error) {
			if err := s.app.Settings.Set(ctx, settings.KeyQueueState, settings.QueueState{}); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.DLQueue.Wake()
			s.app.Bus.Changed("queue", "state", 0)
			return &struct{ Body QueueState }{s.queueState(ctx)}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "queue-remove", Method: http.MethodDelete, Path: "/api/v1/queue/{id}", Tags: tags},
		func(ctx context.Context, in *struct {
			ID        int64 `path:"id"`
			Blocklist bool  `query:"blocklist"`
		}) (*struct{}, error) {
			if s.app.Downloads != nil {
				s.app.Downloads.Cancel(in.ID)
			}
			err := s.app.DLQueue.Remove(ctx, in.ID, in.Blocklist)
			s.app.Bus.Changed("queue", "deleted", in.ID)
			return nil, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "queue-retry", Method: http.MethodPost, Path: "/api/v1/queue/{id}/retry", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			return nil, toHTTPError(s.app.DLQueue.Retry(ctx, in.ID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "queue-clear", Method: http.MethodPost, Path: "/api/v1/queue/clear-finished", Tags: tags},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			err := s.app.DLQueue.ClearFinished(ctx, 0)
			s.app.Bus.Changed("queue", "sync", 0)
			return nil, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "history-list", Method: http.MethodGet, Path: "/api/v1/history", Tags: tags},
		func(ctx context.Context, in *struct {
			SeriesID  int64  `query:"seriesId"`
			ChapterID int64  `query:"chapterId"`
			EventType string `query:"eventType"`
			Sort      string `query:"sort" default:"newest" enum:"newest,oldest,series,event"`
			Page      int    `query:"page" default:"1"`
			PageSize  int    `query:"pageSize" default:"50"`
		}) (*struct{ Body *history.Page }, error) {
			p, err := history.List(ctx, s.app.DB, history.Query{SeriesID: in.SeriesID, ChapterID: in.ChapterID, EventType: in.EventType, Sort: in.Sort, Page: in.Page, PageSize: in.PageSize})
			return &struct{ Body *history.Page }{p}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "blocklist-list", Method: http.MethodGet, Path: "/api/v1/blocklist", Tags: tags},
		func(ctx context.Context, in *struct {
			SeriesID int64 `query:"seriesId"`
		}) (*struct{ Body []BlocklistView }, error) {
			var out []BlocklistView
			q := s.app.DB.NewSelect().TableExpr("blocklist AS b").ColumnExpr("b.*, s.title AS series_title, COALESCE(ss.source_name, '') AS source_name").
				Join("JOIN series AS s ON s.id = b.series_id").Join("LEFT JOIN series_sources AS ss ON ss.id = b.series_source_id")
			if in.SeriesID > 0 {
				q = q.Where("b.series_id = ?", in.SeriesID)
			}
			err := q.OrderExpr("b.id DESC").Limit(1000).Scan(ctx, &out)
			if out == nil {
				out = []BlocklistView{}
			}
			return &struct{ Body []BlocklistView }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "blocklist-delete", Method: http.MethodDelete, Path: "/api/v1/blocklist/{id}", Tags: tags},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			_, err := s.app.DB.NewDelete().Model((*model.Blocklist)(nil)).Where("id = ?", in.ID).Exec(ctx)
			s.app.Bus.Changed("blocklist", "deleted", in.ID)
			return nil, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "wanted-missing", Method: http.MethodGet, Path: "/api/v1/wanted/missing", Tags: tags,
		Summary: "Monitored chapters of monitored series without a file"},
		func(ctx context.Context, in *struct {
			Page     int `query:"page" default:"1"`
			PageSize int `query:"pageSize" default:"50"`
		}) (*struct{ Body WantedPage }, error) {
			if in.Page < 1 {
				in.Page = 1
			}
			if in.PageSize <= 0 || in.PageSize > 500 {
				in.PageSize = 50
			}
			var items []WantedItem
			q := s.app.DB.NewSelect().TableExpr("chapters AS c").
				ColumnExpr("c.*, s.title AS series_title").
				ColumnExpr("(SELECT COUNT(*) FROM chapter_releases r WHERE r.chapter_id = c.id AND r.removed = ?) AS releases", false).
				Join("JOIN series AS s ON s.id = c.series_id").
				Where("s.monitored = ? AND c.monitored = ? AND c.file_id IS NULL AND c.state <> ?", true, true, model.ChapterCleaned)
			total, err := q.Count(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			err = q.OrderExpr("c.release_date DESC NULLS LAST, c.id DESC").Limit(in.PageSize).Offset((in.Page-1)*in.PageSize).Scan(ctx, &items)
			if items == nil {
				items = []WantedItem{}
			}
			return &struct{ Body WantedPage }{WantedPage{Items: items, Total: total, Page: in.Page, PageSize: in.PageSize}}, toHTTPError(err)
		})

	huma.Register(s.api, huma.Operation{OperationID: "calendar", Method: http.MethodGet, Path: "/api/v1/calendar", Tags: tags,
		Summary: "Chapters released in a date range (by source upload date)"},
		func(ctx context.Context, in *struct {
			Start time.Time `query:"start"`
			End   time.Time `query:"end"`
		}) (*struct{ Body []WantedItem }, error) {
			if in.End.IsZero() {
				in.End = time.Now().UTC()
			}
			if in.Start.IsZero() {
				in.Start = in.End.Add(-14 * 24 * time.Hour)
			}
			var items []WantedItem
			err := s.app.DB.NewSelect().TableExpr("chapters AS c").ColumnExpr("c.*, s.title AS series_title").
				Join("JOIN series AS s ON s.id = c.series_id").
				Where("s.preview = ?", false).
				Where("c.release_date >= ? AND c.release_date <= ?", in.Start.UTC(), in.End.UTC()).
				OrderExpr("c.release_date DESC").Limit(1000).Scan(ctx, &items)
			if items == nil {
				items = []WantedItem{}
			}
			return &struct{ Body []WantedItem }{items}, toHTTPError(err)
		})
}
