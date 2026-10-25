package imports

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
)

// AccountSource is a catalog whose site accounts can be imported.
type AccountSource struct {
	ModuleID int64 `json:"moduleId"`
	source.AccountSource
}

// ErrUnauthorized is returned when the site turns the sign-in token away.
var ErrUnauthorized = source.ErrUnauthorized

// AccountSources lists the catalogs whose site accounts can be imported.
func (s *Service) AccountSources(ctx context.Context) ([]AccountSource, error) {
	out := []AccountSource{}
	for _, m := range modules.ActiveAs[source.Accounts](s.Mods, modules.KindSource) {
		list, err := m.Instance.AccountSources(ctx)
		if err != nil {
			s.Log.Warn("list account sources", "module", m.Def.Name, "err", err)
			continue
		}
		for _, a := range list {
			out = append(out, AccountSource{ModuleID: m.Def.ID, AccountSource: a})
		}
	}
	return out, nil
}

// ReadAccount reads a site account's lists and progress into the entries a
// backup gives. It returns the site's name and the lists with their
// statuses. The token is used for this read only and isn't kept.
func (s *Service) ReadAccount(ctx context.Context, moduleID int64, sourceID, token string) (*backupimport.Backup, map[string]string, string, error) {
	acc, _, err := modules.GetAs[source.Accounts](s.Mods, moduleID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("%w: %v", source.ErrUnsupported, err)
	}
	var name string
	list, err := acc.AccountSources(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	for _, a := range list {
		if a.SourceID == sourceID {
			name = a.Name
		}
	}
	if name == "" {
		return nil, nil, "", fmt.Errorf("%w: this source's accounts can't be read", source.ErrUnsupported)
	}
	a, err := acc.ReadAccount(ctx, sourceID, token)
	if err != nil {
		if errors.Is(err, source.ErrUnauthorized) {
			return nil, nil, "", ErrUnauthorized
		}
		if errors.Is(err, source.ErrAccount) {
			return nil, nil, "", err
		}
		return nil, nil, "", fmt.Errorf("read the %s account: %w", name, err)
	}
	b, lists := accountBackup(a, sourceID, name, time.Now().UTC())
	return b, lists, name, nil
}

// CreateFromAccount reads a site account's lists and progress and stores
// them as an import, like an uploaded backup.
func (s *Service) CreateFromAccount(ctx context.Context, moduleID int64, sourceID, token string) (*model.Import, error) {
	b, lists, name, err := s.ReadAccount(ctx, moduleID, sourceID, token)
	if err != nil {
		return nil, err
	}
	return s.create(ctx, name+" account", b, nil, func(imp *model.Import) {
		imp.Info.ModuleID, imp.Info.Lists = moduleID, lists
		// all of an account's titles are in its lists, so none is history only
		imp.Options.OnlyFavorites = false
		// a title in the account is the source manga itself
		imp.Options.FindByTitle = false
		imp.Options.BlockScanlators = false
		for list, status := range lists {
			if status == source.ListDropped {
				if imp.Options.Categories == nil {
					imp.Options.Categories = map[string]model.ImportCategory{}
				}
				imp.Options.Categories[list] = model.ImportCategory{Skip: true}
			}
		}
	})
}

// accountBackup turns an account into the entries a backup gives: its lists
// are the categories. It returns the list names with their statuses.
func accountBackup(a *source.Account, sourceID, name string, now time.Time) (*backupimport.Backup, map[string]string) {
	b := &backupimport.Backup{Format: backupimport.FormatAccount, CreatedAt: now, Sources: map[string]string{sourceID: name},
		Categories: []string{}, Entries: []backupimport.BackupManga{}}
	names := map[string]string{}
	lists := map[string]string{}
	for _, l := range a.Lists {
		n := l.Name
		if n == "" {
			n = l.ID
		}
		if _, taken := lists[n]; taken {
			n = fmt.Sprintf("%s (%s)", n, l.ID)
		}
		names[l.ID], lists[n] = n, l.Status
		b.Categories = append(b.Categories, n)
	}
	for _, e := range a.Entries {
		m := backupimport.BackupManga{SourceID: sourceID, SourceName: name, URL: e.URL, Title: e.Title, Status: e.Status,
			ThumbnailURL: e.ThumbnailURL, Favorite: true, AltTitles: e.AltTitles}
		for _, id := range e.Lists {
			if n, ok := names[id]; ok {
				m.Categories = append(m.Categories, n)
			}
		}
		if e.Chapter >= 0 || e.ChapterRef != "" {
			m.ReadUpTo = &backupimport.ReadUpTo{Chapter: e.Chapter, Page: e.Page, ChapterRef: e.ChapterRef, ChapterPlace: e.ChapterPlace}
		}
		b.Entries = append(b.Entries, m)
	}
	return b, lists
}
