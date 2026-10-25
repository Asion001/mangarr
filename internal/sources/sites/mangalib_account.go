package sites

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Asion001/mangarr/internal/sources/sourcekit"
)

// A MangaLib account's lists are its bookmark folders, which the site shows
// to anyone unless a folder is made private, so the account is read from the
// profile's address and needs no sign-in. A bookmark keeps the chapter
// reached by the site's chapter id (and the page in it).

// mangalibMaxPages stops a runaway listing (the site pages by 25).
const mangalibMaxPages = 400

var mangalibUserID = regexp.MustCompile(`(?:/user/|^)(\d+)(?:[/?#]|$)`)

func (m *mangalib) AccountHelp() sourcekit.AccountHelp {
	return sourcekit.AccountHelp{Field: "Profile link", Steps: []string{
		"Open your profile on mangalib.me and copy its address.",
		"Lists you made private aren't read: make them public in the list's settings first.",
	}}
}

func (m *mangalib) ReadAccount(ctx context.Context, profile string) (sourcekit.Account, error) {
	found := mangalibUserID.FindStringSubmatch(strings.TrimSpace(profile))
	if found == nil {
		return sourcekit.Account{}, fmt.Errorf("%w: paste the profile's address, like %s/ru/user/12345", sourcekit.ErrAccount, m.site)
	}
	user := found[1]
	var who struct {
		Data struct {
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/user/" + user, Headers: m.headers()}, &who); err != nil {
		var se *sourcekit.StatusError
		if asStatusError(err, &se) && se.Code == 404 {
			return sourcekit.Account{}, fmt.Errorf("%w: there is no user %s", sourcekit.ErrAccount, user)
		}
		return sourcekit.Account{}, err
	}
	var folders struct {
		Data []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Public    bool   `json:"public"`
			Abandoned bool   `json:"is_abandoned"`
			Count     int    `json:"count"`
			SiteIDs   []int  `json:"site_ids"`
		} `json:"data"`
	}
	if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/bookmarks/folder/" + user, Headers: m.headers()}, &folders); err != nil {
		return sourcekit.Account{}, err
	}
	acc := sourcekit.Account{User: firstNonBlank(who.Data.Username, user)}
	for _, f := range folders.Data {
		if !slices.Contains(f.SiteIDs, mangalibSiteID) || !f.Public {
			continue
		}
		id := strconv.FormatInt(f.ID, 10)
		acc.Lists = append(acc.Lists, sourcekit.AccountList{ID: id, Name: f.Name, Status: mangalibListStatus(f.ID, f.Abandoned)})
		if f.Count == 0 {
			continue
		}
		entries, err := m.folder(ctx, user, id, f.Count)
		if err != nil {
			return sourcekit.Account{}, fmt.Errorf("read the list %s: %w", f.Name, err)
		}
		acc.Entries = append(acc.Entries, entries...)
	}
	return acc, nil
}

// folder reads one list. The site lists at most 100 bookmarks however it is
// asked, so a longer list is read again in the other order to reach the
// rest (up to 200).
func (m *mangalib) folder(ctx context.Context, user, id string, count int) ([]sourcekit.AccountEntry, error) {
	var entries []sourcekit.AccountEntry
	seen := map[int64]bool{}
	for _, order := range []string{"desc", "asc"} {
		for page := 1; page <= mangalibMaxPages && len(seen) < count; page++ {
			var out struct {
				Data []struct {
					ItemID   int64  `json:"item_id"`
					Progress string `json:"progress"`
					Meta     *struct {
						// ItemNumber is the chapter's place in the title's list
						ItemNumber int `json:"item_number"`
					} `json:"meta"`
					Media mangalibMedia `json:"media"`
				} `json:"data"`
				Meta struct {
					Next any `json:"next_page_url"`
				} `json:"meta"`
			}
			q := url.Values{"status": {id}, "user_id": {user}, "page": {strconv.Itoa(page)}, "sort_type": {order}}
			if err := m.c.JSON(ctx, sourcekit.Request{URL: m.api + "/api/bookmarks", Query: q, Headers: m.headers()}, &out); err != nil {
				return nil, err
			}
			for _, b := range out.Data {
				if b.Media.SlugURL == "" || seen[b.Media.ID] {
					continue
				}
				seen[b.Media.ID] = true
				if (b.Media.Model != "" && b.Media.Model != "manga") || (b.Media.Site != 0 && b.Media.Site != mangalibSiteID) {
					continue // a novel, an anime or another lib site's title
				}
				e := sourcekit.AccountEntry{Manga: m.manga(b.Media.mangalibManga), Status: mangalibStatus(b.Media.Status.ID, b.Media.Status.Label),
					Lists: []string{id}, Chapter: -1}
				for _, t := range []string{b.Media.Name, b.Media.RusName, b.Media.EngName} {
					if t = strings.TrimSpace(t); t != "" && t != e.Title && !slices.Contains(e.AltTitles, t) {
						e.AltTitles = append(e.AltTitles, t)
					}
				}
				if b.ItemID > 0 {
					e.ChapterRef = strconv.FormatInt(b.ItemID, 10)
					if b.Meta != nil && b.Meta.ItemNumber > 0 {
						e.ChapterPlace = b.Meta.ItemNumber
					}
					if p, err := strconv.Atoi(strings.TrimSpace(b.Progress)); err == nil && p > 0 {
						e.Page = p
					}
				}
				entries = append(entries, e)
			}
			if len(out.Data) == 0 || out.Meta.Next == nil || out.Meta.Next == false {
				break
			}
		}
		if len(seen) >= count {
			break
		}
	}
	return entries, nil
}

// mangalibMedia is a bookmark's title.
type mangalibMedia struct {
	mangalibManga
	Model string `json:"model"`
	Site  int    `json:"site"`
}

// mangalibListStatus maps the site's built-in folders (the same ids for
// every user) to list statuses.
func mangalibListStatus(id int64, abandoned bool) string {
	if abandoned {
		return sourcekit.ListDropped
	}
	switch id {
	case 1:
		return sourcekit.ListReading
	case 2:
		return sourcekit.ListPlanned
	case 4:
		return sourcekit.ListCompleted
	case 5:
		return sourcekit.ListFavorite
	}
	return ""
}

func asStatusError(err error, out **sourcekit.StatusError) bool {
	for err != nil {
		if se, ok := err.(*sourcekit.StatusError); ok {
			*out = se
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
