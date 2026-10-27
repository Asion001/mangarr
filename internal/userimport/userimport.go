// Package userimport lets a user bring their own library from another app
// or a manga site: titles already on the server get their progress at once
// (internal/userdata), and the titles that aren't are asked for as requests,
// their progress kept until a request brings the title in.
package userimport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/requests"
	"github.com/Asion001/mangarr/internal/userdata"
)

type Service struct {
	DB       *db.DB
	Mods     *modules.Manager
	Metadata *metadataagg.Aggregator
	Requests *requests.Service
	Log      *slog.Logger

	apply sync.Mutex
}

// Result counts what asking for titles did.
type Result struct {
	Requested int `json:"requested"`
	// Here are titles a request found already on the server; their progress
	// was written.
	Here int `json:"here"`
	// Unmatched are titles no metadata provider knows, so they can't be asked for.
	Unmatched []string `json:"unmatched,omitempty"`
	Failed    []string `json:"failed,omitempty"`
}

// Request asks the admins for each title (one request per title, the
// usual approval applying) and keeps its progress for the user.
func (s *Service) Request(ctx context.Context, p *access.Principal, titles []userdata.ExportTitle) (Result, error) {
	var res Result
	for i := range titles {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		t := &titles[i]
		md := s.metadataFor(ctx, t)
		if md == nil {
			res.Unmatched = append(res.Unmatched, t.Title)
			continue
		}
		r, _, err := s.Requests.Create(ctx, p, requests.CreateInput{ModuleID: md.ModuleID, MetaID: md.ID})
		var avail requests.AvailableError
		switch {
		case errors.As(err, &avail):
			if err := s.write(ctx, p.UserID, avail.SeriesID, t); err != nil {
				return res, err
			}
			res.Here++
			continue
		case err != nil:
			s.Log.Warn("request an imported title", "title", t.Title, "err", err)
			res.Failed = append(res.Failed, t.Title)
			continue
		}
		res.Requested++
		data, err := json.Marshal(t)
		if err != nil {
			return res, err
		}
		row := &model.RequestedProgress{UserID: p.UserID, RequestID: r.ID, Title: string(data), CreatedAt: time.Now().UTC()}
		if _, err := s.DB.NewInsert().Model(row).Exec(ctx); err != nil {
			return res, err
		}
	}
	if res.Requested+res.Here > 0 {
		_ = s.ApplyReady(ctx) // a request may have been added on the spot
	}
	return res, nil
}

// metadataFor is the metadata series to ask for: a tracker id the file
// carries, else a confident title match.
func (s *Service) metadataFor(ctx context.Context, t *userdata.ExportTitle) *model.ImportMetadata {
	for _, m := range modules.ActiveAs[metadata.Module](s.Mods, modules.KindMetadata) {
		if id := t.IDs[m.Def.Implementation]; id != "" {
			return &model.ImportMetadata{ModuleID: m.Def.ID, Provider: m.Def.Implementation, ID: id}
		}
	}
	return imports.MatchTitleMetadata(ctx, s.Metadata, t.Title, t.AltTitles)
}

// write imports one title's progress, follow and lists into a series.
func (s *Service) write(ctx context.Context, userID, seriesID int64, t *userdata.ExportTitle) error {
	d := &userdata.Export{Format: userdata.Format, Version: userdata.Version, Exported: time.Now().UTC(), Titles: []userdata.ExportTitle{*t}}
	_, err := (&userdata.Service{DB: s.DB}).Import(ctx, userdata.Account{UserID: userID}, d,
		userdata.DataImportOptions{Progress: true, Follows: true, Lists: true, Picked: map[int]int64{0: seriesID}})
	return err
}

// ApplyReady writes kept progress for every request that brought its title
// in and whose chapters are listed; the rest wait for a later call.
func (s *Service) ApplyReady(ctx context.Context) error {
	s.apply.Lock()
	defer s.apply.Unlock()
	var rows []struct {
		model.RequestedProgress `bun:",extend"`
		SeriesID                int64 `bun:"series_id"`
	}
	if err := s.DB.NewSelect().TableExpr("requested_progress AS rp").ColumnExpr("rp.*").ColumnExpr("r.series_id").
		Join("JOIN requests AS r ON r.id = rp.request_id").Where("r.series_id IS NOT NULL").Scan(ctx, &rows); err != nil {
		return err
	}
	for _, row := range rows {
		n, err := s.DB.NewSelect().Model((*model.Chapter)(nil)).Where("series_id = ?", row.SeriesID).Count(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			continue // listed on the first refresh
		}
		var t userdata.ExportTitle
		if err := json.Unmarshal([]byte(row.Title), &t); err == nil {
			if err := s.write(ctx, row.UserID, row.SeriesID, &t); err != nil {
				s.Log.Warn("write requested progress", "series", row.SeriesID, "err", err)
				continue
			}
		}
		if _, err := s.DB.NewDelete().Model((*model.RequestedProgress)(nil)).Where("id = ?", row.ID).Exec(ctx); err != nil {
			return err
		}
	}
	// requests declined or deleted take their progress with them
	_, err := s.DB.NewDelete().Model((*model.RequestedProgress)(nil)).
		Where("request_id NOT IN (SELECT id FROM requests WHERE status <> ?)", model.RequestDeclined).Exec(ctx)
	return err
}
