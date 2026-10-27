package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/readinglists"
	"github.com/Asion001/mangarr/internal/readstate"
)

func init() { register((*Server).registerReadingLists) }

// ReadingListsResponse is a user's statuses and lists with how many titles
// on this server each holds.
type ReadingListsResponse struct {
	Statuses []ReadingStatusCount `json:"statuses"`
	Lists    []ReadingListCount   `json:"lists"`
}

type ReadingStatusCount struct {
	Status string `json:"status" enum:"reading,planned,completed,onhold,dropped"`
	Count  int    `json:"count"`
}

type ReadingListCount struct {
	model.ReadingList
	Count int `json:"count"`
}

// ReadingListOwner is a user who has titles in a status or a list.
type ReadingListOwner struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
}

type readingListName struct {
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"100"`
	}
}

func readingListError(err error) error {
	switch {
	case errors.Is(err, readinglists.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, readinglists.ErrBuiltIn), errors.Is(err, readinglists.ErrName):
		return huma.Error400BadRequest(err.Error())
	}
	return toHTTPError(err)
}

// readingLists returns a user's statuses and lists with their counts.
func (s *Server) readingLists(ctx context.Context, userID int64) (*ReadingListsResponse, error) {
	lists, err := readinglists.Lists(ctx, s.app.DB, userID)
	if err != nil {
		return nil, err
	}
	counts, err := readinglists.Counts(ctx, s.app.DB, userID)
	if err != nil {
		return nil, err
	}
	out := &ReadingListsResponse{Statuses: []ReadingStatusCount{}, Lists: []ReadingListCount{}}
	for _, st := range model.ReadingStatuses {
		out.Statuses = append(out.Statuses, ReadingStatusCount{Status: st, Count: counts[st]})
	}
	for _, l := range lists {
		out.Lists = append(out.Lists, ReadingListCount{ReadingList: l, Count: counts[strconv.FormatInt(l.ID, 10)]})
	}
	return out, nil
}

// titleOf returns a visible series' title id.
func (s *Server) titleOf(ctx context.Context, seriesID int64) (int64, error) {
	ser, err := s.visibleSeries(ctx, seriesID)
	if err != nil {
		return 0, err
	}
	return readstate.TitleID(ser), nil
}

func (s *Server) registerReadingLists() {
	tags := []string{"Reading lists"}
	huma.Register(s.api, huma.Operation{OperationID: "me-lists", Method: http.MethodGet, Path: "/api/v1/me/lists", Tags: tags,
		Summary: "Your reading statuses and lists, with how many titles each holds"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *ReadingListsResponse }, error) {
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			out, err := s.readingLists(ctx, userID)
			return &struct{ Body *ReadingListsResponse }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-list-create", Method: http.MethodPost, Path: "/api/v1/me/lists", Tags: tags,
		Summary: "Make a list (or get yours of that name)"},
		func(ctx context.Context, in *readingListName) (*struct{ Body *model.ReadingList }, error) {
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			l, err := readinglists.Create(ctx, s.app.DB, userID, in.Body.Name)
			if err != nil {
				return nil, readingListError(err)
			}
			return &struct{ Body *model.ReadingList }{l}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-list-rename", Method: http.MethodPut, Path: "/api/v1/me/lists/{id}", Tags: tags,
		Summary: "Rename a list you made"},
		func(ctx context.Context, in *struct {
			IDPath
			readingListName
		}) (*struct{ Body *model.ReadingList }, error) {
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			l, err := readinglists.Rename(ctx, s.app.DB, userID, in.ID, in.Body.Name)
			if err != nil {
				return nil, readingListError(err)
			}
			return &struct{ Body *model.ReadingList }{l}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-list-delete", Method: http.MethodDelete, Path: "/api/v1/me/lists/{id}", Tags: tags,
		Summary: "Delete a list you made (its titles stay in the library)"},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			return nil, readingListError(readinglists.Delete(ctx, s.app.DB, userID, in.ID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-lists", Method: http.MethodGet, Path: "/api/v1/series/{id}/lists", Tags: tags,
		Summary: "Your status and lists for a series' title"},
		func(ctx context.Context, in *IDPath) (*struct{ Body readinglists.TitleLists }, error) {
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			titleID, err := s.titleOf(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			e, err := readinglists.Get(ctx, s.app.DB, userID, titleID)
			return &struct{ Body readinglists.TitleLists }{e}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-lists-save", Method: http.MethodPut, Path: "/api/v1/series/{id}/lists", Tags: tags,
		Summary: "Set your status and lists for a series' title (every edition shares them)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body readinglists.TitleLists
		}) (*struct{ Body readinglists.TitleLists }, error) {
			userID, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			titleID, err := s.titleOf(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			if !readinglists.ValidStatus(in.Body.Status) {
				return nil, huma.Error400BadRequest("unknown status")
			}
			if err := readinglists.Set(ctx, s.app.DB, userID, titleID, in.Body); err != nil {
				return nil, readingListError(err)
			}
			e, err := readinglists.Get(ctx, s.app.DB, userID, titleID)
			s.app.Bus.Changed("reading", "lists", in.ID)
			return &struct{ Body readinglists.TitleLists }{e}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "reading-list-owners", Method: http.MethodGet, Path: "/api/v1/reading-lists/owners", Tags: tags,
		Summary: "Users with titles in a status or a list, to filter the library by theirs"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []ReadingListOwner }, error) {
			out := []ReadingListOwner{}
			err := s.app.DB.NewRaw(`SELECT u.id, u.username, u.display_name FROM users AS u
WHERE u.id IN (SELECT user_id FROM title_statuses) OR u.id IN (SELECT l.user_id FROM reading_lists AS l JOIN reading_list_titles AS t ON t.list_id = l.id)
ORDER BY LOWER(u.username)`).Scan(ctx, &out)
			return &struct{ Body []ReadingListOwner }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "user-lists", Method: http.MethodGet, Path: "/api/v1/users/{id}/lists", Tags: tags,
		Summary: "A user's reading statuses and lists, with how many titles each holds"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *ReadingListsResponse }, error) {
			if n, err := s.app.DB.NewSelect().Model((*model.User)(nil)).Where("id = ?", in.ID).Count(ctx); err != nil || n == 0 {
				return nil, huma.Error404NotFound("user not found")
			}
			out, err := s.readingLists(ctx, in.ID)
			return &struct{ Body *ReadingListsResponse }{out}, toHTTPError(err)
		})
}

// listFilter returns the titles the search's list filter keeps (nil: no
// filter). Whose lists count is the caller's, or with listUser another
// user's for people who manage the library.
func (s *Server) listFilter(ctx context.Context, list string, listUser int64) (map[int64]bool, error) {
	if list == "" {
		return nil, nil
	}
	userID, err := s.userOf(ctx)
	if err != nil {
		return nil, toHTTPError(err)
	}
	if listUser > 0 && listUser != userID {
		if !access.From(ctx).Can(access.LibraryManage) {
			return nil, huma.Error403Forbidden("only people who manage the library can filter by someone else's lists")
		}
		userID = listUser
	}
	titles, ok, err := readinglists.Titles(ctx, s.app.DB, userID, list)
	if err != nil {
		return nil, toHTTPError(err)
	}
	if !ok {
		return nil, huma.Error400BadRequest("list is a status or a list's id")
	}
	return titles, nil
}
