// Package imports restores other apps' libraries into mangarr: an uploaded
// backup (see internal/backupimport) is mapped entry by entry to catalog
// manga and metadata, reviewed by the user, then added with its read state.
package imports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/catalogs"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/sourcesearch"
)

var (
	ErrNotFound = errors.New("import not found")
	// ErrBusy is returned while an import is being mapped or run.
	ErrBusy = errors.New("this import is busy; wait for it to finish")
)

type Service struct {
	DB       *db.DB
	Bus      *events.Bus
	Settings *settings.Store
	Mods     *modules.Manager
	Catalogs *catalogs.Service
	Search   *sourcesearch.Service
	Metadata *metadataagg.Aggregator
	Series   *series.Service
	// Sync refreshes a series' sources (applying its add options).
	Sync func(ctx context.Context, seriesID int64) error
	// Push queues a command (RestoreProgress, RefreshSeries).
	Push func(ctx context.Context, name string, body map[string]any) error
	Log  *slog.Logger
	// Dir keeps the uploaded files.
	Dir string

	busy sync.Map // import id -> struct{}
}

// ParseError is returned for uploads that aren't a readable backup.
type ParseError struct{ Err error }

func (e *ParseError) Error() string { return e.Err.Error() }
func (e *ParseError) Unwrap() error { return e.Err }

// lock marks an import busy (mapping or running).
func (s *Service) lock(id int64) (func(), error) {
	if _, loaded := s.busy.LoadOrStore(id, struct{}{}); loaded {
		return nil, ErrBusy
	}
	return func() { s.busy.Delete(id) }, nil
}

// Busy reports whether an import is being mapped or run.
func (s *Service) Busy(id int64) bool {
	_, ok := s.busy.Load(id)
	return ok
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// Create parses a backup and stores it with one entry per manga.
func (s *Service) Create(ctx context.Context, fileName string, data []byte) (*model.Import, error) {
	b, err := backupimport.Parse(data)
	if err != nil {
		return nil, &ParseError{err}
	}
	return s.create(ctx, fileName, b, data, nil)
}

// create stores a parsed backup (data is kept as the upload when set);
// prepare adjusts the new import before it is saved.
func (s *Service) create(ctx context.Context, fileName string, b *backupimport.Backup, data []byte, prepare func(*model.Import)) (*model.Import, error) {
	now := time.Now().UTC()
	opts := model.DefaultImportOptions()
	var rf model.RootFolder
	if err := s.DB.NewSelect().Model(&rf).Order("id").Limit(1).Scan(ctx); err == nil {
		opts.RootFolderID = rf.ID
	}
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser {
		opts.UserID = p.UserID // read state goes to whoever imports
	}
	imp := &model.Import{Format: b.Format, FileName: filepath.Base(fileName), Status: model.ImportMapping, Options: opts,
		Info: model.ImportInfo{Categories: usedCategories(b), Sources: b.Sources, Entries: len(b.Entries)}, CreatedAt: now, UpdatedAt: now}
	if imp.FileName == "" || imp.FileName == "." {
		imp.FileName = "backup"
	}
	if !b.CreatedAt.IsZero() {
		imp.Info.BackupDate = &b.CreatedAt
	}
	if prepare != nil {
		prepare(imp)
	}
	opts = imp.Options
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(imp).Exec(ctx); err != nil {
			return err
		}
		entries := make([]model.ImportEntry, 0, len(b.Entries))
		for i, e := range b.Entries {
			entries = append(entries, model.ImportEntry{ImportID: imp.ID, Position: i, Title: e.Title, State: model.EntryPending,
				Selected: selectedByDefault(e, opts), Data: e, UpdatedAt: now})
		}
		for start := 0; start < len(entries); start += 200 {
			chunk := entries[start:min(start+200, len(entries))]
			if _, err := tx.NewInsert().Model(&chunk).Exec(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.Dir != "" && data != nil {
		if err := os.MkdirAll(s.Dir, 0o750); err == nil {
			name := fmt.Sprintf("%d-%s", imp.ID, unsafeName.ReplaceAllString(imp.FileName, "_"))
			if err := os.WriteFile(filepath.Join(s.Dir, name), data, 0o640); err != nil {
				s.Log.Warn("keep uploaded backup", "err", err)
			}
		}
	}
	s.Bus.Changed("import", "created", imp.ID)
	return imp, nil
}

// usedCategories lists the backup's categories that have manga (apps also
// list an implicit "Default" one).
func usedCategories(b *backupimport.Backup) []string {
	used := map[string]bool{}
	for _, e := range b.Entries {
		for _, c := range e.Categories {
			used[c] = true
		}
	}
	out := []string{}
	for _, c := range b.Categories {
		if used[c] {
			out = append(out, c)
			delete(used, c)
		}
	}
	for _, e := range b.Entries { // categories missing from the list
		for _, c := range e.Categories {
			if used[c] {
				out = append(out, c)
				delete(used, c)
			}
		}
	}
	return out
}

func selectedByDefault(e backupimport.BackupManga, opts model.ImportOptions) bool {
	if opts.OnlyFavorites && !e.Favorite {
		return false
	}
	for _, c := range e.Categories {
		if opts.Categories[c].Skip {
			return false
		}
	}
	return true
}

// Get loads an import.
func (s *Service) Get(ctx context.Context, id int64) (*model.Import, error) {
	var imp model.Import
	if err := s.DB.NewSelect().Model(&imp).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &imp, nil
}

// List returns all imports, newest first.
func (s *Service) List(ctx context.Context) ([]model.Import, error) {
	out := []model.Import{}
	err := s.DB.NewSelect().Model(&out).Order("id DESC").Scan(ctx)
	return out, err
}

// Counts returns the number of entries per state (and "selected").
func (s *Service) Counts(ctx context.Context, id int64) (map[string]int, error) {
	var rows []struct {
		State    string `bun:"state"`
		Selected bool   `bun:"selected"`
		N        int    `bun:"n"`
	}
	err := s.DB.NewSelect().Model((*model.ImportEntry)(nil)).Column("state", "selected").ColumnExpr("COUNT(*) AS n").
		Where("import_id = ?", id).Group("state", "selected").Scan(ctx, &rows)
	counts := map[string]int{}
	for _, r := range rows {
		counts[r.State] += r.N
		counts["all"] += r.N
		if r.Selected {
			counts["selected"] += r.N
			counts["selected:"+r.State] += r.N
		}
	}
	return counts, err
}

// EntryFilter selects entries.
type EntryFilter struct {
	State    string
	Selected *bool
	Query    string
	IDs      []int64
	// List is a category of the backup or a list of the site account.
	List string
	// Source is the matched source's name, or NoSource for entries without one.
	Source string
	// Progress is "started" or "none" (nothing read yet).
	Progress string
	// Sort is "" (order in the file), "title", "read" (most read first),
	// "recent" (read most recently first) or "chapters" (most chapters first).
	Sort string
}

// NoSource filters entries that have no matched source.
const NoSource = "-"

func (f EntryFilter) apply(q *bun.SelectQuery) *bun.SelectQuery {
	if f.State != "" {
		q = q.Where("state = ?", f.State)
	}
	if f.Selected != nil {
		q = q.Where("selected = ?", *f.Selected)
	}
	if f.Query != "" {
		q = q.Where("LOWER(title) LIKE ?", "%"+strings.ToLower(f.Query)+"%")
	}
	if len(f.IDs) > 0 {
		q = q.Where("id IN (?)", bun.In(f.IDs))
	}
	return q
}

// inMemory is whether the filter reads the entries' data, which is JSON
// and is filtered and sorted here rather than in SQL.
func (f EntryFilter) inMemory() bool {
	return f.List != "" || f.Source != "" || f.Progress != "" || f.Sort != ""
}

func (f EntryFilter) match(e *model.ImportEntry) bool {
	if f.List != "" && !slices.Contains(e.Data.Categories, f.List) {
		return false
	}
	if f.Source != "" {
		name := NoSource
		if e.Source != nil {
			name = e.Source.SourceName
		}
		if name != f.Source {
			return false
		}
	}
	switch f.Progress {
	case "started":
		return Started(e.Data)
	case "none":
		return !Started(e.Data)
	}
	return true
}

// Started is whether any of the manga was read.
func Started(m backupimport.BackupManga) bool {
	if m.ReadUpTo != nil && (m.ReadUpTo.Chapter > 0 || m.ReadUpTo.Page > 0 || m.ReadUpTo.ChapterRef != "") {
		return true
	}
	for _, c := range m.Chapters {
		if c.Read || c.LastPageRead > 0 {
			return true
		}
	}
	return false
}

// reached is how far the manga was read, for sorting.
func reached(m backupimport.BackupManga) float64 {
	n := float64(m.ReadCount())
	if m.ReadUpTo != nil {
		n = max(n, m.ReadUpTo.Chapter)
	}
	return n
}

// lastRead is when a chapter of the manga was last read (zero when unknown).
func lastRead(m backupimport.BackupManga) time.Time {
	var at time.Time
	for _, c := range m.Chapters {
		if c.ReadAt != nil && c.ReadAt.After(at) {
			at = *c.ReadAt
		}
	}
	return at
}

func (f EntryFilter) sort(list []model.ImportEntry) {
	var cmp func(a, b *model.ImportEntry) int
	switch f.Sort {
	case "title":
		cmp = func(a, b *model.ImportEntry) int {
			return strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title))
		}
	case "read":
		cmp = func(a, b *model.ImportEntry) int { return compareDesc(reached(a.Data), reached(b.Data)) }
	case "recent":
		cmp = func(a, b *model.ImportEntry) int { return lastRead(b.Data).Compare(lastRead(a.Data)) }
	case "chapters":
		cmp = func(a, b *model.ImportEntry) int { return compareDesc(len(a.Data.Chapters), len(b.Data.Chapters)) }
	default:
		cmp = func(*model.ImportEntry, *model.ImportEntry) int { return 0 }
	}
	slices.SortStableFunc(list, func(a, b model.ImportEntry) int {
		if c := cmp(&a, &b); c != 0 {
			return c
		}
		return a.Position - b.Position
	})
}

func compareDesc[T int | float64](a, b T) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

// selectEntries loads the entries of an import matching f, in its order.
func (s *Service) selectEntries(ctx context.Context, id int64, f EntryFilter) ([]model.ImportEntry, error) {
	out := []model.ImportEntry{}
	if err := f.apply(s.DB.NewSelect().Model(&out).Where("import_id = ?", id)).Order("position").Scan(ctx); err != nil {
		return nil, err
	}
	if !f.inMemory() {
		return out, nil
	}
	kept := out[:0]
	for i := range out {
		if f.match(&out[i]) {
			kept = append(kept, out[i])
		}
	}
	f.sort(kept)
	return kept, nil
}

// Entries lists a page of entries.
func (s *Service) Entries(ctx context.Context, id int64, f EntryFilter, page, pageSize int) ([]model.ImportEntry, int, error) {
	if f.inMemory() {
		all, err := s.selectEntries(ctx, id, f)
		if err != nil {
			return nil, 0, err
		}
		if pageSize <= 0 {
			return all, len(all), nil
		}
		from := min(max(page-1, 0)*pageSize, len(all))
		return all[from:min(from+pageSize, len(all))], len(all), nil
	}
	out := []model.ImportEntry{}
	q := f.apply(s.DB.NewSelect().Model(&out).Where("import_id = ?", id)).Order("position")
	if pageSize > 0 {
		q = q.Limit(int64(pageSize)).Offset(int64(max(page-1, 0) * pageSize))
	}
	total, err := q.ScanAndCount(ctx)
	return out, int(total), err
}

// Facets counts an import's entries per list and per matched source
// (NoSource for those without one).
func (s *Service) Facets(ctx context.Context, id int64) (lists, sources map[string]int, err error) {
	var rows []model.ImportEntry
	if err := s.DB.NewSelect().Model(&rows).Column("data", "source").Where("import_id = ?", id).Scan(ctx); err != nil {
		return nil, nil, err
	}
	lists, sources = map[string]int{}, map[string]int{}
	for _, e := range rows {
		for _, c := range e.Data.Categories {
			lists[c]++
		}
		name := NoSource
		if e.Source != nil {
			name = e.Source.SourceName
		}
		sources[name]++
	}
	return lists, sources, nil
}

// EntryPatch changes entries.
type EntryPatch struct {
	Selected *bool `json:"selected,omitempty"`
	// Source picks the catalog manga (one entry).
	Source *model.ImportSource `json:"source,omitempty"`
	// Metadata picks the metadata series (one entry); ClearMetadata removes it.
	Metadata      *model.ImportMetadata `json:"metadata,omitempty"`
	ClearMetadata bool                  `json:"clearMetadata,omitempty"`
	// Accept confirms the suggested source of entries under review.
	Accept bool `json:"accept,omitempty"`
}

// UpdateEntries applies a patch to the entries matching f.
func (s *Service) UpdateEntries(ctx context.Context, id int64, f EntryFilter, p EntryPatch) (int, error) {
	if s.Busy(id) {
		return 0, ErrBusy
	}
	entries, err := s.selectEntries(ctx, id, f)
	if err != nil {
		return 0, err
	}
	if (p.Source != nil || p.Metadata != nil) && len(entries) != 1 {
		return 0, fmt.Errorf("a source or metadata can only be set for one entry at a time")
	}
	now := time.Now().UTC()
	changed := 0
	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		for i := range entries {
			e := &entries[i]
			if e.State == model.EntryImported {
				continue
			}
			if p.Selected != nil {
				e.Selected = *p.Selected
			}
			if p.Source != nil {
				src := *p.Source
				src.How = model.MatchManual
				e.Source, e.State, e.Message, e.Extension = &src, model.EntryReady, "", nil
				e.Selected = true
			}
			if p.Accept && e.State == model.EntryReview && e.Source != nil {
				e.State, e.Message, e.Selected = model.EntryReady, "", true
			}
			if p.Metadata != nil {
				md := *p.Metadata
				md.How = model.MatchManual
				e.Metadata = &md
			}
			if p.ClearMetadata {
				e.Metadata = nil
			}
			e.UpdatedAt = now
			if _, err := tx.NewUpdate().Model(e).Column("selected", "source", "metadata", "extension", "state", "message", "updated_at").
				WherePK().Exec(ctx); err != nil {
				return err
			}
			changed++
		}
		return nil
	})
	if err == nil && changed > 0 {
		s.Bus.Changed("import", "updated", id)
	}
	return changed, err
}

// SetOptions saves an import's options.
func (s *Service) SetOptions(ctx context.Context, id int64, opts model.ImportOptions) (*model.Import, error) {
	imp, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if imp.Status == model.ImportRunning {
		return nil, ErrBusy
	}
	if imp.RequestedBy != nil {
		// the progress is the user's who asked for it
		opts.ReadState, opts.UserID = true, *imp.RequestedBy
	}
	onlyFavChanged := opts.OnlyFavorites != imp.Options.OnlyFavorites
	imp.Options, imp.UpdatedAt = opts, time.Now().UTC()
	if _, err := s.DB.NewUpdate().Model(imp).Column("options", "updated_at").WherePK().Exec(ctx); err != nil {
		return nil, err
	}
	if onlyFavChanged || len(opts.Categories) > 0 {
		// reselect entries that aren't mapped to anything the user touched
		var entries []model.ImportEntry
		if err := s.DB.NewSelect().Model(&entries).Where("import_id = ? AND state <> ?", id, model.EntryImported).Scan(ctx); err != nil {
			return nil, err
		}
		for i := range entries {
			e := &entries[i]
			sel := selectedByDefault(e.Data, opts) && e.State != model.EntryReview && e.State != model.EntryExtension
			if e.Source != nil && e.Source.How == model.MatchManual {
				sel = selectedByDefault(e.Data, opts)
			}
			if sel != e.Selected {
				e.Selected = sel
				if _, err := s.DB.NewUpdate().Model(e).Column("selected").WherePK().Exec(ctx); err != nil {
					return nil, err
				}
			}
		}
	}
	s.Bus.Changed("import", "updated", id)
	return imp, nil
}

// Delete removes an import (series already added stay).
func (s *Service) Delete(ctx context.Context, id int64) error {
	if s.Busy(id) {
		return ErrBusy
	}
	imp, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.DB.NewDelete().Model((*model.ImportEntry)(nil)).Where("import_id = ?", id).Exec(ctx); err != nil {
		return err
	}
	// the request a user's import stands for goes with it
	if _, err := s.DB.NewDelete().Model((*model.Request)(nil)).Where("import_id = ?", id).Exec(ctx); err != nil {
		return err
	}
	if _, err := s.DB.NewDelete().Model(imp).WherePK().Exec(ctx); err != nil {
		return err
	}
	if s.Dir != "" {
		name := fmt.Sprintf("%d-%s", imp.ID, unsafeName.ReplaceAllString(imp.FileName, "_"))
		_ = os.Remove(filepath.Join(s.Dir, name))
	}
	s.Bus.Changed("import", "deleted", id)
	return nil
}

func (s *Service) setStatus(ctx context.Context, imp *model.Import, status, progress, errMsg string) {
	imp.Status, imp.Progress, imp.Error, imp.UpdatedAt = status, progress, errMsg, time.Now().UTC()
	if _, err := s.DB.NewUpdate().Model(imp).Column("status", "progress", "error", "updated_at").WherePK().Exec(ctx); err != nil {
		s.Log.Warn("save import status", "err", err)
	}
	s.Bus.Changed("import", "updated", imp.ID)
}

func (s *Service) saveEntry(ctx context.Context, e *model.ImportEntry) error {
	e.UpdatedAt = time.Now().UTC()
	_, err := s.DB.NewUpdate().Model(e).Column("state", "selected", "data", "source", "metadata", "extension", "series_id", "message", "updated_at").
		WherePK().Exec(ctx)
	return err
}

// Recover fixes imports interrupted by a restart: mapping continues,
// running ones wait for the user to start them again.
func (s *Service) Recover(ctx context.Context) ([]int64, error) {
	var list []model.Import
	if err := s.DB.NewSelect().Model(&list).Where("status IN (?)", bun.In([]string{model.ImportMapping, model.ImportRunning})).Scan(ctx); err != nil {
		return nil, err
	}
	var remap []int64
	for i := range list {
		imp := &list[i]
		if imp.Status == model.ImportMapping {
			remap = append(remap, imp.ID)
			continue
		}
		s.setStatus(ctx, imp, model.ImportReview, "", "interrupted by a restart; start it again to import the rest")
	}
	return remap, nil
}
