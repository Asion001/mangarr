package api

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
)

func init() { register((*Server).registerUpdatesSeen) }

// seenItemsKeep is how long single items marked seen are remembered (the
// feed reaches back a year at most).
const seenItemsKeep = 400 * 24 * time.Hour

// markAllLimit caps how many items one filtered "mark all seen" marks.
const markAllLimit = 5000

// UpdateRef names one update: a chapter by its id, a new title by the id
// the feed gives it (seriesId).
type UpdateRef struct {
	Kind string    `json:"kind" enum:"series,chapter"`
	ID   int64     `json:"id"`
	At   time.Time `json:"at"`
}

// UpdatesSeenInput marks updates seen: All marks every update matching the
// filters, Items the ones named.
type UpdatesSeenInput struct {
	All          bool        `json:"all,omitempty"`
	Days         int         `json:"days,omitempty" minimum:"0" maximum:"365"`
	Kind         string      `json:"kind,omitempty" enum:",all,chapter,series"`
	Availability string      `json:"availability,omitempty" enum:",downloaded,streamable"`
	Language     string      `json:"language,omitempty"`
	Followed     bool        `json:"followed,omitempty"`
	SeriesID     int64       `json:"seriesId,omitempty"`
	Items        []UpdateRef `json:"items,omitempty"`
}

// UpdatesUnseen is how many updates you haven't seen.
type UpdatesUnseen struct {
	Count int `json:"count"`
}

// seenUser is whose seen cursor the caller moves: a signed-in user's own,
// or the install's (0) without a login.
func seenUser(p *access.Principal) int64 {
	if p != nil && p.Kind == access.KindUser {
		return p.UserID
	}
	return 0
}

// updateSeen returns the caller's seen cursor, starting it at now the first
// time: what came before isn't news to someone who just started.
func (s *Server) updateSeen(ctx context.Context, viewer *access.Principal) (*model.UpdateSeen, error) {
	seen := &model.UpdateSeen{UserID: seenUser(viewer)}
	err := s.app.DB.NewSelect().Model(seen).WherePK().Scan(ctx)
	if err == nil {
		return seen, nil
	}
	now := time.Now().UTC()
	seen.SeenAt, seen.SeenRank, seen.SeenID, seen.UpdatedAt = now, chapterUpdateRank, math.MaxInt64, now
	if _, err := s.app.DB.NewInsert().Model(seen).On("CONFLICT (user_id) DO NOTHING").Exec(ctx); err != nil {
		return nil, err
	}
	return seen, s.app.DB.NewSelect().Model(seen).WherePK().Scan(ctx)
}

// seenThrough reports whether row is at or before the cursor.
func seenThrough(seen *model.UpdateSeen, row updateFeedRow) bool {
	if seen == nil {
		return false
	}
	if !row.At.Equal(seen.SeenAt) {
		return row.At.Before(seen.SeenAt)
	}
	if r := updateRank(row.Kind); r != seen.SeenRank {
		return r < seen.SeenRank
	}
	return row.SortID <= seen.SeenID
}

// seenRef is the id a row is marked seen under: a chapter's, or for a new
// title its group (seriesUpdateGroup), which stays the same whichever of
// its editions a language or follow filter leaves to stand for it.
func seenRef(row updateFeedRow) int64 {
	if row.Kind == "series" {
		return row.WorkKey
	}
	return row.SortID
}

// seenItems are the rows marked seen one by one, keyed "kind:id".
func (s *Server) seenItems(ctx context.Context, seen *model.UpdateSeen, rows []updateFeedRow) (map[string]bool, error) {
	out := map[string]bool{}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		if !seenThrough(seen, r) {
			ids = append(ids, seenRef(r))
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	var found []model.UpdateSeenItem
	if err := s.app.DB.NewSelect().Model(&found).Where("user_id = ?", seen.UserID).Where("item_id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, err
	}
	for _, it := range found {
		out[it.Kind+":"+strconv.FormatInt(it.ItemID, 10)] = true
	}
	return out, nil
}

func (s *Server) registerUpdatesSeen() {
	tags := []string{"Updates"}
	huma.Register(s.api, huma.Operation{OperationID: "updates-unseen", Method: http.MethodGet, Path: "/api/v1/updates/unseen", Tags: tags,
		Summary: "How many updates of the last days you haven't seen"},
		func(ctx context.Context, in *struct {
			Days int `query:"days" default:"30" minimum:"1" maximum:"365"`
		}) (*struct{ Body UpdatesUnseen }, error) {
			viewer := access.From(ctx)
			seen, err := s.updateSeen(ctx, viewer)
			if err != nil {
				return nil, toHTTPError(err)
			}
			n, err := s.countUpdates(ctx, viewer, updateFilter{Days: max(in.Days, 1), Unseen: true}, seen)
			return &struct{ Body UpdatesUnseen }{UpdatesUnseen{n}}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "updates-seen", Method: http.MethodPost, Path: "/api/v1/updates/seen", Tags: tags,
		Summary: "Mark updates seen (every one matching the filters, or the ones named); reading progress doesn't change"},
		func(ctx context.Context, in *struct{ Body UpdatesSeenInput }) (*struct{ Body UpdatesUnseen }, error) {
			viewer := access.From(ctx)
			b := in.Body
			if b.Days <= 0 {
				b.Days = 30
			}
			if b.Kind == "all" {
				b.Kind = ""
			}
			f := updateFilter{Days: b.Days, Kind: b.Kind, Availability: b.Availability, Language: b.Language, Followed: b.Followed, SeriesID: b.SeriesID, Unseen: true}
			seen, err := s.updateSeen(ctx, viewer)
			if err != nil {
				return nil, toHTTPError(err)
			}
			if err := s.markSeen(ctx, viewer, seen, f, b); err != nil {
				return nil, toHTTPError(err)
			}
			if seen, err = s.updateSeen(ctx, viewer); err != nil {
				return nil, toHTTPError(err)
			}
			n, err := s.countUpdates(ctx, viewer, updateFilter{Days: 30, Unseen: true}, seen)
			s.app.Bus.Changed("updates", "seen", seenUser(viewer))
			return &struct{ Body UpdatesUnseen }{UpdatesUnseen{n}}, toHTTPError(err)
		})
}

func (s *Server) markSeen(ctx context.Context, viewer *access.Principal, seen *model.UpdateSeen, f updateFilter, b UpdatesSeenInput) error {
	now := time.Now().UTC()
	var refs []UpdateRef
	switch {
	case b.All && !f.filtered() && f.Kind == "" && b.Days >= 30:
		// everything (a shorter period marks only what it shows, below):
		// move the cursor to the newest update there is
		page, err := s.listUpdates(ctx, viewer, updateFilter{Days: 365, Unseen: true}, 1, 1, nil)
		if err != nil || len(page.Items) == 0 {
			return err
		}
		top := page.Items[0]
		id := top.ChapterID
		if top.Kind == "series" {
			id = top.SeriesID
		}
		seen.SeenAt, seen.SeenRank, seen.SeenID, seen.UpdatedAt = top.At, updateRank(top.Kind), id, now
		if _, err := s.app.DB.NewUpdate().Model(seen).WherePK().Exec(ctx); err != nil {
			return err
		}
		_, err = s.app.DB.NewDelete().Model((*model.UpdateSeenItem)(nil)).Where("user_id = ?", seen.UserID).
			Where("at <= ? OR at < ?", top.At, now.Add(-seenItemsKeep)).Exec(ctx)
		return err
	case b.All:
		// only what the filters show: mark those one by one
		page, err := s.listUpdates(ctx, viewer, f, 1, markAllLimit, nil)
		if err != nil {
			return err
		}
		for _, it := range page.Items {
			id := it.ChapterID
			if it.Kind == "series" {
				id = it.SeriesID
			}
			refs = append(refs, UpdateRef{Kind: it.Kind, ID: id, At: it.At})
		}
	default:
		refs = b.Items
	}
	if len(refs) == 0 {
		return nil
	}
	groups, err := s.seriesGroups(ctx, refs)
	if err != nil {
		return err
	}
	rows := make([]model.UpdateSeenItem, 0, len(refs))
	for _, r := range refs {
		if r.Kind != "chapter" && r.Kind != "series" || r.ID <= 0 {
			return badRequest("each item needs a kind (chapter or series) and an id")
		}
		at := r.At
		if at.IsZero() {
			at = now
		}
		id := r.ID
		if r.Kind == "series" {
			id = groups[r.ID]
		}
		rows = append(rows, model.UpdateSeenItem{UserID: seen.UserID, Kind: r.Kind, ItemID: id, At: at.UTC()})
	}
	_, err = s.app.DB.NewInsert().Model(&rows).On("CONFLICT DO NOTHING").Exec(ctx)
	return err
}

// seriesGroups maps the series ids of new-title refs to their group (see
// seenRef). A series that's gone keeps its own id negated, as it would be.
func (s *Server) seriesGroups(ctx context.Context, refs []UpdateRef) (map[int64]int64, error) {
	out := map[int64]int64{}
	var ids []int64
	for _, r := range refs {
		if r.Kind == "series" && r.ID > 0 {
			out[r.ID] = -r.ID
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		ID    int64 `bun:"id"`
		Group int64 `bun:"grp"`
	}
	err := s.app.DB.NewSelect().TableExpr("series AS s").ColumnExpr("s.id").ColumnExpr(seriesUpdateGroup+" AS grp").
		Where("s.id IN (?)", bun.In(ids)).Scan(ctx, &rows)
	for _, r := range rows {
		out[r.ID] = r.Group
	}
	return out, err
}
