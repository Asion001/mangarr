package sites

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// A Senkuro account's lists are its bookmark collections, and its progress
// is the bookmark itself: the chapter reached and the page in it. The site's
// reader moves the bookmark to the next chapter (page unset) once the last
// page of a chapter is read, so chapters numbered below it are read.
//
// The site signs in only through its own pages, so the token is the one its
// web app keeps in the access_token cookie.

const senkuroViewerQuery = `query fetchViewer {
  viewer {
    user { id slug name }
    userBookmarkCollections { id name entityType status system preference { name } }
  }
}`

const senkuroBookmarksQuery = `query fetchUserBookmarks($after: String, $userId: ID!) {
  userBookmarks(first: 20, after: $after, userId: $userId, subjectType: MANGA) {
    edges {
      node {
        entityId collectionId page number
        entity { id ... on Manga { id slug originalName { lang content } titles { lang content } mangaStatus: status cover { original { url } } } }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`

// senkuroMaxBookmarks stops a runaway listing (the site pages by 20).
const senkuroMaxBookmarks = 20000

func (s *senkuro) AccountHelp() sourcekit.AccountHelp {
	return sourcekit.AccountHelp{Field: "Sign-in token", Secret: true, Steps: []string{
		"Sign in at " + strings.TrimPrefix(senkuroWeb, "https://") + " in a computer browser.",
		"Open the developer tools (F12), then Application (Storage in Firefox), then Cookies, then the site.",
		"Copy the value of access_token and paste it here.",
	}}
}

// senkuroWeb is where people sign in (the site moved from senkuro.com).
const senkuroWeb = "https://senkuro.me"

func (s *senkuro) ReadAccount(ctx context.Context, token string) (sourcekit.Account, error) {
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer "))
	if token == "" {
		return sourcekit.Account{}, sourcekit.ErrUnauthorized
	}
	var viewer struct {
		Viewer *struct {
			User *struct {
				ID   string `json:"id"`
				Slug string `json:"slug"`
				Name string `json:"name"`
			} `json:"user"`
			Collections []struct {
				ID         string `json:"id"`
				Name       string `json:"name"`
				EntityType string `json:"entityType"`
				Status     string `json:"status"`
				System     bool   `json:"system"`
				Preference *struct {
					Name string `json:"name"`
				} `json:"preference"`
			} `json:"userBookmarkCollections"`
		} `json:"viewer"`
	}
	if err := s.queryAs(ctx, token, "fetchViewer", senkuroViewerQuery, nil, &viewer); err != nil {
		return sourcekit.Account{}, err
	}
	if viewer.Viewer == nil || viewer.Viewer.User == nil {
		return sourcekit.Account{}, sourcekit.ErrUnauthorized
	}
	u := viewer.Viewer.User
	acc := sourcekit.Account{User: firstNonBlank(u.Name, u.Slug)}
	for _, c := range viewer.Viewer.Collections {
		if !strings.EqualFold(c.EntityType, "MANGA") {
			continue
		}
		name := c.Name
		if c.Preference != nil && strings.TrimSpace(c.Preference.Name) != "" {
			name = strings.TrimSpace(c.Preference.Name)
		}
		l := sourcekit.AccountList{ID: c.ID, Name: name}
		if c.System {
			l.Status = senkuroListStatus(c.Status)
		}
		acc.Lists = append(acc.Lists, l)
	}

	var after string
	for len(acc.Entries) < senkuroMaxBookmarks {
		var out struct {
			Bookmarks struct {
				Edges []struct {
					Node struct {
						CollectionID string  `json:"collectionId"`
						Page         *int    `json:"page"`
						Number       *string `json:"number"`
						Entity       *struct {
							senkuroManga
							Status string `json:"mangaStatus"`
						} `json:"entity"`
					} `json:"node"`
				} `json:"edges"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"userBookmarks"`
		}
		vars := map[string]any{"userId": u.ID}
		if after != "" {
			vars["after"] = after
		}
		if err := s.queryAs(ctx, token, "fetchUserBookmarks", senkuroBookmarksQuery, vars, &out); err != nil {
			return sourcekit.Account{}, fmt.Errorf("read the bookmarks: %w", err)
		}
		for _, e := range out.Bookmarks.Edges {
			n := e.Node
			if n.Entity == nil || n.Entity.Slug == "" {
				continue // an anime or a title that is gone
			}
			entry := sourcekit.AccountEntry{Manga: s.manga(n.Entity.senkuroManga), Status: senkuroStatus(n.Entity.Status), Chapter: -1}
			for _, t := range append([]senkuroText{n.Entity.OriginalName}, n.Entity.Titles...) {
				if t.Content != "" && t.Content != entry.Title && !slices.Contains(entry.AltTitles, t.Content) {
					entry.AltTitles = append(entry.AltTitles, t.Content)
				}
			}
			if n.CollectionID != "" {
				entry.Lists = []string{n.CollectionID}
			}
			if n.Number != nil {
				if v, err := strconv.ParseFloat(strings.TrimSpace(*n.Number), 64); err == nil && v >= 0 {
					entry.Chapter = v
					if n.Page != nil && *n.Page > 0 {
						entry.Page = *n.Page
					}
				}
			}
			acc.Entries = append(acc.Entries, entry)
		}
		if !out.Bookmarks.PageInfo.HasNextPage || out.Bookmarks.PageInfo.EndCursor == "" || out.Bookmarks.PageInfo.EndCursor == after {
			break
		}
		after = out.Bookmarks.PageInfo.EndCursor
	}
	return acc, nil
}

func senkuroListStatus(s string) string {
	switch strings.ToUpper(s) {
	case "WATCHING", "READING":
		return sourcekit.ListReading
	case "PLANNING":
		return sourcekit.ListPlanned
	case "COMPLETED":
		return sourcekit.ListCompleted
	case "FAVORITED":
		return sourcekit.ListFavorite
	case "PAUSED":
		return sourcekit.ListOnHold
	case "DROPPED":
		return sourcekit.ListDropped
	}
	return ""
}

func firstNonBlank(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
