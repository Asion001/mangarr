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
	note := strings.TrimSpace(in.Note)
	if len(note) > 500 {
		note = note[:500]
	}
	now := time.Now().UTC()
	var open []model.Request
	if err := s.DB.NewSelect().Model(&open).Where("status IN (?)", bun.In([]string{model.RequestPending, model.RequestApproved})).Scan(ctx); err != nil {
		return nil, false, err
	}
	for _, r := range open {
		same := sameSeries(md.ExternalIDs, r.Metadata.ExternalIDs) || (workID != nil && r.WorkID != nil && *r.WorkID == *workID)
		if same && r.Language == lang {
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
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil || r.Status != model.RequestPending {
		return
	}
	langs, err := s.Series.Languages(ctx)
	if r.Language != "" {
		langs, err = []string{r.Language}, nil
	}
	if err != nil || len(langs) == 0 {
		s.Log.Info("request waits for a manager: no language to add it in", "request", r.Title)
		s.attemptFailed(ctx, id, "Set a language on a root folder (or default search languages) so requests know where to go; choose sources and retry Add.", err)
		return
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
		return
	}
	add := series.AddEditionsRequest{Sources: links, Monitor: model.MonitorAll, MonitorNew: model.MonitorAll, SearchMissing: true}
	if r.WorkID != nil {
		add.WorkID = *r.WorkID // a new language joins the title
	} else if r.Metadata.ID != "" {
		add.Metadata = &metadataagg.Ref{ModuleID: r.Metadata.ModuleID, Provider: r.Metadata.Provider, ID: r.Metadata.ID}
	}
	if add.WorkID == 0 && add.Metadata == nil {
		s.attemptFailed(ctx, id, "This request has no metadata to add it by; choose sources and add it.", nil)
		return
	}
	res, err := s.Series.AddEditions(ctx, add)
	if err != nil && (res == nil || len(res.Editions) == 0) {
		s.Log.Warn("request waits for a manager: couldn't add it", "request", r.Title, "err", err)
		s.attemptFailed(ctx, id, "Automatic add failed.", err)
		return
	}
	if err != nil {
		s.Log.Warn("request added in some languages only", "request", r.Title, "err", err)
	}
	ser := res.Editions[0]
	s.Log.Info("request added automatically", "request", r.Title, "series", ser.ID, "sources", names)
	if err := s.Link(ctx, id, ser.ID, nil); err != nil {
		s.Log.Warn("request added but could not be linked", "request", r.Title, "series", ser.ID, "err", err)
		s.attemptFailed(ctx, id, fmt.Sprintf("Series %d was added, but request fulfilment failed; use Link to retry.", ser.ID), err)
	}
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
	var r model.Request
	if err := s.DB.NewSelect().Model(&r).Where("id = ?", id).Scan(ctx); err != nil {
		return notFound(err)
	}
	var ser model.Series
	if err := s.DB.NewSelect().Model(&ser).Column("id", "title").Where("id = ?", seriesID).Scan(ctx); err != nil {
		return fmt.Errorf("series: %w", notFound(err))
	}
	files, err := s.DB.NewSelect().Model((*model.ChapterFile)(nil)).Where("series_id = ?", seriesID).Count(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	r.SeriesID, r.HandledAt, r.UpdatedAt, r.Reason = &seriesID, &now, now, ""
	r.Status = model.RequestApproved
	if files > 0 {
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
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		if handledBy != nil {
			_, err = s.DB.NewUpdate().Model((*model.Request)(nil)).Set("handled_by = ?", *handledBy).Where("id = ? AND handled_by IS NULL", id).Exec(ctx)
		}
		return err
	}
	users := s.requesters(ctx, id)
	for _, u := range users {
		s.follow(ctx, seriesID, u)
	}
	s.Bus.Changed("request", "updated", id)
	if r.Status == model.RequestAvailable {
		s.tell(users, seriesID, "Available: "+ser.Title, "The series you asked for is in the library.")
	} else {
		s.tell(users, seriesID, "Approved: "+ser.Title, "The series you asked for was added; you'll hear when its first chapter arrives.")
	}
	return nil
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
	body := "Your request was declined."
	if r.Reason != "" {
		body = "Your request was declined: " + r.Reason
	}
	s.tell(s.requesters(ctx, id), 0, "Declined: "+r.Title, body)
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
	q := s.DB.NewSelect().Model((*model.Request)(nil)).Order("created_at DESC", "id DESC").Limit(500)
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
	return s.DB.NewSelect().Model((*model.Request)(nil)).Where("status = ?", model.RequestPending).Count(ctx)
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
