package imports

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/source"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/series"
)

// InstallExtensions installs the extensions entries wait for and returns
// the entries to map again.
func (s *Service) InstallExtensions(ctx context.Context, id int64, progress func(string)) ([]int64, error) {
	if s.Busy(id) {
		return nil, ErrBusy
	}
	var entries []model.ImportEntry
	if err := s.DB.NewSelect().Model(&entries).Where("import_id = ? AND state = ?", id, model.EntryExtension).Scan(ctx); err != nil {
		return nil, err
	}
	type pkg struct {
		moduleID int64
		name     string
	}
	todo := map[pkg]string{}
	var ids []int64
	for _, e := range entries {
		if e.Extension != nil {
			todo[pkg{e.Extension.ModuleID, e.Extension.Pkg}] = e.Extension.Name
			ids = append(ids, e.ID)
		}
	}
	var errs []error
	installed := map[int64]bool{}
	n := 0
	for p, name := range todo {
		n++
		if progress != nil {
			progress(fmt.Sprintf("installing %s (%d/%d)", name, n, len(todo)))
		}
		em, _, err := modules.GetAs[source.ExtensionManager](s.Mods, p.moduleID)
		if err == nil {
			err = em.InstallExtension(ctx, p.name)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		installed[p.moduleID] = true
	}
	for moduleID := range installed {
		s.Catalogs.Invalidate(moduleID)
		s.Bus.Changed("extension", "updated", moduleID)
	}
	return ids, errors.Join(errs...)
}

// RunResult counts what a run did.
type RunResult struct {
	Added    int `json:"added"`
	Merged   int `json:"merged"`
	Failed   int `json:"failed"`
	ReadSets int `json:"readStates"`
}

// Run adds the selected, mapped entries.
func (s *Service) Run(ctx context.Context, id int64, progress func(string)) (RunResult, error) {
	var res RunResult
	unlock, err := s.lock(id)
	if err != nil {
		return res, err
	}
	defer unlock()
	imp, err := s.Get(ctx, id)
	if err != nil {
		return res, err
	}
	opts := imp.Options
	if opts.RootFolderID == 0 {
		return res, errors.New("choose a root folder first")
	}
	var entries []model.ImportEntry
	if err := s.DB.NewSelect().Model(&entries).Where("import_id = ? AND selected = ? AND state IN (?)", id, true,
		bun.In([]string{model.EntryReady, model.EntryLibrary, model.EntryFailed})).Order("position").Scan(ctx); err != nil {
		return res, err
	}
	s.setStatus(ctx, imp, model.ImportRunning, fmt.Sprintf("importing 0 of %d", len(entries)), "")
	fail := func(err error) (RunResult, error) {
		s.setStatus(ctx, imp, model.ImportReview, "", err.Error())
		return res, err
	}
	if opts.ReadState && opts.ReaderID == 0 {
		r := &model.Reader{Name: readerName(imp.Format), CreatedAt: time.Now().UTC()}
		if _, err := s.DB.NewInsert().Model(r).Exec(ctx); err != nil {
			return fail(err)
		}
		opts.ReaderID, imp.Options.ReaderID = r.ID, r.ID
		if _, err := s.DB.NewUpdate().Model(imp).Column("options").WherePK().Exec(ctx); err != nil {
			return fail(err)
		}
		s.Bus.Changed("readers", "created", r.ID)
	}
	tags := map[string]int64{}
	if opts.CategoryTags {
		// only categories in use (apps list an implicit "Default" too)
		var used []string
		seen := map[string]bool{}
		for _, e := range entries {
			for _, c := range e.Data.Categories {
				if !seen[c] {
					seen[c] = true
					used = append(used, c)
				}
			}
		}
		if tags, err = s.ensureTags(ctx, used); err != nil {
			return fail(err)
		}
	}

	// entries of the same series (other sources, same metadata) merge into
	// the series added first: the one with the most read chapters goes first
	// so monitoring starts after what was read
	orderDuplicates(entries)
	added := map[string]int64{}
	resume := map[int64]resumePoint{}
	for i := range entries {
		if err := ctx.Err(); err != nil {
			s.setStatus(ctx, imp, model.ImportReview, "", "stopped")
			return res, err
		}
		e := &entries[i]
		merged, n, err := s.runEntry(ctx, imp, e, tags, added, resume)
		switch {
		case err != nil:
			res.Failed++
			e.State, e.Message = model.EntryFailed, err.Error()
			s.Log.Warn("import entry failed", "title", e.Title, "err", err)
		case merged:
			res.Merged++
			e.State = model.EntryImported
		default:
			res.Added++
			e.State = model.EntryImported
		}
		res.ReadSets += n
		if err := s.saveEntry(ctx, e); err != nil {
			return fail(err)
		}
		msg := fmt.Sprintf("importing %d of %d", i+1, len(entries))
		if progress != nil {
			progress(msg)
		}
		imp.Progress = msg
		s.setStatus(ctx, imp, model.ImportRunning, msg, "")
	}
	summary := fmt.Sprintf("%d added, %d merged, %d failed, %d chapters marked read", res.Added, res.Merged, res.Failed, res.ReadSets)
	s.setStatus(ctx, imp, model.ImportDone, summary, "")
	return res, nil
}

func readerName(format string) string {
	if format == backupimport.FormatAidoku {
		return "Aidoku backup"
	}
	return "Mihon backup"
}

func (s *Service) ensureTags(ctx context.Context, names []string) (map[string]int64, error) {
	out := map[string]int64{}
	var existing []model.Tag
	if err := s.DB.NewSelect().Model(&existing).Scan(ctx); err != nil {
		return nil, err
	}
	byLabel := map[string]int64{}
	for _, t := range existing {
		byLabel[strings.ToLower(t.Label)] = t.ID
	}
	for _, n := range names {
		label := strings.ToLower(strings.TrimSpace(n))
		if label == "" {
			continue
		}
		if id, ok := byLabel[label]; ok {
			out[n] = id
			continue
		}
		t := &model.Tag{Label: label}
		if _, err := s.DB.NewInsert().Model(t).Exec(ctx); err != nil {
			return nil, err
		}
		byLabel[label], out[n] = t.ID, t.ID
		s.Bus.Changed("tag", "created", t.ID)
	}
	return out, nil
}

func metaKey(md *model.ImportMetadata) string {
	if md == nil {
		return ""
	}
	return md.Provider + ":" + md.ID
}

// runEntry adds (or merges) one entry. It returns whether it merged into an
// existing series and how many read states were written.
func (s *Service) runEntry(ctx context.Context, imp *model.Import, e *model.ImportEntry, tags map[string]int64, added map[string]int64,
	resume map[int64]resumePoint) (bool, int, error) {
	if e.Source == nil {
		return false, 0, errors.New("no source picked")
	}
	opts := imp.Options
	var target int64
	switch {
	case e.SeriesID != nil:
		target = *e.SeriesID
	case added[metaKey(e.Metadata)] != 0:
		target = added[metaKey(e.Metadata)]
	}
	if target != 0 {
		if rp, ok := resume[target]; ok {
			// added by this run: monitor from the first chapter unread in
			// every merged entry, including the new source's chapters
			if from, read := e.Data.ResumeFrom(); read && (!rp.read || from > rp.from) {
				rp = resumePoint{from: from, read: true}
				resume[target] = rp
			}
			if err := s.monitorAgain(ctx, target, rp, opts); err != nil {
				return true, 0, err
			}
		}
		if err := s.merge(ctx, target, e); err != nil {
			return true, 0, err
		}
		e.SeriesID = &target
		e.Message = "merged into an existing series"
		n, err := s.readState(ctx, opts, target, e.Data)
		return true, n, err
	}

	d := e.Data
	req := series.AddRequest{Title: d.Title, RootFolderID: opts.RootFolderID, ProfileID: opts.ProfileID, MonitorNew: opts.MonitorNew,
		SearchMissing: opts.SearchMissing, Tags: []int64{}, NoRefresh: true,
		Sources: []series.SourceLink{{ModuleID: e.Source.ModuleID, SourceID: e.Source.SourceID, URL: e.Source.URL, Title: e.Source.Title,
			SourceName: e.Source.SourceName, Lang: e.Source.Lang}}}
	for _, c := range d.Categories {
		if rule, ok := opts.Categories[c]; ok {
			if rule.RootFolderID != 0 {
				req.RootFolderID = rule.RootFolderID
			}
			if rule.ProfileID != 0 {
				req.ProfileID = rule.ProfileID
			}
		}
		if id, ok := tags[c]; ok {
			req.Tags = append(req.Tags, id)
		}
	}
	if opts.BlockScanlators {
		req.BlockedScanlators = d.ExcludedScanlators
	}
	switch opts.Monitor {
	case "unread", "":
		req.Monitor = model.MonitorAll
		if from, ok := d.ResumeFrom(); ok {
			req.Monitor, req.FromChapter = model.MonitorFrom, from
		}
	default:
		req.Monitor = opts.Monitor
	}
	if e.Metadata != nil {
		req.Metadata = &metadataagg.Ref{ModuleID: e.Metadata.ModuleID, Provider: e.Metadata.Provider, ID: e.Metadata.ID}
	}
	ser, err := s.Series.Add(ctx, req)
	if err != nil && req.Metadata != nil && !errors.Is(err, series.ErrExists) && strings.HasPrefix(err.Error(), "metadata:") {
		// the provider is down or the id is gone: add it without metadata
		s.Log.Warn("import without metadata", "title", d.Title, "err", err)
		req.Metadata = nil
		ser, err = s.Series.Add(ctx, req)
		e.Message = "added without metadata (" + err2str(err) + ")"
	}
	if errors.Is(err, series.ErrExists) {
		if id := s.seriesByMetadata(ctx, e.Metadata); id != 0 {
			if err := s.merge(ctx, id, e); err != nil {
				return true, 0, err
			}
			e.SeriesID = &id
			e.Message = "merged into an existing series"
			n, err := s.readState(ctx, opts, id, d)
			return true, n, err
		}
	}
	if err != nil {
		return false, 0, err
	}
	e.SeriesID = &ser.ID
	if k := metaKey(e.Metadata); k != "" {
		added[k] = ser.ID
	}
	if opts.Monitor == "unread" || opts.Monitor == "" {
		resume[ser.ID] = resumePoint{from: req.FromChapter, read: req.Monitor == model.MonitorFrom}
	}
	// the first sync creates the chapters the read state is mapped to
	var syncErr error
	for attempt := 0; attempt < 2; attempt++ {
		if syncErr = s.Sync(ctx, ser.ID); syncErr == nil {
			break
		}
	}
	if syncErr != nil {
		e.Message = "added; the first refresh failed (" + syncErr.Error() + "), read chapters weren't imported"
		return false, 0, nil
	}
	n, err := s.readState(ctx, opts, ser.ID, d)
	return false, n, err
}

type resumePoint struct {
	from float64
	read bool
}

// orderDuplicates moves the entry with the most read chapters to the front
// of each group sharing metadata (keeping the group where it first appears).
func orderDuplicates(entries []model.ImportEntry) {
	first := map[string]int{}
	for i, e := range entries {
		if k := metaKey(e.Metadata); k != "" {
			if _, ok := first[k]; !ok {
				first[k] = i
			}
		}
	}
	rank := func(e model.ImportEntry) int {
		if k := metaKey(e.Metadata); k != "" {
			return first[k]
		}
		return -1
	}
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := entries[idx[a]], entries[idx[b]]
		ka, kb := rank(ea), rank(eb)
		pa, pb := idx[a], idx[b]
		if ka >= 0 {
			pa = ka
		}
		if kb >= 0 {
			pb = kb
		}
		if pa != pb {
			return pa < pb
		}
		return ea.Data.ReadCount() > eb.Data.ReadCount()
	})
	out := make([]model.ImportEntry, len(entries))
	for i, j := range idx {
		out[i] = entries[j]
	}
	copy(entries, out)
}

// monitorAgain makes the next sync apply the add options again (monitoring
// every chapter from rp, and searching when asked).
func (s *Service) monitorAgain(ctx context.Context, seriesID int64, rp resumePoint, opts model.ImportOptions) error {
	ser, err := s.Series.Get(ctx, seriesID)
	if err != nil {
		return err
	}
	ser.AddOptions = model.AddOptions{Pending: true, Monitor: model.MonitorAll, SearchMissing: opts.SearchMissing}
	if rp.read {
		ser.AddOptions.Monitor, ser.AddOptions.FromChapter = model.MonitorFrom, rp.from
	}
	_, err = s.DB.NewUpdate().Model(ser).Column("add_options").WherePK().Exec(ctx)
	return err
}

func err2str(err error) string {
	if err == nil {
		return "metadata lookup failed"
	}
	return err.Error()
}

func (s *Service) seriesByMetadata(ctx context.Context, md *model.ImportMetadata) int64 {
	if md == nil {
		return 0
	}
	var all []model.Series
	if err := s.DB.NewSelect().Model(&all).Column("id", "metadata").Where("preview = ?", false).Scan(ctx); err != nil {
		return 0
	}
	for _, ser := range all {
		if ser.Metadata.ExternalIDs[md.Provider] == md.ID {
			return ser.ID
		}
	}
	return 0
}

// merge links the entry's source to an existing series (unless linked).
func (s *Service) merge(ctx context.Context, seriesID int64, e *model.ImportEntry) error {
	n, err := s.DB.NewSelect().Model((*model.SeriesSource)(nil)).
		Where("series_id = ? AND module_id = ? AND source_id = ? AND manga_url = ?", seriesID, e.Source.ModuleID, e.Source.SourceID, e.Source.URL).
		Count(ctx)
	if err != nil || n > 0 {
		return err
	}
	_, err = s.Series.LinkSource(ctx, seriesID, series.SourceLink{ModuleID: e.Source.ModuleID, SourceID: e.Source.SourceID, URL: e.Source.URL,
		Title: e.Source.Title, SourceName: e.Source.SourceName, Lang: e.Source.Lang})
	if err != nil {
		return err
	}
	if s.Sync != nil {
		_ = s.Sync(ctx, seriesID) // chapters of the new source, for the read state
	}
	return nil
}

// readState writes the entry's read chapters for the import's reader.
func (s *Service) readState(ctx context.Context, opts model.ImportOptions, seriesID int64, d backupimport.BackupManga) (int, error) {
	if !opts.ReadState || opts.ReaderID == 0 {
		return 0, nil
	}
	n, err := ImportReadState(ctx, s.DB, opts.ReaderID, seriesID, d, time.Now().UTC())
	if err != nil || n == 0 {
		return n, err
	}
	s.Bus.Changed("series", "updated", seriesID)
	if opts.PushProgress && s.Push != nil {
		_ = s.Push(ctx, "RestoreProgress", map[string]any{"seriesId": seriesID})
	}
	return n, nil
}

// ImportReadState maps the backup's read chapters to the series' chapters
// (by chapter url, then number) and raises the reader's state. It returns
// how many chapter states changed.
func ImportReadState(ctx context.Context, idb bun.IDB, readerID, seriesID int64, d backupimport.BackupManga, now time.Time) (int, error) {
	var chapters []model.Chapter
	if err := idb.NewSelect().Model(&chapters).Column("id", "number_sort").Where("series_id = ?", seriesID).Scan(ctx); err != nil {
		return 0, err
	}
	var releases []model.ChapterRelease
	if err := idb.NewSelect().Model(&releases).Column("chapter_id", "chapter_url").Where("series_id = ? AND chapter_id IS NOT NULL", seriesID).
		Scan(ctx); err != nil {
		return 0, err
	}
	byURL := map[string]int64{}
	for _, r := range releases {
		if r.ChapterID != nil {
			byURL[r.ChapterURL] = *r.ChapterID
		}
	}
	byNumber := func(n float64) int64 {
		if n < 0 {
			return 0
		}
		for _, c := range chapters {
			if math.Abs(c.NumberSort-n) < 0.001 {
				return c.ID
			}
		}
		return 0
	}
	type want struct {
		completed bool
		page      int
		readAt    *time.Time
	}
	wanted := map[int64]want{}
	for _, c := range d.Chapters {
		if !c.Read && c.LastPageRead <= 0 {
			continue
		}
		id := byURL[c.URL]
		if id == 0 {
			id = byNumber(c.Number)
		}
		if id == 0 {
			continue
		}
		w := wanted[id]
		w.completed = w.completed || c.Read
		if !c.Read {
			w.page = max(w.page, c.LastPageRead+1) // 1-based like library servers
		}
		if c.ReadAt != nil && (w.readAt == nil || c.ReadAt.After(*w.readAt)) {
			w.readAt = c.ReadAt
		}
		wanted[id] = w
	}
	if len(wanted) == 0 {
		return 0, nil
	}
	var existing []model.ChapterReadState
	if err := idb.NewSelect().Model(&existing).Where("reader_id = ? AND series_id = ?", readerID, seriesID).Scan(ctx); err != nil {
		return 0, err
	}
	have := map[int64]*model.ChapterReadState{}
	for i := range existing {
		have[existing[i].ChapterID] = &existing[i]
	}
	changed := 0
	for id, w := range wanted {
		if w.completed {
			w.page = 0
		}
		readAt := w.readAt
		if readAt == nil && w.completed {
			readAt = &now
		}
		st := have[id]
		if st == nil {
			st = &model.ChapterReadState{ReaderID: readerID, ChapterID: id, SeriesID: seriesID, Completed: w.completed, Page: w.page,
				ReadAt: readAt, SyncedAt: now, Origin: model.ReadOriginBackup}
			if err := readstate.Save(ctx, idb, st); err != nil {
				return changed, err
			}
			changed++
			continue
		}
		// only raise progress
		if st.Completed || (!w.completed && st.Page >= w.page) {
			continue
		}
		st.Completed, st.Page, st.ReadAt, st.SyncedAt, st.Origin = w.completed, w.page, readAt, now, model.ReadOriginBackup
		if err := readstate.Save(ctx, idb, st); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}
