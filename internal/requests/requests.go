// Package requests lets users ask for series (like Jellyseerr): they pick a
// metadata result, and managers add it (or a group's requests are added
// automatically when Quick search finds a confident source). A request is
// approved once the series is in the library and available when its first
// chapter is imported; the people who asked follow the series and hear
// about each step.
package requests

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/metadataagg"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
	"github.com/Asion001/mangarr/internal/modules/metadata"
	"github.com/Asion001/mangarr/internal/modules/notify"
	"github.com/Asion001/mangarr/internal/series"
	"github.com/Asion001/mangarr/internal/sourcesearch"
)

var ErrNotFound = errors.New("request not found")

// AvailableError: the series is already in the library.
type AvailableError struct{ SeriesID int64 }

func (e AvailableError) Error() string { return "already in the library" }

type Service struct {
	DB     *db.DB
	Bus    *events.Bus
	Mods   *modules.Manager
	Series *series.Service
	Search *sourcesearch.Service
	Log    *slog.Logger
	// DropImport deletes the import a declined kind "import" request stands for.
	DropImport func(ctx context.Context, importID int64) error
	// QueueSearch searches for a series' missing chapters (in the background).
	QueueSearch func(ctx context.Context, seriesID int64) error
	// Tell sends users a message on their own notification targets.
	Tell func(users []int64, event string, seriesID int64, msg notify.Message)
	// ctx outlives requests (automatic adds run after the call returns).
	ctx context.Context
}

// Start follows the library to move requests along.
func (s *Service) Start(ctx context.Context) error {
	s.ctx = ctx
	s.Bus.Subscribe(func(e events.Event) {
		go s.seriesAdded(e.SeriesID)
	}, events.SeriesAdded)
	s.Bus.Subscribe(func(e events.Event) {
		go s.chapterImported(e.SeriesID)
	}, events.ChapterImported)
	return nil
}

func (s *Service) context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

// Requester is someone who asked.
type Requester struct {
	UserID int64     `json:"userId"`
	Name   string    `json:"name"`
	Note   string    `json:"note,omitempty"`
	At     time.Time `json:"at"`
}

// Request is a request as the API shows it.
type Request struct {
	model.Request
	SeriesTitle string `json:"seriesTitle,omitempty"`
	// EditionOf is a series of the title a new language is asked for.
	EditionOf  int64       `json:"editionOf,omitempty"`
	HandledBy  string      `json:"handledBy,omitempty"`
	Requesters []Requester `json:"requesters"`
	// Count is how many people asked (Requesters may list only you).
	Count int `json:"count"`
	// Mine: you asked for it.
	Mine bool `json:"mine"`
	// Import counts the titles of a kind "import" request.
	Import *ImportSummary `json:"import,omitempty"`
}

// ImportSummary is how far a user's library import is.
type ImportSummary struct {
	Titles   int `json:"titles"`
	Imported int `json:"imported"`
	// Lists counts the titles per list they were in.
	Lists map[string]int `json:"lists"`
}

// Lookup fetches a metadata result (the source of truth for a request).
func (s *Service) Lookup(ctx context.Context, moduleID int64, id string) (*model.RequestMetadata, string, error) {
	mod, def, err := modules.GetAs[metadata.Module](s.Mods, moduleID)
	if err != nil {
		return nil, "", fmt.Errorf("metadata module: %w", err)
	}
	md, err := mod.Get(ctx, id)
	if err != nil {
		return nil, "", err
	}
	ids := map[string]string{}
	for k, v := range md.ExternalIDs {
		ids[k] = v
	}
	provider := md.Provider
	if provider == "" {
		provider = def.Implementation
	}
	ids[provider] = md.ID
	desc := md.Description
	if len(desc) > 2000 {
		desc = desc[:2000]
	}
	return &model.RequestMetadata{ModuleID: moduleID, Provider: provider, ID: md.ID, AltTitles: md.AltTitles, Year: md.Year,
		Format: md.Format, Status: md.Status, Description: desc, CoverURL: md.CoverURL, URL: md.URL, Genres: md.Genres,
		Adult: md.Adult, ExternalIDs: ids}, md.Title, nil
}

// sameSeries reports whether two sets of external ids name the same series
// (MAL ids are skipped: providers disagree on them).
func sameSeries(a, b map[string]string) bool {
	for k, v := range a {
		if k != "mal" && v != "" && b[k] == v {
			return true
		}
	}
	return false
}

// ValidationError is a request that can't be made as asked.
type ValidationError struct{ Msg string }

func (e ValidationError) Error() string { return e.Msg }

// CreateInput is what someone asks for: a metadata result (ModuleID and
// MetaID) or a title in the library (SeriesID), in a language or in the
// server's default languages.
type CreateInput struct {
	ModuleID int64
	MetaID   string
	SeriesID int64
	Language string
	Note     string
	// Monitor asks for SeriesID's chapters that aren't monitored to be
	// downloaded.
	Monitor bool
}

// fromSeries describes a title in the library the way a metadata result
// would (the module and id when an active metadata module knows it).
func (s *Service) fromSeries(ser *model.Series) *model.RequestMetadata {
	m := ser.Metadata
	md := &model.RequestMetadata{AltTitles: m.AltTitles, Year: m.Year, Format: m.Format, Description: m.Description,
		CoverURL: m.CoverURL, Genres: m.Genres, ExternalIDs: m.ExternalIDs}
	for _, mod := range modules.ActiveAs[metadata.Module](s.Mods, modules.KindMetadata) {
		if id := m.ExternalIDs[mod.Def.Implementation]; id != "" {
			md.ModuleID, md.Provider, md.ID = mod.Def.ID, mod.Def.Implementation, id
			break
		}
	}
	return md
}

// Create asks for a series, or for another language of one. Asking for
// what someone already asked for joins their request.
func (s *Service) Create(ctx context.Context, p *access.Principal, in CreateInput) (*Request, bool, error) {
	if p == nil || p.Kind != access.KindUser {
		return nil, false, errors.New("sign in as a user to request series")
	}
	if in.Monitor {
		return s.createMonitor(ctx, p, in)
	}
	lang := strings.ToLower(strings.TrimSpace(in.Language))
	if lang != "" {
		_, offered, err := s.Series.Offered(ctx)
		if err != nil {
			return nil, false, err
		}
		if !slices.Contains(offered, lang) {
			return nil, false, ValidationError{"this server isn't set up for that language"}
		}
	}
	var existing []model.Series
	if err := s.DB.NewSelect().Model(&existing).Column("id", "title", "metadata", "tags", "root_folder_id", "work_id", "language").Where("preview = ?", false).Scan(ctx); err != nil {
		return nil, false, err
	}
	var md *model.RequestMetadata
	var title string
	var workID *int64
	if in.SeriesID > 0 {
		// another language of a title in the library
		i := slices.IndexFunc(existing, func(x model.Series) bool { return x.ID == in.SeriesID })
		if i < 0 || !p.Sees(&existing[i]) {
			return nil, false, ErrNotFound
		}
		if lang == "" {
			return nil, false, ValidationError{"choose a language"}
		}
		ser := existing[i]
		var full model.Series
		if err := s.DB.NewSelect().Model(&full).Where("id = ?", ser.ID).Scan(ctx); err != nil {
			return nil, false, err
		}
		md, title = s.fromSeries(&full), full.Title
		if ser.WorkID > 0 {
			id := ser.WorkID
			workID = &id
		}
	} else {
		var err error
		if md, title, err = s.Lookup(ctx, in.ModuleID, in.MetaID); err != nil {
			return nil, false, err
		}
		if metadata.IsNovel(md.Format) {
			return nil, false, ValidationError{"that's a novel, and manga sources have no chapters of novels: ask for its manga instead"}
		}
	}
	for i := range existing {
		ser := &existing[i]
		same := sameSeries(md.ExternalIDs, ser.Metadata.ExternalIDs) || (workID != nil && ser.WorkID == *workID) || ser.ID == in.SeriesID
		// one they can't see still gets a request: a manager may widen their access
		if !same || !p.Sees(ser) {
			continue
		}
		if lang == "" || strings.EqualFold(ser.Language, lang) {
			return nil, false, AvailableError{ser.ID}
		}
		if workID == nil && ser.WorkID > 0 {
			id := ser.WorkID
			workID = &id
		}
	}
	note := trimNote(in.Note)
	now := time.Now().UTC()
	var open []model.Request
	if err := s.DB.NewSelect().Model(&open).Where("status IN (?)", bun.In([]string{model.RequestPending, model.RequestApproved})).Scan(ctx); err != nil {
		return nil, false, err
	}
	for _, r := range open {
		same := sameSeries(md.ExternalIDs, r.Metadata.ExternalIDs) || (workID != nil && r.WorkID != nil && *r.WorkID == *workID)
		if same && r.Language == lang && r.Kind == "" {
			ru := &model.RequestUser{RequestID: r.ID, UserID: p.UserID, Note: note, CreatedAt: now}
			if _, err := s.DB.NewInsert().Model(ru).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
				return nil, false, err
			}
			if r.SeriesID != nil {
				s.follow(ctx, *r.SeriesID, p.UserID)
			}
			s.Bus.Changed("request", "updated", r.ID)
			v, err := s.Get(ctx, p, r.ID)
			return v, true, err
		}
	}
	r := &model.Request{Title: title, Metadata: *md, Language: lang, WorkID: workID, Status: model.RequestPending, CreatedAt: now, UpdatedAt: now}
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(r).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&model.RequestUser{RequestID: r.ID, UserID: p.UserID, Note: note, CreatedAt: now}).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	who := p.DisplayName
	if who == "" {
		who = p.Username
	}
	body := who + " asked for " + title
	if md.Year > 0 {
		body += fmt.Sprintf(" (%d)", md.Year)
	}
	if lang != "" {
		body += " in " + lang
	}
	if note != "" {
		body += ": " + note
	}
	s.Bus.Publish(events.Event{Type: events.RequestCreated, Payload: events.MessagePayload{Title: "New request: " + title, Message: body}})
	s.Bus.Changed("request", "created", r.ID)
	if s.autoApproves(ctx, p.GroupID) {
		go s.autoAdd(r.ID)
	}
	v, err := s.Get(ctx, p, r.ID)
	return v, false, err
}

func trimNote(note string) string {
	note = strings.TrimSpace(note)
	if len(note) > 500 {
		note = strings.ToValidUTF8(note[:500], "")
	}
	return note
}

// waiting counts a series' chapters that aren't monitored or downloaded.
func (s *Service) waiting(ctx context.Context, seriesID int64) (int, error) {
	n, err := s.DB.NewSelect().Model((*model.Chapter)(nil)).Where("series_id = ?", seriesID).Where("monitored = ?", false).
		Where("file_id IS NULL").Where("state <> ?", model.ChapterCleaned).Count(ctx)
	return int(n), err
}

// createMonitor asks for a title in the library to be downloaded: its
// chapters that aren't monitored yet.
func (s *Service) createMonitor(ctx context.Context, p *access.Principal, in CreateInput) (*Request, bool, error) {
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Where("id = ?", in.SeriesID).Where("preview = ?", false).Scan(ctx); err != nil || !p.Sees(&ser) {
		return nil, false, ErrNotFound
	}
	n, err := s.waiting(ctx, ser.ID)
	if err != nil {
		return nil, false, err
	}
	if n == 0 {
		return nil, false, ValidationError{"every chapter of this title is already downloaded or on its way"}
	}
	note := trimNote(in.Note)
	now := time.Now().UTC()
	var open model.Request
	err = s.DB.NewSelect().Model(&open).Where("kind = ? AND series_id = ? AND status = ?", model.RequestKindMonitor, ser.ID, model.RequestPending).Limit(1).Scan(ctx)
	if err == nil {
		if _, err := s.DB.NewInsert().Model(&model.RequestUser{RequestID: open.ID, UserID: p.UserID, Note: note, CreatedAt: now}).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
			return nil, false, err
		}
		s.follow(ctx, ser.ID, p.UserID)
		s.Bus.Changed("request", "updated", open.ID)
		v, err := s.Get(ctx, p, open.ID)
		return v, true, err
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	id := ser.ID
	r := &model.Request{Title: ser.Title, Metadata: *s.fromSeries(&ser), Kind: model.RequestKindMonitor, Language: ser.Language, SeriesID: &id,
		Status: model.RequestPending, CreatedAt: now, UpdatedAt: now}
	err = s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(r).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&model.RequestUser{RequestID: r.ID, UserID: p.UserID, Note: note, CreatedAt: now}).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	s.follow(ctx, ser.ID, p.UserID)
	who := p.DisplayName
	if who == "" {
		who = p.Username
	}
	body := fmt.Sprintf("%s asked to download %s (%d chapters not monitored)", who, ser.Title, n)
	if note != "" {
		body += ": " + note
	}
	s.Bus.Publish(events.Event{Type: events.RequestCreated, Payload: events.MessagePayload{Title: "Download request: " + ser.Title, Message: body}})
	s.Bus.Changed("request", "created", r.ID)
	if s.autoApproves(ctx, p.GroupID) {
		go s.autoAdd(r.ID)
	}
	v, err := s.Get(ctx, p, r.ID)
	return v, false, err
}

// Monitor fulfils a download request: the title and its chapters that
// aren't downloaded are monitored, the missing ones searched, and the
// request approved. handler is who did it (nil: automatic).
func (s *Service) Monitor(ctx context.Context, id int64, handler *access.Principal) error {
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return notFound(err)
	}
	if r.Kind != model.RequestKindMonitor || r.SeriesID == nil {
		return ValidationError{"not a download request"}
	}
	seriesID := *r.SeriesID
	now := time.Now().UTC()
	if _, err := s.DB.NewUpdate().Model((*model.Series)(nil)).Set("monitored = ?", true).Set("updated_at = ?", now).Where("id = ?", seriesID).Exec(ctx); err != nil {
		return err
	}
	if _, err := s.DB.NewUpdate().Model((*model.Chapter)(nil)).Set("monitored = ?", true).Set("updated_at = ?", now).
		Where("series_id = ? AND monitored = ? AND file_id IS NULL AND state <> ?", seriesID, false, model.ChapterCleaned).Exec(ctx); err != nil {
		return err
	}
	s.Bus.Changed("series", "updated", seriesID)
	if s.QueueSearch != nil {
		if err := s.QueueSearch(ctx, seriesID); err != nil {
			return err
		}
	}
	return s.Link(ctx, id, seriesID, handler)
}

func (s *Service) autoApproves(ctx context.Context, groupID int64) bool {
	var g model.Group
	err := s.DB.NewSelect().Model(&g).Column("auto_approve_requests").Where("id = ?", groupID).Scan(ctx)
	return err == nil && g.AutoApproveRequests
}

// autoAdd adds a request's series when Quick search finds a confident
// source; otherwise it waits for a manager.
func (s *Service) autoAdd(id int64) {
	ctx, cancel := context.WithTimeout(s.context(), 3*time.Minute)
	defer cancel()
	s.add(ctx, id, nil)
}

// SetLanguage changes the language pending requests ask for.
func (s *Service) SetLanguage(ctx context.Context, ids []int64, lang string) (int, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	if lang != "" {
		_, offered, err := s.Series.Offered(ctx)
		if err != nil {
			return 0, err
		}
		if !slices.Contains(offered, lang) {
			return 0, ValidationError{"this server isn't set up for that language"}
		}
	}
	res, err := s.DB.NewUpdate().Model((*model.Request)(nil)).Set("language = ?", lang).Set("reason = ''").Set("updated_at = ?", time.Now().UTC()).
		Where("id IN (?) AND status = ? AND kind = ''", bun.In(ids), model.RequestPending).Exec(ctx)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	for _, id := range ids {
		s.Bus.Changed("request", "updated", id)
	}
	return int(n), nil
}

// add adds a pending request's series with the sources Quick search is
// confident about, reporting whether it was added.
func (s *Service) add(ctx context.Context, id int64, handler *access.Principal) bool {
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil || r.Status != model.RequestPending {
		return false
	}
	if r.Kind == model.RequestKindImport {
		return false // handled on the Import page
	}
	if r.Kind == model.RequestKindMonitor {
		if err := s.Monitor(ctx, id, handler); err != nil {
			s.attemptFailed(ctx, id, "Automatic download failed.", err)
			return false
		}
		return true
	}
	if r.WorkID == nil && metadata.IsNovel(r.Metadata.Format) {
		// sources would find the manga by its name and file it under the novel
		s.attemptFailed(ctx, id, "This request is for a novel; link it to its manga's series or decline it.", nil)
		return false
	}
	langs, err := s.Series.Languages(ctx)
	if r.Language != "" {
		langs, err = []string{r.Language}, nil
	}
	if err != nil || len(langs) == 0 {
		s.Log.Info("request waits for a manager: no language to add it in", "request", r.Title)
		s.attemptFailed(ctx, id, "Set a language on a root folder (or default search languages) so requests know where to go; choose sources and retry Add.", err)
		return false
	}
	// each language's edition gets every source of its default list that
	// has the title, in list order: the first is primary, the rest fallbacks
	var links []series.SourceLink
	var names []string
	found, err := s.Search.Defaults(ctx, sourcesearch.DefaultsInput{Query: r.Title, Titles: append([]string{r.Title}, r.Metadata.AltTitles...), Langs: langs})
	if err == nil {
		for _, ed := range found.Editions {
			for _, src := range ed.Sources {
				m := src.Match
				if m == nil {
					continue
				}
				links = append(links, series.SourceLink{ModuleID: m.ModuleID, SourceID: m.SourceID, URL: m.Manga.URL, EngineRef: m.Manga.EngineRef,
					Title: m.Manga.Title, SourceName: m.SourceName, Lang: firstLang(m.Lang, ed.Lang)})
				names = append(names, m.SourceName)
			}
		}
	}
	if len(links) == 0 {
		s.Log.Info("request waits for a manager: no confident source", "request", r.Title, "languages", langs)
		s.attemptFailed(ctx, id, "No confident source match was found; choose a source and retry Add.", nil)
		return false
	}
	add := series.AddEditionsRequest{Sources: links, Monitor: model.MonitorAll, MonitorNew: model.MonitorAll, SearchMissing: true}
	if r.WorkID != nil {
		add.WorkID = *r.WorkID // a new language joins the title
	} else if r.Metadata.ID != "" {
		add.Metadata = &metadataagg.Ref{ModuleID: r.Metadata.ModuleID, Provider: r.Metadata.Provider, ID: r.Metadata.ID}
	}
	if add.WorkID == 0 && add.Metadata == nil {
		s.attemptFailed(ctx, id, "This request has no metadata to add it by; choose sources and add it.", nil)
		return false
	}
	res, err := s.Series.AddEditions(ctx, add)
	if err != nil && (res == nil || len(res.Editions) == 0) {
		s.Log.Warn("request waits for a manager: couldn't add it", "request", r.Title, "err", err)
		s.attemptFailed(ctx, id, "Automatic add failed.", err)
		return false
	}
	if err != nil {
		s.Log.Warn("request added in some languages only", "request", r.Title, "err", err)
	}
	ser := res.Editions[0]
	s.Log.Info("request added automatically", "request", r.Title, "series", ser.ID, "sources", names)
	if err := s.Link(ctx, id, ser.ID, handler); err != nil {
		s.Log.Warn("request added but could not be linked", "request", r.Title, "series", ser.ID, "err", err)
		s.attemptFailed(ctx, id, fmt.Sprintf("Series %d was added, but request fulfilment failed; use Link to retry.", ser.ID), err)
	}
	return true
}

// firstLang keeps a catalog's own language unless it spans several.
func firstLang(catalog, searched string) string {
	if l := strings.ToLower(strings.TrimSpace(catalog)); l != "" && l != "all" && l != "multi" {
		return l
	}
	return searched
}

func (s *Service) attemptFailed(ctx context.Context, id int64, message string, cause error) {
	if cause != nil {
		message += " " + cause.Error()
	}
	message = strings.TrimSpace(message)
	if len(message) > 500 {
		// cut on a rune boundary: half a character is invalid UTF-8 to Postgres
		message = strings.ToValidUTF8(message[:500], "")
	}
	_, _ = s.DB.NewUpdate().Model((*model.Request)(nil)).Set("reason = ?", message).Set("updated_at = ?", time.Now().UTC()).
		Where("id = ? AND status = ?", id, model.RequestPending).Exec(ctx)
	s.Bus.Changed("request", "updated", id)
}

// Link marks a request fulfilled by a series (approved, or available when it
// already has chapters). handler is who did it (nil: automatic).
func (s *Service) Link(ctx context.Context, id, seriesID int64, handler *access.Principal) error {
	_, err := s.link(ctx, id, seriesID, handler, true)
	return err
}

// link links a request to a series, telling its requesters when tell is
// set; it reports whether the request changed.
func (s *Service) link(ctx context.Context, id, seriesID int64, handler *access.Principal, tell bool) (bool, error) {
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return false, notFound(err)
	}
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Column("id", "title").Where("id = ?", seriesID).Scan(ctx); err != nil {
		return false, fmt.Errorf("series: %w", notFound(err))
	}
	files, err := s.DB.NewSelect().Model((*model.ChapterFile)(nil)).Where("series_id = ?", seriesID).Count(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	r.SeriesID, r.HandledAt, r.UpdatedAt, r.Reason = &seriesID, &now, now, ""
	r.Status = model.RequestApproved
	// a download request waits for the chapters it asked for
	if files > 0 && r.Kind != model.RequestKindMonitor {
		r.Status, r.AvailableAt = model.RequestAvailable, &now
	}
	var handledBy *int64
	if handler != nil && handler.Kind == access.KindUser {
		handledBy = &handler.UserID
	}
	r.HandledBy = handledBy
	// linking twice (the add hook and whoever added it) tells people once
	res, err := s.DB.NewUpdate().Model(&r).Column("series_id", "status", "reason", "handled_by", "handled_at", "available_at", "updated_at").
		Where("id = ?", id).Where("NOT (series_id IS NOT NULL AND series_id = ? AND status IN (?))", seriesID, bun.In([]string{model.RequestApproved, model.RequestAvailable})).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if handledBy != nil {
			_, err = s.DB.NewUpdate().Model((*model.Request)(nil)).Set("handled_by = ?", *handledBy).Where("id = ? AND handled_by IS NULL", id).Exec(ctx)
		}
		return false, err
	}
	users := s.requesters(ctx, id)
	for _, u := range users {
		s.follow(ctx, seriesID, u)
	}
	s.Bus.Changed("request", "updated", id)
	if !tell {
		return true, nil
	}
	if r.Kind == model.RequestKindMonitor {
		s.tell(users, seriesID, "Downloading: "+ser.Title, "The chapters you asked for are being downloaded; you'll hear when they arrive.")
	} else if r.Status == model.RequestAvailable {
		s.tell(users, seriesID, "Available: "+ser.Title, "The series you asked for is in the library.")
	} else {
		s.tell(users, seriesID, "Approved: "+ser.Title, "The series you asked for was added; you'll hear when its first chapter arrives.")
	}
	return true, nil
}

// Decline turns a request down.
func (s *Service) Decline(ctx context.Context, id int64, reason string, handler *access.Principal) error {
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return notFound(err)
	}
	now := time.Now().UTC()
	r.Status, r.Reason, r.HandledAt, r.UpdatedAt = model.RequestDeclined, strings.TrimSpace(reason), &now, now
	if handler != nil && handler.Kind == access.KindUser {
		r.HandledBy = &handler.UserID
	}
	if _, err := s.DB.NewUpdate().Model(&r).Column("status", "reason", "handled_by", "handled_at", "updated_at").WherePK().Exec(ctx); err != nil {
		return err
	}
	s.Bus.Changed("request", "updated", id)
	if r.Kind == model.RequestKindImport && r.ImportID != nil && s.DropImport != nil {
		if err := s.DropImport(ctx, *r.ImportID); err != nil {
			s.Log.Warn("drop the import of a declined request", "import", *r.ImportID, "err", err)
		}
	}
	body := "Your request was declined."
	if r.Reason != "" {
		body = "Your request was declined: " + r.Reason
	}
	s.tell(s.requesters(ctx, id), 0, "Declined: "+r.Title, body)
	return nil
}

// ForBatch loads the pending requests among ids that can be handled in an
// import (titles and languages, not downloads or users' imports).
func (s *Service) ForBatch(ctx context.Context, ids []int64) ([]model.Request, error) {
	var rs []model.Request
	err := s.DB.NewSelect().Model(&rs).Where("id IN (?) AND status = ? AND kind = '' AND import_id IS NULL", bun.In(ids), model.RequestPending).
		Order("created_at").Scan(ctx)
	return rs, err
}

// Names lists who asked for each request, by request id.
func (s *Service) Names(ctx context.Context, ids []int64) (map[int64][]string, error) {
	var rows []struct {
		RequestID   int64  `bun:"request_id"`
		Username    string `bun:"username"`
		DisplayName string `bun:"display_name"`
	}
	err := s.DB.NewSelect().TableExpr("request_users AS ru").ColumnExpr("ru.request_id, u.username, u.display_name").
		Join("JOIN users AS u ON u.id = ru.user_id").Where("ru.request_id IN (?)", bun.In(ids)).OrderExpr("ru.created_at").Scan(ctx, &rows)
	out := map[int64][]string{}
	for _, r := range rows {
		name := r.Username
		if r.DisplayName != "" {
			name = r.DisplayName
		}
		out[r.RequestID] = append(out[r.RequestID], name)
	}
	return out, err
}

// InBatch marks requests as handled in an import.
func (s *Service) InBatch(ctx context.Context, ids []int64, importID int64) error {
	if _, err := s.DB.NewUpdate().Model((*model.Request)(nil)).Set("import_id = ?", importID).Set("reason = ''").Set("updated_at = ?", time.Now().UTC()).
		Where("id IN (?)", bun.In(ids)).Exec(ctx); err != nil {
		return err
	}
	for _, id := range ids {
		s.Bus.Changed("request", "updated", id)
	}
	return nil
}

// BatchAdded links the requests an import of requests added to their
// series, quietly, then (when the import says so) tells each requester once
// which of their requests were added.
func (s *Service) BatchAdded(ctx context.Context, importID int64, handler *access.Principal) error {
	var imp model.Import
	if err := s.DB.NewSelect().Model(&imp).Where("id = ?", importID).Scan(ctx); err != nil {
		return notFound(err)
	}
	var entries []model.ImportEntry
	if err := s.DB.NewSelect().Model(&entries).Where("import_id = ? AND state = ? AND series_id IS NOT NULL", importID, model.EntryImported).
		Order("position").Scan(ctx); err != nil {
		return err
	}
	added := map[int64][]string{}
	var users []int64
	for _, e := range entries {
		if e.Data.RequestID == 0 {
			continue
		}
		changed, err := s.link(ctx, e.Data.RequestID, *e.SeriesID, handler, false)
		if err != nil {
			s.Log.Warn("link a request added in a batch", "request", e.Data.RequestID, "err", err)
			continue
		}
		if !changed {
			continue
		}
		for _, u := range s.requesters(ctx, e.Data.RequestID) {
			if len(added[u]) == 0 {
				users = append(users, u)
			}
			added[u] = append(added[u], e.Title)
		}
	}
	if !imp.Options.TellRequesters {
		return nil
	}
	for _, u := range users {
		titles := added[u]
		body := titles[0] + " was added to the library."
		if len(titles) > 1 {
			body = strings.Join(titles, ", ") + " were added to the library."
		}
		if len(titles) > 3 {
			body = fmt.Sprintf("%s and %d more were added to the library.", strings.Join(titles[:3], ", "), len(titles)-3)
		}
		s.tell([]int64{u}, 0, fmt.Sprintf("Added: %d of your requests", len(titles)), body)
	}
	return nil
}

// CreateImport makes the one request that stands for a user's library
// import (its titles are reviewed on the Import page).
func (s *Service) CreateImport(ctx context.Context, p *access.Principal, importID int64, title string, titles int) (*Request, error) {
	now := time.Now().UTC()
	r := &model.Request{Title: title, Kind: model.RequestKindImport, ImportID: &importID, Status: model.RequestPending, CreatedAt: now, UpdatedAt: now}
	err := s.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewInsert().Model(r).Exec(ctx); err != nil {
			return err
		}
		_, err := tx.NewInsert().Model(&model.RequestUser{RequestID: r.ID, UserID: p.UserID, CreatedAt: now}).Exec(ctx)
		return err
	})
	if err != nil {
		return nil, err
	}
	who := p.DisplayName
	if who == "" {
		who = p.Username
	}
	s.Bus.Publish(events.Event{Type: events.RequestCreated, Payload: events.MessagePayload{Title: "New request: " + title,
		Message: fmt.Sprintf("%s asked for %d titles from their library; review them under Import.", who, titles)}})
	s.Bus.Changed("request", "created", r.ID)
	return s.Get(ctx, p, r.ID)
}

// ImportAdded marks the request of a user's import approved once some of
// its titles are added, and tells the user.
func (s *Service) ImportAdded(ctx context.Context, importID int64, added int, handler *access.Principal) error {
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("import_id = ?", importID).Limit(1).Scan(ctx); err != nil {
		return notFound(err)
	}
	if added == 0 {
		return nil
	}
	now := time.Now().UTC()
	if r.Status == model.RequestPending {
		r.Status, r.Reason, r.HandledAt = model.RequestApproved, "", &now
		if handler != nil && handler.Kind == access.KindUser {
			r.HandledBy = &handler.UserID
		}
	}
	r.UpdatedAt = now
	if _, err := s.DB.NewUpdate().Model(&r).Column("status", "reason", "handled_by", "handled_at", "updated_at").WherePK().Exec(ctx); err != nil {
		return err
	}
	s.Bus.Changed("request", "updated", r.ID)
	s.tell(s.requesters(ctx, r.ID), 0, "Added from your library", fmt.Sprintf("%d of the titles you asked for were added, with your progress.", added))
	return nil
}

// Withdraw takes a user's name off a request; a pending request nobody
// asks for any more goes away.
func (s *Service) Withdraw(ctx context.Context, id, userID int64) error {
	res, err := s.DB.NewDelete().Model((*model.RequestUser)(nil)).Where("request_id = ? AND user_id = ?", id, userID).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	left, err := s.DB.NewSelect().Model((*model.RequestUser)(nil)).Where("request_id = ?", id).Count(ctx)
	if err != nil {
		return err
	}
	if left == 0 {
		_, err = s.DB.NewDelete().Model((*model.Request)(nil)).Where("id = ? AND status = ?", id, model.RequestPending).Exec(ctx)
	}
	s.Bus.Changed("request", "deleted", id)
	return err
}

// Delete removes a request.
func (s *Service) Delete(ctx context.Context, id int64) error {
	res, err := s.DB.NewDelete().Model((*model.Request)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	s.Bus.Changed("request", "deleted", id)
	return nil
}

// Filter selects requests to list.
type Filter struct {
	Status string
	// All lists everyone's (managers); otherwise only the caller's.
	All bool
}

// Manages reports whether p handles requests.
func Manages(p *access.Principal) bool {
	return p.Can(access.RequestsManage) || p.Can(access.LibraryManage)
}

// asks is whose requests these are (0 for callers without an account).
func asks(p *access.Principal) int64 {
	if p == nil {
		return 0
	}
	return p.UserID
}

// List returns requests, newest first.
func (s *Service) List(ctx context.Context, p *access.Principal, f Filter) ([]Request, error) {
	q := s.DB.NewSelect().Model((*model.Request)(nil)).Order("created_at DESC", "id DESC").Limit(5000)
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if !f.All || !Manages(p) {
		q = q.Where("id IN (SELECT request_id FROM request_users WHERE user_id = ?)", asks(p))
	}
	var rs []model.Request
	if err := q.Scan(ctx, &rs); err != nil {
		return nil, err
	}
	return s.views(ctx, p, rs)
}

// Get loads one request the caller may see.
func (s *Service) Get(ctx context.Context, p *access.Principal, id int64) (*Request, error) {
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return nil, notFound(err)
	}
	vs, err := s.views(ctx, p, []model.Request{r})
	if err != nil {
		return nil, err
	}
	if !vs[0].Mine && !Manages(p) {
		return nil, ErrNotFound
	}
	return &vs[0], nil
}

// Pending counts requests waiting for a manager.
func (s *Service) Pending(ctx context.Context) (int, error) {
	n, err := s.DB.NewSelect().Model((*model.Request)(nil)).Where("status = ?", model.RequestPending).Count(ctx)
	return int(n), err
}

func (s *Service) views(ctx context.Context, p *access.Principal, rs []model.Request) ([]Request, error) {
	out := make([]Request, len(rs))
	if len(rs) == 0 {
		return out, nil
	}
	ids := make([]int64, len(rs))
	seriesIDs, userIDs := []int64{}, []int64{}
	for i, r := range rs {
		ids[i] = r.ID
		if r.SeriesID != nil {
			seriesIDs = append(seriesIDs, *r.SeriesID)
		}
		if r.HandledBy != nil {
			userIDs = append(userIDs, *r.HandledBy)
		}
	}
	var rus []model.RequestUser
	if err := s.DB.NewSelect().Model(&rus).Where("request_id IN (?)", bun.In(ids)).Order("created_at").Scan(ctx); err != nil {
		return nil, err
	}
	for _, ru := range rus {
		userIDs = append(userIDs, ru.UserID)
	}
	names := map[int64]string{}
	if len(userIDs) > 0 {
		var us []model.User
		if err := s.DB.NewSelect().Model(&us).Column("id", "username", "display_name").Where("id IN (?)", bun.In(userIDs)).Scan(ctx); err != nil {
			return nil, err
		}
		for _, u := range us {
			names[u.ID] = u.Username
			if u.DisplayName != "" {
				names[u.ID] = u.DisplayName
			}
		}
	}
	editions := map[int64]int64{}
	var workIDs []int64
	for _, r := range rs {
		if r.WorkID != nil {
			workIDs = append(workIDs, *r.WorkID)
		}
	}
	if len(workIDs) > 0 {
		var ss []model.Series
		if err := s.DB.NewSelect().Model(&ss).Column("id", "work_id").Where("work_id IN (?)", bun.In(workIDs)).Where("preview = ?", false).Order("id").Scan(ctx); err != nil {
			return nil, err
		}
		for _, x := range ss {
			if editions[x.WorkID] == 0 {
				editions[x.WorkID] = x.ID
			}
		}
	}
	imports := map[int64]*ImportSummary{}
	var importIDs []int64
	for _, r := range rs {
		if r.ImportID != nil {
			importIDs = append(importIDs, *r.ImportID)
		}
	}
	if len(importIDs) > 0 {
		var es []model.ImportEntry
		if err := s.DB.NewSelect().Model(&es).Column("import_id", "state", "data").Where("import_id IN (?)", bun.In(importIDs)).Scan(ctx); err != nil {
			return nil, err
		}
		for _, e := range es {
			sum := imports[e.ImportID]
			if sum == nil {
				sum = &ImportSummary{Lists: map[string]int{}}
				imports[e.ImportID] = sum
			}
			sum.Titles++
			if e.State == model.EntryImported {
				sum.Imported++
			}
			for _, c := range e.Data.Categories {
				sum.Lists[c]++
			}
		}
	}
	titles := map[int64]string{}
	if len(seriesIDs) > 0 {
		var ss []model.Series
		if err := s.DB.NewSelect().Model(&ss).Column("id", "title").Where("id IN (?)", bun.In(seriesIDs)).Scan(ctx); err != nil {
			return nil, err
		}
		for _, x := range ss {
			titles[x.ID] = x.Title
		}
	}
	manager := Manages(p)
	for i, r := range rs {
		v := Request{Request: r, Requesters: []Requester{}}
		if r.SeriesID != nil {
			v.SeriesTitle = titles[*r.SeriesID]
		}
		if r.WorkID != nil {
			v.EditionOf = editions[*r.WorkID]
		}
		if r.HandledBy != nil {
			v.HandledBy = names[*r.HandledBy]
		}
		if r.ImportID != nil {
			v.Import = imports[*r.ImportID]
			if v.Import == nil {
				v.Import = &ImportSummary{Lists: map[string]int{}}
			}
		}
		for _, ru := range rus {
			if ru.RequestID != r.ID {
				continue
			}
			v.Count++
			mine := ru.UserID == asks(p) && ru.UserID != 0
			v.Mine = v.Mine || mine
			if manager || mine { // others' names are theirs
				v.Requesters = append(v.Requesters, Requester{UserID: ru.UserID, Name: names[ru.UserID], Note: ru.Note, At: ru.CreatedAt})
			}
		}
		out[i] = v
	}
	return out, nil
}

// seriesAdded links pending requests for the same series.
func (s *Service) seriesAdded(seriesID int64) {
	ctx := s.context()
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Column("id", "metadata", "work_id", "language").Where("id = ?", seriesID).Scan(ctx); err != nil {
		return
	}
	var pending []model.Request
	if err := s.DB.NewSelect().Model(&pending).Where("status = ?", model.RequestPending).Scan(ctx); err != nil {
		return
	}
	for _, r := range pending {
		if r.Kind == model.RequestKindMonitor || r.ImportID != nil {
			continue // handled in an import (BatchAdded)
		}
		same := sameSeries(r.Metadata.ExternalIDs, ser.Metadata.ExternalIDs) || (r.WorkID != nil && ser.WorkID == *r.WorkID)
		// a request for one language waits for that language's edition
		if same && (r.Language == "" || strings.EqualFold(r.Language, ser.Language)) {
			if err := s.Link(ctx, r.ID, seriesID, nil); err != nil {
				s.Log.Warn("link request", "request", r.ID, "err", err)
			}
		}
	}
}

// chapterImported makes approved requests for the series available.
func (s *Service) chapterImported(seriesID int64) {
	ctx := s.context()
	var rs []model.Request
	if err := s.DB.NewSelect().Model(&rs).Where("status = ? AND series_id = ?", model.RequestApproved, seriesID).Scan(ctx); err != nil {
		return
	}
	now := time.Now().UTC()
	for _, r := range rs {
		res, err := s.DB.NewUpdate().Model((*model.Request)(nil)).Set("status = ?", model.RequestAvailable).Set("available_at = ?", now).Set("updated_at = ?", now).
			Where("id = ? AND status = ?", r.ID, model.RequestApproved).Exec(ctx)
		if err != nil {
			continue
		}
		if n, _ := res.RowsAffected(); n == 0 {
			continue // someone else got there first
		}
		s.Bus.Changed("request", "updated", r.ID)
		if r.ImportID != nil {
			continue // added in a batch: told once, when it was added
		}
		title := r.Title
		var ser model.Series
		if err := s.DB.NewSelect().Model(&ser).Column("title").Where("id = ?", seriesID).Scan(ctx); err == nil {
			title = ser.Title
		}
		s.tell(s.requesters(ctx, r.ID), seriesID, "Available: "+title, "The first chapter of the series you asked for is here.")
	}
}

func (s *Service) requesters(ctx context.Context, id int64) []int64 {
	var users []int64
	_ = s.DB.NewSelect().Model((*model.RequestUser)(nil)).Column("user_id").Where("request_id = ?", id).Scan(ctx, &users)
	return users
}

func (s *Service) follow(ctx context.Context, seriesID, userID int64) {
	f := &model.Follow{UserID: userID, SeriesID: seriesID, CreatedAt: time.Now().UTC()}
	if _, err := s.DB.NewInsert().Model(f).On("CONFLICT DO NOTHING").Exec(ctx); err != nil {
		s.Log.Warn("follow", "series", seriesID, "user", userID, "err", err)
	}
}

func (s *Service) tell(users []int64, seriesID int64, title, body string) {
	if s.Tell != nil && len(users) > 0 {
		s.Tell(users, events.RequestUpdated, seriesID, notify.Message{Title: title, Body: body})
	}
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
