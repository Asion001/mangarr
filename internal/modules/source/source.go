// Package source defines the interface of source modules: engines that can
// search manga catalogs, list chapters and fetch page images. The Suwayomi
// module is one implementation; others (native scrapers, a custom JVM host,
// torrent/DDL indexers) can be added without touching the core.
package source

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
)

// SourceInfo describes one catalog (e.g. "MangaDex (EN)") offered by a module.
type SourceInfo struct {
	ID             string `json:"id"` // portable id (Mihon source id for Keiyoushi sources)
	Name           string `json:"name"`
	Lang           string `json:"lang"`
	DisplayName    string `json:"displayName"`
	SupportsLatest bool   `json:"supportsLatest"`
	NSFW           bool   `json:"nsfw"`
	IconURL        string `json:"iconUrl,omitempty"`
	Extension      string `json:"extension,omitempty"`
}

// MangaRef identifies a manga portably: (SourceID, URL). EngineRef is an
// optional engine-specific cache (e.g. a Suwayomi manga id) that may go stale.
type MangaRef struct {
	SourceID  string `json:"sourceId"`
	URL       string `json:"url"`
	EngineRef string `json:"engineRef,omitempty"`
	// TitleHint is used to re-link when the engine lost the manga.
	TitleHint string `json:"titleHint,omitempty"`
}

type Manga struct {
	MangaRef
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnailUrl,omitempty"`
	// ChapterCount is set when the engine already knows the chapter list
	// (nil = unknown; getting it needs a details request).
	ChapterCount *int `json:"chapterCount,omitempty"`
}

type MangaPage struct {
	Mangas  []Manga `json:"mangas"`
	HasNext bool    `json:"hasNext"`
}

const (
	StatusUnknown   = "unknown"
	StatusOngoing   = "ongoing"
	StatusCompleted = "completed"
	StatusHiatus    = "hiatus"
	StatusCancelled = "cancelled"
)

type MangaDetails struct {
	Manga
	Author      string   `json:"author,omitempty"`
	Artist      string   `json:"artist,omitempty"`
	Description string   `json:"description,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Status      string   `json:"status"`
	WebURL      string   `json:"webUrl,omitempty"`
	// Rating is the site's community score, nil when it shows none.
	Rating *model.Rating `json:"rating,omitempty"`
}

type Chapter struct {
	URL       string `json:"url"`
	EngineRef string `json:"engineRef,omitempty"`
	Name      string `json:"name"`
	Scanlator string `json:"scanlator,omitempty"`
	// Number as reported by the source (-1 when unknown).
	Number     float64    `json:"number"`
	UploadDate *time.Time `json:"uploadDate,omitempty"`
	WebURL     string     `json:"webUrl,omitempty"`
}

type ChapterRef struct {
	Manga     MangaRef `json:"manga"`
	URL       string   `json:"url"`
	EngineRef string   `json:"engineRef,omitempty"`
}

type Page struct {
	Index int    `json:"index"`
	URL   string `json:"url"`
	// SourceID is the catalog the page belongs to (set by the core).
	SourceID string `json:"sourceId,omitempty"`
}

// Module is the required interface of source modules.
type Module interface {
	modules.Instance
	// Sources lists the catalogs this module can query.
	Sources(ctx context.Context) ([]SourceInfo, error)
	// Search queries one catalog.
	Search(ctx context.Context, sourceID, query string, page int) (*MangaPage, error)
	// Manga fetches details and (optionally) the chapter list. The returned
	// details carry a refreshed EngineRef.
	Manga(ctx context.Context, ref MangaRef, withChapters bool) (*MangaDetails, []Chapter, error)
	// Pages lists the page images of a chapter.
	Pages(ctx context.Context, ref ChapterRef) ([]Page, error)
	// FetchPage downloads one page image. The caller closes the body.
	FetchPage(ctx context.Context, p Page) (body io.ReadCloser, contentType string, err error)
}

// Latest is implemented by modules that can list recently updated manga.
type Latest interface {
	Latest(ctx context.Context, sourceID string, page int) (*MangaPage, error)
	Popular(ctx context.Context, sourceID string, page int) (*MangaPage, error)
}

// Extension is an installable plug-in package (e.g. a Keiyoushi APK/JAR).
type Extension struct {
	Pkg         string `json:"pkg"`
	Name        string `json:"name"`
	Lang        string `json:"lang"`
	VersionName string `json:"versionName"`
	VersionCode int    `json:"versionCode"`
	Installed   bool   `json:"installed"`
	HasUpdate   bool   `json:"hasUpdate"`
	Obsolete    bool   `json:"obsolete"`
	NSFW        bool   `json:"nsfw"`
	IconURL     string `json:"iconUrl,omitempty"`
}

// ExtensionManager is implemented by modules whose catalogs come from
// installable extensions.
type ExtensionManager interface {
	Extensions(ctx context.Context, refresh bool) ([]Extension, error)
	InstallExtension(ctx context.Context, pkg string) error
	UpdateExtension(ctx context.Context, pkg string) error
	UninstallExtension(ctx context.Context, pkg string) error
	Stores(ctx context.Context) ([]string, error)
	AddStore(ctx context.Context, url string) error
	RemoveStore(ctx context.Context, url string) error
}

// Preference is one configurable option of a catalog (engine specific).
type Preference struct {
	Key          string   `json:"key"`
	Position     int      `json:"position"`
	Type         string   `json:"type"` // switch, checkbox, edittext, list, multiselect
	Title        string   `json:"title"`
	Summary      string   `json:"summary,omitempty"`
	Value        any      `json:"value"`
	Entries      []string `json:"entries,omitempty"`
	EntryValues  []string `json:"entryValues,omitempty"`
	Visible      bool     `json:"visible"`
	DefaultValue any      `json:"defaultValue,omitempty"`
}

// Preferences is implemented by modules exposing per-catalog settings.
type Preferences interface {
	SourcePreferences(ctx context.Context, sourceID string) ([]Preference, error)
	SetSourcePreference(ctx context.Context, sourceID string, position int, typ string, value any) error
}

var (
	// ErrNotFound means the manga/chapter no longer exists at the source.
	ErrNotFound = errors.New("not found at source")
	// ErrUnsupported means the module does not support the operation.
	ErrUnsupported = errors.New("operation not supported by this source module")
	// ErrUnauthorized means the site turned an account's sign-in token away.
	ErrUnauthorized = errors.New("the site didn't accept the sign-in token; copy a fresh one")
	// ErrAccount means the account asked for can't be read; the error says why.
	ErrAccount = errors.New("the account can't be read")
)

// PageRequest is everything needed to fetch a page image, so another
// process (a download worker) can do it instead of the server.
type PageRequest struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// Fetchable is implemented by modules whose pages another machine can fetch:
// the request must carry everything the site needs (referer, user agent,
// credentials). Modules without it are fetched through the server.
type Fetchable interface {
	PageRequest(ctx context.Context, p Page) (PageRequest, error)
}

// Thumbnails is implemented by modules that can proxy cover thumbnails.
type Thumbnails interface {
	Thumbnail(ctx context.Context, ref MangaRef) (body io.ReadCloser, contentType string, err error)
}

// Maintainer is implemented by modules with housekeeping (e.g. cache cleanup),
// called by the download manager when the queue is idle.
type Maintainer interface {
	Maintain(ctx context.Context) error
}

// Assets is implemented by modules whose icon/image URLs are relative paths
// on an internal server that browsers cannot reach; the API proxies them.
type Assets interface {
	FetchAsset(ctx context.Context, path string) (body io.ReadCloser, contentType string, err error)
}

// AutoUpdater is implemented by extension managers that may install updates automatically.
type AutoUpdater interface {
	AutoUpdateExtensions() bool
}

// Accounts is implemented by modules with sites whose user accounts keep
// manga lists and reading progress that can be imported.
type Accounts interface {
	// AccountSources lists the catalogs whose accounts can be read.
	AccountSources(ctx context.Context) ([]AccountSource, error)
	// ReadAccount reads an account's lists and progress. The token is used
	// for this call only and must not be kept.
	ReadAccount(ctx context.Context, sourceID, token string) (*Account, error)
}

// AccountSource is a catalog whose accounts can be read, with how to sign in.
type AccountSource struct {
	SourceID string `json:"sourceId"`
	Name     string `json:"name"`
	Lang     string `json:"lang"`
	// Field names what is pasted to sign in ("Sign-in token").
	Field string `json:"field"`
	// Secret: what is pasted signs in as the person (typed as a password).
	Secret bool `json:"secret"`
	// Steps say where to find it.
	Steps []string `json:"steps"`
}

// List statuses, for an account's lists that carry one.
const (
	ListReading   = "reading"
	ListPlanned   = "planned"
	ListCompleted = "completed"
	ListFavorite  = "favorite"
	ListOnHold    = "onhold"
	ListDropped   = "dropped"
)

// Account is a site account's manga lists and progress.
type Account struct {
	User    string         `json:"user"`
	Lists   []AccountList  `json:"lists"`
	Entries []AccountEntry `json:"entries"`
}

type AccountList struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Status is one of the List* values, or empty for the user's own list.
	Status string `json:"status,omitempty"`
}

// AccountEntry is one manga of an account with how far it was read.
type AccountEntry struct {
	Manga
	Status    string   `json:"status"`
	AltTitles []string `json:"altTitles,omitempty"`
	// Lists are the ids of the lists it is in.
	Lists []string `json:"lists,omitempty"`
	// Chapter is the number of the chapter reached (-1 when not started):
	// chapters numbered below it are read, and it is open at Page (1-based,
	// 0 when not started).
	Chapter float64 `json:"chapter"`
	Page    int     `json:"page,omitempty"`
	// ChapterRef is the site's id of the chapter reached, when the site keeps
	// that instead of its number (Chapter is then -1).
	ChapterRef string `json:"chapterRef,omitempty"`
	// ChapterPlace is that chapter's place in the title's list (1-based).
	ChapterPlace int `json:"chapterPlace,omitempty"`
}
