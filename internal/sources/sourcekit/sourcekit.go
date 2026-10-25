// Package sourcekit is the toolkit for mangarr's own source sites: the Site
// interface a site implements, the types it speaks in, and the HTTP helpers
// it uses. It deliberately depends on nothing else in mangarr, so the sites
// (and this kit) can move to a repository of their own.
package sourcekit

import (
	"context"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnsupported means a site can't do this (browse, preferences, ...).
var ErrUnsupported = errors.New("not supported by this site")

// ErrNotFound means the manga or chapter is gone from the site.
var ErrNotFound = errors.New("not found at the site")

// Info describes a site. ID must stay stable forever: it identifies the
// catalog in every library, and matching the id the Mihon/Tachiyomi
// extension for the same site uses keeps backup imports working.
type Info struct {
	ID      string
	Name    string
	Lang    string
	BaseURL string
	NSFW    bool
	// SupportsBrowse: the site implements Browser (popular and latest).
	SupportsBrowse bool
	IconURL        string
}

// Ref identifies a manga at a site by its path or URL ("/manga/<id>").
type Ref struct {
	URL string
	// ID is the site's own id for the manga, kept alongside the URL for APIs
	// that only speak ids. mangarr stores it, but a site must still work from
	// the URL alone: a stored id can be missing or stale.
	ID string
	// Title helps a site re-find a manga whose URL changed.
	Title string
}

// Manga is one search or browse result.
type Manga struct {
	URL string
	// ID is the site's own id, when it has one (see Ref.ID).
	ID       string
	Title    string
	CoverURL string
	// Chapters is the chapter count when the site already knows it.
	Chapters *int
}

// Status values a site may report.
const (
	StatusUnknown   = "unknown"
	StatusOngoing   = "ongoing"
	StatusCompleted = "completed"
	StatusHiatus    = "hiatus"
	StatusCancelled = "cancelled"
)

// Details is everything a site knows about one manga.
type Details struct {
	Manga
	Author      string
	Artist      string
	Description string
	Genres      []string
	Status      string
	WebURL      string
}

// Chapter is one chapter of a manga.
type Chapter struct {
	URL string
	// ID is the site's own id for the chapter (see Ref.ID).
	ID        string
	Name      string
	Scanlator string
	// Number as the site reports it (-1 when it doesn't).
	Number     float64
	UploadedAt *time.Time
	WebURL     string
}

// PageImage is one page: where to get it, and what a request for it needs.
// Headers matter when the site checks the Referer, and they let another
// machine (a worker) fetch the page itself.
type PageImage struct {
	Index   int
	URL     string
	Headers map[string]string
	// Decode is what the site needs to turn the downloaded bytes into the
	// real image (a scramble seed, a key), for sites that implement Decoder.
	// A page with it can only be fetched through mangarr, not by a worker.
	Decode string
}

// Results is one page of results.
type Results struct {
	Mangas  []Manga
	HasNext bool
}

// Has reports whether a result for this manga is already in the list, so a
// site scraping a page with repeated links doesn't return it twice.
func (r *Results) Has(url string) bool {
	for _, m := range r.Mangas {
		if m.URL == url {
			return true
		}
	}
	return false
}

// PageRef says which chapter to list pages for. It carries the manga as
// well, because several sites address a chapter as (manga, chapter) and
// nothing else.
type PageRef struct {
	Manga Ref
	URL   string
	ID    string
}

// Site is what a site implements. Everything optional is a separate
// interface, so a small site stays small.
type Site interface {
	Info() Info
	Search(ctx context.Context, query string, page int) (Results, error)
	Details(ctx context.Context, ref Ref) (Details, error)
	Chapters(ctx context.Context, ref Ref) ([]Chapter, error)
	Pages(ctx context.Context, ch PageRef) ([]PageImage, error)
}

// Decoder is implemented by sites whose images arrive scrambled or
// encrypted. DecodePage gets the downloaded bytes of a page whose Decode was
// set, and returns the image to keep.
type Decoder interface {
	DecodePage(ctx context.Context, decode string, data []byte) ([]byte, error)
}

// Browser is implemented by sites with popular and latest listings.
type Browser interface {
	Popular(ctx context.Context, page int) (Results, error)
	Latest(ctx context.Context, page int) (Results, error)
}

// Configurable is implemented by sites with their own options (a language, a
// content rating). Values are strings, bools or numbers.
type Configurable interface {
	Options() []Option
	SetOption(key string, value any) error
}

// Option is one of a site's settings.
type Option struct {
	Key     string
	Title   string
	Help    string
	Type    string // switch, text, select, multiselect
	Value   any
	Choices []Choice
}

// Choice is one value of a select option.
type Choice struct {
	Value string
	Label string
}

// Politeness is how hard a site may be hit; the core turns it into limits.
type Politeness struct {
	RequestsPerMinute int
	MaxConcurrent     int
}

// Polite is implemented by sites that know their own limits.
type Polite interface {
	Politeness() Politeness
}

// ErrUnauthorized means the site turned an account's sign-in token away.
var ErrUnauthorized = errors.New("the site didn't accept the sign-in token")

// ErrAccount means the account asked for can't be read (no such user, an
// address that isn't a profile); the error says why.
var ErrAccount = errors.New("the account can't be read")

// AccountReader is implemented by sites whose accounts keep manga lists and
// reading progress that can be brought into a library.
type AccountReader interface {
	// AccountHelp says what to paste to sign in and where to find it.
	AccountHelp() AccountHelp
	// ReadAccount reads the account's manga lists with the progress in
	// each title. The token is only used for this call.
	ReadAccount(ctx context.Context, token string) (Account, error)
}

// AccountHelp tells a user how to sign in to read their account.
type AccountHelp struct {
	// Field names what is pasted ("Sign-in token").
	Field string
	// Secret is set when what is pasted signs in as the person (a token),
	// so it is typed in a password field.
	Secret bool
	// Steps say where to find it, one step each.
	Steps []string
}

// List statuses, for lists that carry one.
const (
	ListReading   = "reading"
	ListPlanned   = "planned"
	ListCompleted = "completed"
	ListFavorite  = "favorite"
	ListOnHold    = "onhold"
	ListDropped   = "dropped"
)

// Account is what a site account holds.
type Account struct {
	// User is the account's name at the site.
	User    string
	Lists   []AccountList
	Entries []AccountEntry
}

// AccountList is one of the account's lists.
type AccountList struct {
	ID   string
	Name string
	// Status is one of the List* values, or empty for a list of the user's own.
	Status string
}

// AccountEntry is one manga in the account with how far it was read.
type AccountEntry struct {
	Manga
	// Status is the manga's status (Status*).
	Status string
	// AltTitles are its other titles (other languages, the original).
	AltTitles []string
	// Lists are the ids of the lists it is in.
	Lists []string
	// Chapter is the number of the chapter reached (-1 when not started):
	// chapters numbered below it are read, and it is open at Page (1-based,
	// 0 when not started).
	Chapter float64
	Page    int
	// ChapterRef is the site's id of the chapter reached, for sites that
	// keep that instead of its number (Chapter is then -1).
	ChapterRef string
	// ChapterPlace is that chapter's place in the title's list (1-based),
	// when the site says: the last resort for placing it.
	ChapterPlace int
}

// KeiyoushiID is the id Mihon and Tachiyomi give an extension: the first
// eight bytes of md5("<name lowercased>/<lang>/<version>"), big-endian and
// made positive. A site declared with the same name, language and version as
// the Keiyoushi extension for it therefore gets the same id, which is what
// keeps a Mihon backup import linking here instead of to an unknown catalog.
func KeiyoushiID(name, lang string, version int) string {
	sum := md5.Sum([]byte(strings.ToLower(name) + "/" + lang + "/" + strconv.Itoa(version)))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])&math.MaxInt64, 10)
}

// Builder makes a site. Sites register one in init().
type Builder func(deps Deps) Site

// Deps are what the core gives a site: an HTTP client that already carries a
// cookie jar, a browser user agent and the challenge solver.
type Deps struct {
	Client *Client
}

var (
	mu       sync.Mutex
	builders = map[string]Builder{}
)

// Register adds a site to the registry (called from a site's init).
// Registering the same id twice panics: ids must be unique and stable.
func Register(id string, b Builder) {
	mu.Lock()
	defer mu.Unlock()
	if _, dup := builders[id]; dup {
		panic("sourcekit: site " + id + " registered twice")
	}
	builders[id] = b
}

// RegisterLangs registers a site that serves several languages as one
// catalog per language, each with the id Keiyoushi gives that language's
// source (see KeiyoushiID), so every language links up on a backup import.
// langs are Keiyoushi's codes ("en", "pt-BR", "es-419"); the builder gets
// one and reports it as Info().Lang.
func RegisterLangs(name string, version int, langs []string, b func(deps Deps, lang string) Site) {
	for _, lang := range langs {
		Register(KeiyoushiID(name, lang, version), func(d Deps) Site { return b(d, lang) })
	}
}

// Build makes every registered site.
func Build(d Deps) []Site {
	mu.Lock()
	ids := make([]string, 0, len(builders))
	for id := range builders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Site, 0, len(ids))
	for _, id := range ids {
		out = append(out, builders[id](d))
	}
	mu.Unlock()
	return out
}

// Registered lists the ids of every known site.
func Registered() []string {
	mu.Lock()
	defer mu.Unlock()
	ids := make([]string, 0, len(builders))
	for id := range builders {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
