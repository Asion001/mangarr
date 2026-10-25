// Package userdata exports one account's own data (progress, reading
// history and time, follows, reader and interface settings) and imports it
// on another mangarr server. Series ids differ between servers, so every
// title carries what identifies it anywhere: its sources, metadata ids and
// names.
package userdata

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/backupimport"
	"github.com/Asion001/mangarr/internal/chapternum"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readstate"
	"github.com/Asion001/mangarr/internal/titlematch"
	"github.com/Asion001/mangarr/internal/version"
)

// Format names the file; Version changes when old files stop importing.
const (
	Format  = "mangarr-user-data"
	Version = 1
)

// Data is an exported account.
type Export struct {
	Format   string    `json:"format"`
	Version  int       `json:"version"`
	Exported time.Time `json:"exported"`
	Server   string    `json:"server,omitempty" doc:"mangarr version that exported it"`
	User     string    `json:"user,omitempty"`
	// UI are the interface preferences; Reader the web reader's defaults.
	UI     *ExportUI       `json:"ui,omitempty"`
	Reader json.RawMessage `json:"reader,omitempty"`
	Titles []ExportTitle   `json:"titles"`
}

type ExportUI struct {
	Locale  string          `json:"locale"`
	Mode    string          `json:"mode"`
	Options model.UIOptions `json:"options"`
}

// Title is one series and what the account did with it.
type ExportTitle struct {
	Title     string            `json:"title"`
	AltTitles []string          `json:"altTitles,omitempty"`
	Language  string            `json:"language,omitempty"`
	IDs       map[string]string `json:"ids,omitempty" doc:"Metadata ids, as anilist: 30013"`
	Sources   []ExportSource    `json:"sources,omitempty"`
	Following bool              `json:"following,omitempty"`
	// Reader are the web reader's settings for this title.
	Reader   json.RawMessage  `json:"reader,omitempty"`
	Progress []ExportProgress `json:"progress,omitempty"`
	// Reached is how far the title was read, from apps and sites that keep
	// only that: chapters numbered below it are read.
	Reached *ExportReached `json:"reached,omitempty"`
	// Lists are the app's categories or the site's lists the title was in.
	Lists    []string        `json:"lists,omitempty"`
	History  []ExportEvent   `json:"history,omitempty"`
	Sessions []ExportSession `json:"sessions,omitempty"`
}

type ExportSource struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name,omitempty"`
	Lang     string `json:"lang,omitempty"`
	URL      string `json:"url"`
}

// Progress is the state of one chapter number.
type ExportProgress struct {
	Number    string     `json:"number"`
	Completed bool       `json:"completed"`
	Page      int        `json:"page,omitempty"`
	ReadAt    *time.Time `json:"readAt,omitempty"`
	At        time.Time  `json:"at"`
}

// Reached is a reading position: chapters numbered below Chapter are read,
// and Chapter is open at Page (1-based, 0 when not started).
type ExportReached struct {
	Chapter float64 `json:"chapter"`
	Page    int     `json:"page,omitempty"`
	// ChapterRef is the source's id of the chapter reached when the file
	// knows only that (Chapter is then -1).
	ChapterRef string `json:"chapterRef,omitempty"`
}

// Event is an entry of the reading history.
type ExportEvent struct {
	Number    string    `json:"number"`
	Chapters  int       `json:"chapters,omitempty"`
	Completed bool      `json:"completed"`
	Page      int       `json:"page,omitempty"`
	Origin    string    `json:"origin,omitempty"`
	Client    string    `json:"client,omitempty"`
	Device    string    `json:"device,omitempty"`
	Outcome   string    `json:"outcome,omitempty"`
	At        time.Time `json:"at"`
}

// Session is reading time spent in a chapter.
type ExportSession struct {
	Number  string    `json:"number"`
	Seconds int       `json:"seconds"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
}

// Account is whose data it is.
type Account struct {
	UserID   int64
	Username string
}

// Service reads and writes an account's data.
type Service struct{ DB *db.DB }

// visible reports which series the caller may see (nil: all).
func visible(ctx context.Context) func(*model.Series) bool {
	p := access.From(ctx)
	if p == nil || p.Can(access.LibraryEdit) || !p.Scope.Limited() {
		return func(*model.Series) bool { return true }
	}
	return p.Scope.Allows
}

// Export collects the account's data. Titles it never touched are left out.
func (s *Service) Export(ctx context.Context, acc Account) (*Export, error) {
	var series []model.Series
	if err := s.DB.NewSelect().Model(&series).Where("preview = ?", false).Order("sort_title").Scan(ctx); err != nil {
		return nil, err
	}
	allowed := visible(ctx)
	titles := map[int64]*ExportTitle{}
	var order []int64
	for i := range series {
		ser := &series[i]
		if !allowed(ser) {
			continue
		}
		titles[ser.ID] = &ExportTitle{Title: ser.Title, AltTitles: ser.Metadata.AltTitles, Language: ser.Language, IDs: ser.Metadata.ExternalIDs}
		order = append(order, ser.ID)
	}
	touched := map[int64]bool{}

	var states []struct {
		SeriesID  int64      `bun:"series_id"`
		Number    string     `bun:"number_key"`
		Completed bool       `bun:"completed"`
		Page      int        `bun:"page"`
		ReadAt    *time.Time `bun:"read_at"`
		SyncedAt  time.Time  `bun:"synced_at"`
	}
	if err := s.DB.NewRaw(`SELECT st.series_id, c.number_key, st.completed, st.page, st.read_at, st.synced_at
FROM chapter_read_states AS st JOIN chapters AS c ON c.id = st.chapter_id WHERE st.user_id = ? ORDER BY c.number_sort`, acc.UserID).Scan(ctx, &states); err != nil {
		return nil, err
	}
	for _, st := range states {
		if t := titles[st.SeriesID]; t != nil {
			t.Progress = append(t.Progress, ExportProgress{Number: st.Number, Completed: st.Completed, Page: st.Page, ReadAt: st.ReadAt, At: st.SyncedAt})
			touched[st.SeriesID] = true
		}
	}

	var events []struct {
		SeriesID  int64     `bun:"series_id"`
		Number    string    `bun:"number_key"`
		Chapters  int       `bun:"chapters"`
		Completed bool      `bun:"completed"`
		Page      int       `bun:"page"`
		Origin    string    `bun:"origin"`
		Client    string    `bun:"client"`
		Device    string    `bun:"device"`
		Outcome   string    `bun:"outcome"`
		At        time.Time `bun:"at"`
	}
	if err := s.DB.NewRaw(`SELECT e.series_id, c.number_key, e.chapters, e.completed, e.page, e.origin, e.client, e.device, e.outcome, e.at
FROM read_events AS e JOIN chapters AS c ON c.id = e.chapter_id WHERE e.user_id = ? ORDER BY e.at, e.id`, acc.UserID).Scan(ctx, &events); err != nil {
		return nil, err
	}
	for _, e := range events {
		if t := titles[e.SeriesID]; t != nil {
			t.History = append(t.History, ExportEvent{Number: e.Number, Chapters: e.Chapters, Completed: e.Completed, Page: e.Page,
				Origin: e.Origin, Client: e.Client, Device: e.Device, Outcome: e.Outcome, At: e.At})
			touched[e.SeriesID] = true
		}
	}

	var sessions []struct {
		SeriesID  int64     `bun:"series_id"`
		Number    string    `bun:"number_key"`
		Seconds   int       `bun:"active_seconds"`
		StartedAt time.Time `bun:"started_at"`
		UpdatedAt time.Time `bun:"updated_at"`
	}
	if err := s.DB.NewRaw(`SELECT r.series_id, c.number_key, r.active_seconds, r.started_at, r.updated_at
FROM reading_sessions AS r JOIN chapters AS c ON c.id = r.chapter_id WHERE r.user_id = ? ORDER BY r.started_at`, acc.UserID).Scan(ctx, &sessions); err != nil {
		return nil, err
	}
	for _, se := range sessions {
		if t := titles[se.SeriesID]; t != nil {
			t.Sessions = append(t.Sessions, ExportSession{Number: se.Number, Seconds: se.Seconds, Started: se.StartedAt, Updated: se.UpdatedAt})
			touched[se.SeriesID] = true
		}
	}

	out := &Export{Format: Format, Version: Version, Exported: time.Now().UTC(), Server: version.Version, User: acc.Username, Titles: []ExportTitle{}}
	if acc.UserID > 0 {
		var follows []model.Follow
		if err := s.DB.NewSelect().Model(&follows).Where("user_id = ?", acc.UserID).Scan(ctx); err != nil {
			return nil, err
		}
		for _, f := range follows {
			if t := titles[f.SeriesID]; t != nil {
				t.Following = true
				touched[f.SeriesID] = true
			}
		}
		var prefs []model.ReaderPrefs
		if err := s.DB.NewSelect().Model(&prefs).Where("user_id = ?", acc.UserID).Scan(ctx); err != nil {
			return nil, err
		}
		for _, p := range prefs {
			if p.SeriesID == 0 {
				out.Reader = json.RawMessage(p.Data)
			} else if t := titles[p.SeriesID]; t != nil {
				t.Reader = json.RawMessage(p.Data)
				touched[p.SeriesID] = true
			}
		}
		var ui model.UIPreferences
		if err := s.DB.NewSelect().Model(&ui).Where("user_id = ?", acc.UserID).Scan(ctx); err == nil {
			out.UI = &ExportUI{Locale: ui.Locale, Mode: ui.Mode, Options: ui.Options}
		}
	}

	var sources []model.SeriesSource
	if len(touched) > 0 {
		if err := s.DB.NewSelect().Model(&sources).Order("id").Scan(ctx); err != nil {
			return nil, err
		}
	}
	for _, src := range sources {
		if t := titles[src.SeriesID]; t != nil && touched[src.SeriesID] {
			t.Sources = append(t.Sources, ExportSource{SourceID: src.SourceID, Name: src.SourceName, Lang: src.Lang, URL: src.MangaURL})
		}
	}
	for _, id := range order {
		if touched[id] {
			out.Titles = append(out.Titles, *titles[id])
		}
	}
	return out, nil
}

// Check rejects a file that isn't an account export this version reads.
func Check(d *Export) error {
	if d == nil || d.Format != Format {
		return errors.New("this isn't a mangarr data export")
	}
	if d.Version > Version {
		return fmt.Errorf("this export is from a newer mangarr (format %d); update this server first", d.Version)
	}
	return nil
}

// Match is where a title of the file goes on this server.
type DataTitleMatch struct {
	Index int    `json:"index"`
	Title string `json:"title"`
	// SeriesID is 0 when the title isn't on this server.
	SeriesID    int64  `json:"seriesId"`
	SeriesTitle string `json:"seriesTitle,omitempty"`
	By          string `json:"by,omitempty" enum:"source,id,title,picked,"`
	Read        int    `json:"read" doc:"Chapters read in the file"`
	Following   bool   `json:"following"`
	// Reached and Lists are the file's, for titles from apps and sites.
	Reached *ExportReached `json:"reached,omitempty"`
	Lists   []string       `json:"lists,omitempty"`
	// Source names where the title came from (titles not on this server).
	Source string `json:"source,omitempty"`
}

// library indexes this server's series for matching.
type library struct {
	series  map[int64]*model.Series
	sources map[string][]int64
	ids     map[string][]int64
	titles  map[string][]int64
}

func sourceKey(sourceID, url string) string { return sourceID + "\x00" + url }

func (s *Service) library(ctx context.Context) (*library, error) {
	var series []model.Series
	if err := s.DB.NewSelect().Model(&series).Where("preview = ?", false).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	var sources []model.SeriesSource
	if err := s.DB.NewSelect().Model(&sources).Scan(ctx); err != nil {
		return nil, err
	}
	allowed := visible(ctx)
	lib := &library{series: map[int64]*model.Series{}, sources: map[string][]int64{}, ids: map[string][]int64{}, titles: map[string][]int64{}}
	for i := range series {
		ser := &series[i]
		if !allowed(ser) {
			continue
		}
		lib.series[ser.ID] = ser
		for k, v := range ser.Metadata.ExternalIDs {
			if v != "" {
				lib.ids[k+":"+v] = append(lib.ids[k+":"+v], ser.ID)
			}
		}
		for _, name := range append([]string{ser.Title}, ser.Metadata.AltTitles...) {
			if n := titlematch.Normalize(name); n != "" {
				lib.titles[n] = append(lib.titles[n], ser.ID)
			}
		}
	}
	for _, src := range sources {
		if lib.series[src.SeriesID] != nil {
			k := sourceKey(src.SourceID, src.MangaURL)
			lib.sources[k] = append(lib.sources[k], src.SeriesID)
		}
	}
	return lib, nil
}

// pick prefers a candidate in the title's language.
func (l *library) pick(ids []int64, lang string) int64 {
	if len(ids) == 0 {
		return 0
	}
	for _, id := range ids {
		if l.series[id].Language == lang {
			return id
		}
	}
	return ids[0]
}

func (l *library) match(t *ExportTitle) (int64, string) {
	for _, src := range t.Sources {
		if id := l.pick(l.sources[sourceKey(src.SourceID, src.URL)], t.Language); id > 0 {
			return id, "source"
		}
	}
	keys := make([]string, 0, len(t.IDs))
	for k := range t.IDs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if id := l.pick(l.ids[k+":"+t.IDs[k]], t.Language); id > 0 {
			return id, "id"
		}
	}
	for _, name := range append([]string{t.Title}, t.AltTitles...) {
		if id := l.pick(l.titles[titlematch.Normalize(name)], t.Language); id > 0 {
			return id, "title"
		}
	}
	return 0, ""
}

// Preview matches every title of the file to a series here, without
// writing anything. picked overrides a match by title index (0: skip it).
func (s *Service) Preview(ctx context.Context, d *Export, picked map[int]int64) ([]DataTitleMatch, error) {
	if err := Check(d); err != nil {
		return nil, err
	}
	lib, err := s.library(ctx)
	if err != nil {
		return nil, err
	}
	matches := lib.matches(d, picked)
	// a position read up to counts the chapters it covers here
	for i := range matches {
		m := &matches[i]
		r := d.Titles[m.Index].Reached
		if m.SeriesID == 0 || r == nil {
			continue
		}
		if r.Chapter < 0 {
			n, ok := readstate.ChapterNumberByRef(ctx, s.DB, m.SeriesID, r.ChapterRef)
			if !ok {
				continue
			}
			resolved := *r
			resolved.Chapter = n
			m.Reached = &resolved
		}
		n, err := s.DB.NewSelect().Table("chapters").Where("series_id = ? AND number_sort >= 0 AND number_sort < ?", m.SeriesID, m.Reached.Chapter-0.0005).Count(ctx)
		if err != nil {
			return nil, err
		}
		m.Read = max(m.Read, int(n))
	}
	return matches, nil
}

func (l *library) matches(d *Export, picked map[int]int64) []DataTitleMatch {
	out := make([]DataTitleMatch, 0, len(d.Titles))
	for i := range d.Titles {
		t := &d.Titles[i]
		m := DataTitleMatch{Index: i, Title: t.Title, Following: t.Following, Reached: t.Reached, Lists: t.Lists}
		if len(t.Sources) > 0 {
			m.Source = firstNonBlank(t.Sources[0].Name, t.Sources[0].SourceID)
		}
		for _, p := range t.Progress {
			if p.Completed {
				m.Read++
			}
		}
		m.SeriesID, m.By = l.match(t)
		if id, ok := picked[i]; ok {
			m.SeriesID, m.By = 0, ""
			if l.series[id] != nil {
				m.SeriesID, m.By = id, "picked"
			}
		}
		if ser := l.series[m.SeriesID]; ser != nil {
			m.SeriesTitle = ser.Title
		}
		out = append(out, m)
	}
	return out
}

// Options choose what an import writes.
type DataImportOptions struct {
	Progress bool `json:"progress"`
	History  bool `json:"history" doc:"Reading history and reading time"`
	Follows  bool `json:"follows"`
	Settings bool `json:"settings" doc:"Reader and interface settings"`
	// Picked overrides matches: title index → series id (0: skip the title).
	Picked map[int]int64 `json:"picked,omitempty"`
	// Request asks the admins for titles that aren't on this server, by
	// index; their progress is written once they are added.
	Request []int `json:"request,omitempty"`
}

// Result counts what an import wrote.
type DataImportResult struct {
	Titles   int `json:"titles"`
	Progress int `json:"progress" doc:"Chapters whose progress was set"`
	Kept     int `json:"kept" doc:"Chapters already further here"`
	Missing  int `json:"missing" doc:"Chapters this server doesn't list (yet)"`
	History  int `json:"history"`
	Sessions int `json:"sessions"`
	Follows  int `json:"follows"`
	Settings int `json:"settings"`
	// Requested are titles not on this server asked for (in the background).
	Requested int `json:"requested,omitempty"`
}

// Import writes the file's data for the matched titles. It never lowers
// progress and adds no history entry twice, so it can run again.
func (s *Service) Import(ctx context.Context, acc Account, d *Export, opts DataImportOptions) (*DataImportResult, error) {
	if err := Check(d); err != nil {
		return nil, err
	}
	lib, err := s.library(ctx)
	if err != nil {
		return nil, err
	}
	matches := lib.matches(d, opts.Picked)
	res := &DataImportResult{}
	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		now := time.Now().UTC()
		for _, m := range matches {
			if m.SeriesID == 0 {
				continue
			}
			res.Titles++
			t := &d.Titles[m.Index]
			chapters, err := chapterIDs(ctx, tx, m.SeriesID)
			if err != nil {
				return err
			}
			if opts.Progress {
				if err := importProgress(ctx, tx, acc.UserID, lib.series[m.SeriesID], chapters, withReached(ctx, tx, m.SeriesID, t, chapters), res); err != nil {
					return err
				}
				if _, err := readstate.FinishLastPages(ctx, tx, m.SeriesID, now); err != nil {
					return err
				}
			}
			if opts.History {
				if err := importHistory(ctx, tx, acc.UserID, m.SeriesID, chapters, t, res); err != nil {
					return err
				}
			}
			if acc.UserID == 0 {
				continue
			}
			if opts.Follows && t.Following {
				r, err := tx.NewInsert().Model(&model.Follow{UserID: acc.UserID, SeriesID: m.SeriesID, CreatedAt: now}).On("CONFLICT DO NOTHING").Exec(ctx)
				if err != nil {
					return err
				}
				if n, _ := r.RowsAffected(); n > 0 {
					res.Follows++
				}
			}
			if opts.Settings && len(t.Reader) > 0 {
				if err := saveReaderPrefs(ctx, tx, acc.UserID, m.SeriesID, t.Reader, now); err != nil {
					return err
				}
				res.Settings++
			}
		}
		if opts.Settings && acc.UserID > 0 {
			if len(d.Reader) > 0 {
				if err := saveReaderPrefs(ctx, tx, acc.UserID, 0, d.Reader, now); err != nil {
					return err
				}
				res.Settings++
			}
			if d.UI != nil {
				ui := model.UIPreferences{UserID: acc.UserID, Locale: d.UI.Locale, Mode: d.UI.Mode, Options: d.UI.Options, UpdatedAt: now}
				if !validLocale(ui.Locale) {
					ui.Locale = "auto"
				}
				if ui.Mode != "editing" {
					ui.Mode = "reading"
				}
				if _, err := tx.NewInsert().Model(&ui).On("CONFLICT (user_id) DO UPDATE").
					Set("locale = EXCLUDED.locale").Set("mode = EXCLUDED.mode").Set("options = EXCLUDED.options").Set("updated_at = EXCLUDED.updated_at").Exec(ctx); err != nil {
					return err
				}
				res.Settings++
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return res, nil
}

func validLocale(l string) bool {
	switch l {
	case "auto", "en", "ru", "uk":
		return true
	}
	return false
}

func chapterIDs(ctx context.Context, tx bun.Tx, seriesID int64) (map[string]int64, error) {
	var rows []struct {
		ID     int64  `bun:"id"`
		Number string `bun:"number_key"`
	}
	if err := tx.NewSelect().Table("chapters").Column("id", "number_key").Where("series_id = ?", seriesID).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		out[r.Number] = r.ID
	}
	return out, nil
}

func importProgress(ctx context.Context, tx bun.Tx, userID int64, ser *model.Series, chapters map[string]int64, progress []ExportProgress, res *DataImportResult) error {
	var have []model.TitleReadState
	if err := tx.NewSelect().Model(&have).Where("user_id = ? AND title_id = ?", userID, readstate.TitleID(ser)).Scan(ctx); err != nil {
		return err
	}
	current := map[string]model.TitleReadState{}
	for _, h := range have {
		current[h.NumberKey] = h
	}
	for _, p := range progress {
		id, ok := chapters[p.Number]
		if !ok {
			res.Missing++
			continue
		}
		if h, ok := current[p.Number]; ok && (h.Completed || !p.Completed && h.Page >= p.Page) {
			res.Kept++
			continue
		}
		st := &model.ChapterReadState{UserID: userID, ChapterID: id, Completed: p.Completed, Page: max(p.Page, 0), ReadAt: p.ReadAt, SyncedAt: p.At, Origin: model.ReadOriginBackup}
		if st.SyncedAt.IsZero() {
			st.SyncedAt = time.Now().UTC()
		}
		if err := readstate.Save(ctx, tx, st); err != nil {
			return err
		}
		res.Progress++
	}
	return nil
}

func importHistory(ctx context.Context, tx bun.Tx, userID, seriesID int64, chapters map[string]int64, t *ExportTitle, res *DataImportResult) error {
	for _, e := range t.History {
		id, ok := chapters[e.Number]
		if !ok {
			continue
		}
		n, err := tx.NewSelect().Model((*model.ReadEvent)(nil)).Where("user_id = ? AND chapter_id = ? AND at = ?", userID, id, e.At).Count(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		ev := &model.ReadEvent{UserID: userID, SeriesID: seriesID, ChapterID: id, Chapters: max(e.Chapters, 1), Completed: e.Completed, Page: e.Page,
			Origin: e.Origin, Client: e.Client, Device: e.Device, Outcome: e.Outcome, At: e.At}
		if ev.Origin == "" {
			ev.Origin = model.ReadOriginBackup
		}
		if _, err := tx.NewInsert().Model(ev).Exec(ctx); err != nil {
			return err
		}
		res.History++
	}
	for _, se := range t.Sessions {
		id, ok := chapters[se.Number]
		if !ok || se.Seconds < 0 {
			continue
		}
		// the same session imported twice gets the same id
		key := sha256.Sum256(fmt.Appendf(nil, "%d/%d/%s/%d", userID, id, se.Started.UTC().Format(time.RFC3339Nano), se.Seconds))
		row := &model.ReadingSession{ID: fmt.Sprintf("import-%x", key[:12]), UserID: userID, SeriesID: seriesID, ChapterID: id,
			ActiveSeconds: se.Seconds, StartedAt: se.Started, UpdatedAt: se.Updated}
		r, err := tx.NewInsert().Model(row).On("CONFLICT DO NOTHING").Exec(ctx)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.Sessions++
		}
	}
	return nil
}

func saveReaderPrefs(ctx context.Context, tx bun.Tx, userID, seriesID int64, data json.RawMessage, now time.Time) error {
	if !json.Valid(data) || !strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		return nil
	}
	_, err := tx.NewInsert().Model(&model.ReaderPrefs{UserID: userID, SeriesID: seriesID, Data: string(data), UpdatedAt: now}).
		On("CONFLICT (user_id, series_id) DO UPDATE").Set("data = EXCLUDED.data").Set("updated_at = EXCLUDED.updated_at").Exec(ctx)
	return err
}

// Summary counts what an export holds.
type Summary struct {
	Titles   int  `json:"titles"`
	Read     int  `json:"read" doc:"Chapters read"`
	History  int  `json:"history"`
	Follows  int  `json:"follows"`
	Settings bool `json:"settings"`
}

// Summarize counts an export's contents.
func Summarize(d *Export) Summary {
	out := Summary{Titles: len(d.Titles), Settings: d.UI != nil || len(d.Reader) > 0}
	for _, t := range d.Titles {
		for _, p := range t.Progress {
			if p.Completed {
				out.Read++
			}
		}
		out.History += len(t.History)
		if t.Following {
			out.Follows++
		}
		out.Settings = out.Settings || len(t.Reader) > 0
	}
	return out
}

// withReached adds the chapters a title's reading position covers to its
// progress: every chapter numbered below it read, the page in it kept.
func withReached(ctx context.Context, tx bun.IDB, seriesID int64, t *ExportTitle, chapters map[string]int64) []ExportProgress {
	r := t.Reached
	if r == nil {
		return t.Progress
	}
	if r.Chapter < 0 {
		n, ok := readstate.ChapterNumberByRef(ctx, tx, seriesID, r.ChapterRef)
		if !ok {
			return t.Progress
		}
		resolved := *r
		resolved.Chapter = n
		r = &resolved
	}
	out := append([]ExportProgress(nil), t.Progress...)
	now := time.Now().UTC()
	for key := range chapters {
		n, err := strconv.ParseFloat(key, 64)
		switch {
		case err != nil || n < 0:
		case n < r.Chapter-0.0005:
			out = append(out, ExportProgress{Number: key, Completed: true, At: now})
		case r.Page > 0 && math.Abs(n-r.Chapter) < 0.001:
			out = append(out, ExportProgress{Number: key, Page: r.Page, At: now})
		}
	}
	return out
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// FromBackup turns an app backup or a site account (read into the same
// shape) into an export of the user's own data: titles with their sources,
// tracker ids, lists, follows and progress.
func FromBackup(b *backupimport.Backup, user string) *Export {
	d := &Export{Format: Format, Version: Version, Exported: b.CreatedAt, User: user, Titles: []ExportTitle{}}
	if d.Exported.IsZero() {
		d.Exported = time.Now().UTC()
	}
	for _, e := range b.Entries {
		t := ExportTitle{Title: e.Title, AltTitles: e.AltTitles, Following: e.Favorite, Lists: e.Categories,
			Sources: []ExportSource{{SourceID: e.SourceID, Name: firstNonBlank(e.SourceName, b.Sources[e.SourceID]), URL: e.URL}}}
		if len(e.Trackers) > 0 {
			t.IDs = map[string]string{}
			for k, v := range e.Trackers {
				t.IDs[k] = v
			}
		}
		for _, c := range e.Chapters {
			if c.Number < 0 || (!c.Read && c.LastPageRead <= 0) {
				continue
			}
			p := ExportProgress{Number: chapternum.Key(c.Number), Completed: c.Read, ReadAt: c.ReadAt, At: d.Exported}
			if !c.Read {
				p.Page = c.LastPageRead + 1 // 1-based like the reader
			}
			t.Progress = append(t.Progress, p)
		}
		if e.ReadUpTo != nil {
			t.Reached = &ExportReached{Chapter: e.ReadUpTo.Chapter, Page: e.ReadUpTo.Page, ChapterRef: e.ReadUpTo.ChapterRef}
		}
		d.Titles = append(d.Titles, t)
	}
	return d
}
