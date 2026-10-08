// Package metadata defines metadata provider modules (AniList, MangaUpdates,
// MangaDex, ComicVine, ...). The core queries all enabled providers by
// priority and merges their results (see internal/metadataagg).
package metadata

import (
	"context"
	"errors"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/modules"
)

// SeriesMetadata is what a provider knows about a series.
type SeriesMetadata struct {
	Provider      string            `json:"provider"` // implementation name, e.g. "anilist"
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	AltTitles     []string          `json:"altTitles,omitempty"`
	Description   string            `json:"description,omitempty"`
	Status        string            `json:"status,omitempty"` // ongoing/completed/hiatus/cancelled/unknown
	Year          int               `json:"year,omitempty"`
	Authors       []string          `json:"authors,omitempty"`
	Artists       []string          `json:"artists,omitempty"`
	Genres        []string          `json:"genres,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	Publisher     string            `json:"publisher,omitempty"`
	CoverURL      string            `json:"coverUrl,omitempty"`
	Links         map[string]string `json:"links,omitempty"`
	ExternalIDs   map[string]string `json:"externalIds,omitempty"` // cross ids: "mal" -> "13", "mangaupdates" -> "..."
	Adult         bool              `json:"adult,omitempty"`
	TotalChapters int               `json:"totalChapters,omitempty"`
	Format        string            `json:"format,omitempty"`  // manga, manhwa, manhua, comic, oneshot, novel
	Country       string            `json:"country,omitempty"` // ISO country of origin
	URL           string            `json:"url,omitempty"`

	// Adaptations is nil when the provider did not fetch relations.
	Adaptations []model.Adaptation `json:"adaptations,omitempty"`
}

type Module interface {
	modules.Instance
	Search(ctx context.Context, query string, limit int) ([]SeriesMetadata, error)
	Get(ctx context.Context, id string) (*SeriesMetadata, error)
}

// LanguageSearcher lets a provider localize search result titles for one
// request. Providers without it continue to use Search.
type LanguageSearcher interface {
	SearchLanguage(ctx context.Context, query, language string, limit int) ([]SeriesMetadata, error)
}

// LanguageGetter lets an add flow keep the selected edition language when it
// resolves the full metadata record.
type LanguageGetter interface {
	GetLanguage(ctx context.Context, id, language string) (*SeriesMetadata, error)
}

// ExternalLookup is implemented by providers that can resolve a series from
// another provider's id (e.g. AniList by MAL id). Used to join providers.
type ExternalLookup interface {
	LookupExternal(ctx context.Context, provider, id string) (*SeriesMetadata, error)
}

// ErrNotFound is returned when a provider has no such series.
var ErrNotFound = errors.New("metadata not found")

// Related is a title in the same story: a sequel, prequel, side story, ...
type Related struct {
	SeriesMetadata
	// Relation is the provider's relation in lower case: sequel, prequel,
	// side_story, spin_off, alternative, parent, source, ...
	Relation string `json:"relation"`
}

// Recommended is a title the provider's users recommend alongside another.
type Recommended struct {
	SeriesMetadata
	// Votes is how many users recommend it (the provider's rating).
	Votes int `json:"votes"`
}

// Recommendations are what a provider suggests for one of its series.
type Recommendations struct {
	Related     []Related
	Recommended []Recommended
}

// Recommender is implemented by providers that know related and
// recommended titles for a series.
type Recommender interface {
	Recommendations(ctx context.Context, id string) (*Recommendations, error)
}
