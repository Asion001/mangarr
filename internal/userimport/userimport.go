// Package userimport lets a user bring their own library from another app
// or a manga site: titles already on the server get their progress at once
// (internal/userdata), and the titles that aren't go to the admins as one
// import with the user's progress and lists. (Progress kept for requests
// of single titles, from before, is written once a request brings its title
// in.)
package userimport

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/imports"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/requests"
	"github.com/Asion001/mangarr/internal/userdata"
)

type Service struct {
	DB       *db.DB
	Imports  *imports.Service
	Mods     *modules.Manager
	Metadata *metadataagg.Aggregator
	Requests *requests.Service
	Log      *slog.Logger

	apply sync.Mutex
}

// statusLists names the lists a title's status and Favorites become in an
// import (as a site's built-in lists would be).
var statusLists = map[string]string{model.ReadReading: "Reading", model.ReadPlanned: "Planned", model.ReadCompleted: "Completed",
	model.ReadOnHold: "On hold", model.ReadDropped: "Dropped", source.ListFavorite: "Favorites"}

// ToBackup turns titles read from another app or site back into the entries
// of an import, with the lists of each title as its categories. It returns
// the lists that stand for a status or Favorites.
func ToBackup(titles []userdata.ExportTitle, origin *userdata.ExportOrigin, now time.Time) (*backupimport.Backup, map[string]string) {
	b := &backupimport.Backup{Format: backupimport.FormatMihon, CreatedAt: now, Sources: map[string]string{}, Categories: []string{}, Entries: []backupimport.BackupManga{}}
	if origin != nil && origin.Format != "" {
		b.Format = origin.Format
	}
	lists := map[string]string{}
	seen := map[string]bool{}
	category := func(name, status string) string {
		if status != "" {
			lists[name] = status
		}
		if !seen[name] {
			seen[name] = true
			b.Categories = append(b.Categories, name)
		}
		return name
	}
	for _, t := range titles {
		m := backupimport.BackupManga{Title: t.Title, AltTitles: t.AltTitles, Favorite: true, Status: "unknown"}
		if len(t.Sources) > 0 {
			src := t.Sources[0]
			m.SourceID, m.SourceName, m.URL = src.SourceID, src.Name, src.URL
			if src.Name != "" {
				b.Sources[src.SourceID] = src.Name
			}
		}
		if len(t.IDs) > 0 {
			m.Trackers = map[string]string{}
			for k, v := range t.IDs {
				m.Trackers[k] = v
			}
		}
		if t.Status != "" {
			m.Categories = append(m.Categories, category(statusLists[t.Status], t.Status))
		}
		if t.Favorite {
			m.Categories = append(m.Categories, category(statusLists[source.ListFavorite], source.ListFavorite))
		}
		for _, l := range t.Lists {
			m.Categories = append(m.Categories, category(l, ""))
		}
		for _, p := range t.Progress {
			n, err := strconv.ParseFloat(p.Number, 64)
			if err != nil {
				continue
			}
			c := backupimport.BackupChapter{Number: n, Read: p.Completed, ReadAt: p.ReadAt}
			if !p.Completed && p.Page > 0 {
				c.LastPageRead = p.Page - 1
			}
			m.Chapters = append(m.Chapters, c)
		}
		if r := t.Reached; r != nil {
			m.ReadUpTo = &backupimport.ReadUpTo{Chapter: r.Chapter, Page: r.Page, ChapterRef: r.ChapterRef, ChapterPlace: r.ChapterPlace}
		}
		b.Entries = append(b.Entries, m)
	}
	return b, lists
}

// RequestLibrary sends the titles of a user's library that aren't on the
// server to the admins as one import, reviewed on the Import page like
// their own, with one request standing for it. The import's read state goes
// to the user; follows and lists are written as its titles are added
// (Added).
func (s *Service) RequestLibrary(ctx context.Context, p *access.Principal, titles []userdata.ExportTitle, origin *userdata.ExportOrigin) (*model.Import, error) {
	if len(titles) == 0 {
		return nil, errors.New("no titles to ask for")
	}
	b, lists := ToBackup(titles, origin, time.Now().UTC())
	who := p.DisplayName
	if who == "" {
		who = p.Username
	}
	name := who + "'s library"
	var moduleID int64
	if origin != nil {
		moduleID = origin.ModuleID
	}
	imp, err := s.Imports.CreateRequested(ctx, name, b, moduleID, lists, p.UserID)
	if err != nil {
		return nil, err
	}
	if _, err := s.Requests.CreateImport(ctx, p, imp.ID, name, len(titles)); err != nil {
		return nil, err
	}
	return imp, nil
}

// Added writes the follows and lists of the titles a user's import added
// (its read state was written by the import itself), and tells the request.
func (s *Service) Added(ctx context.Context, importID int64, handler *access.Principal) error {
	imp, err := s.Imports.Get(ctx, importID)
	if err != nil || imp.RequestedBy == nil {
		return err
	}
	var entries []model.ImportEntry
	if err := s.DB.NewSelect().Model(&entries).Where("import_id = ? AND state = ? AND series_id IS NOT NULL", importID, model.EntryImported).
		Scan(ctx); err != nil {
		return err
	}
	for i := range entries {
		e := &entries[i]
		d := userdata.FromBackup(&backupimport.Backup{Entries: []backupimport.BackupManga{e.Data}}, "", imp.Info.Lists)
		t := d.Titles[0]
		t.Progress, t.Reached = nil, nil
		if err := s.writeOnly(ctx, *imp.RequestedBy, *e.SeriesID, &t, userdata.DataImportOptions{Follows: true, Lists: true}); err != nil {
			s.Log.Warn("write the lists of an imported title", "series", *e.SeriesID, "err", err)
		}
	}
	return s.Requests.ImportAdded(ctx, importID, len(entries), handler)
}

// write imports one title's progress, follow and lists into a series.
func (s *Service) write(ctx context.Context, userID, seriesID int64, t *userdata.ExportTitle) error {
	return s.writeOnly(ctx, userID, seriesID, t, userdata.DataImportOptions{Progress: true, Follows: true, Lists: true})
}

func (s *Service) writeOnly(ctx context.Context, userID, seriesID int64, t *userdata.ExportTitle, opts userdata.DataImportOptions) error {
	d := &userdata.Export{Format: userdata.Format, Version: userdata.Version, Exported: time.Now().UTC(), Titles: []userdata.ExportTitle{*t}}
	opts.Picked = map[int]int64{0: seriesID}
	_, err := (&userdata.Service{DB: s.DB}).Import(ctx, userdata.Account{UserID: userID}, d, opts)
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
