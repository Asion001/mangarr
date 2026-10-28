package model

import (
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/backupimport"
)

// Import statuses.
const (
	ImportMapping = "mapping" // entries are being matched to catalogs
	ImportReview  = "review"  // mapped; waiting for the user to start it
	ImportRunning = "running"
	ImportDone    = "done"
	ImportFailed  = "failed"
)

// Import entry states.
const (
	EntryPending   = "pending"   // not mapped yet
	EntryReady     = "ready"     // a catalog manga was found
	EntryReview    = "review"    // unsure or nothing found: pick a source
	EntryExtension = "extension" // the catalog's extension isn't installed
	EntryLibrary   = "library"   // already in the library (read state is merged)
	EntryImported  = "imported"
	EntryFailed    = "failed"
)

// Import is an uploaded backup of another app.
type Import struct {
	bun.BaseModel `bun:"table:imports"`
	ID            int64         `bun:"id,pk,autoincrement" json:"id"`
	Format        string        `bun:"format,notnull" json:"format"`
	FileName      string        `bun:"file_name,notnull" json:"fileName"`
	Status        string        `bun:"status,notnull" json:"status" enum:"mapping,review,running,done,failed"`
	Progress      string        `bun:"progress,notnull" json:"progress"`
	Options       ImportOptions `bun:"options,notnull" json:"options"`
	Info          ImportInfo    `bun:"info,notnull" json:"info"`
	Error         string        `bun:"error,notnull" json:"error"`
	// RequestedBy is the user who asked for this import from their own
	// library: their progress and lists go with it.
	RequestedBy *int64    `bun:"requested_by" json:"requestedBy,omitempty"`
	CreatedAt   time.Time `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt   time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}

// ImportInfo describes the backup itself.
type ImportInfo struct {
	BackupDate *time.Time        `json:"backupDate,omitempty"`
	Categories []string          `json:"categories"`
	Sources    map[string]string `json:"sources"`
	Entries    int               `json:"entries"`
	// ModuleID is the source module that read the account (account imports).
	ModuleID int64 `json:"moduleId,omitempty"`
	// Lists maps an account's list names to their status (reading, dropped,
	// ...; account imports).
	Lists map[string]string `json:"lists,omitempty"`
}

// ImportOptions control how entries are added.
type ImportOptions struct {
	// OnlyFavorites leaves manga that are only in the history unselected.
	OnlyFavorites bool  `json:"onlyFavorites"`
	RootFolderID  int64 `json:"rootFolderId"`
	ProfileID     int64 `json:"profileId"`
	// Monitor is "unread" (from the first chapter after the last read one),
	// or an add option: all, future, none.
	Monitor       string `json:"monitor" enum:"unread,all,future,none"`
	MonitorNew    string `json:"monitorNew" enum:"all,none"`
	SearchMissing bool   `json:"searchMissing"`
	// CategoryTags tags series with their backup categories.
	CategoryTags bool `json:"categoryTags"`
	// Categories overrides root folder / profile per category.
	Categories map[string]ImportCategory `json:"categories,omitempty"`
	// ReadState imports read chapters for UserID (0 = the first admin).
	ReadState bool  `json:"readState"`
	UserID    int64 `json:"userId"`
	// PushProgress writes the imported read state to Komga/Kavita once the
	// chapters are on disk.
	PushProgress bool `json:"pushProgress"`
	// MatchMetadata links a metadata provider (by tracker id or title).
	MatchMetadata bool `json:"matchMetadata"`
	// FindByTitle searches catalogs by title when a source can't be mapped.
	FindByTitle bool `json:"findByTitle"`
	// BlockScanlators copies the app's excluded scanlators to the series.
	BlockScanlators bool `json:"blockScanlators"`
}

// ImportCategory overrides options for one backup category.
type ImportCategory struct {
	RootFolderID int64 `json:"rootFolderId,omitempty"`
	ProfileID    int64 `json:"profileId,omitempty"`
	// Skip leaves the category's manga unselected.
	Skip bool `json:"skip,omitempty"`
}

// DefaultImportOptions are the options of a new import.
func DefaultImportOptions() ImportOptions {
	return ImportOptions{OnlyFavorites: true, Monitor: "unread", MonitorNew: MonitorAll, SearchMissing: true, CategoryTags: true,
		ReadState: true, PushProgress: true, MatchMetadata: true, FindByTitle: true, BlockScanlators: true}
}

// ImportEntry is one manga of an import.
type ImportEntry struct {
	bun.BaseModel `bun:"table:import_entries"`
	ID            int64                    `bun:"id,pk,autoincrement" json:"id"`
	ImportID      int64                    `bun:"import_id,notnull" json:"importId"`
	Position      int                      `bun:"position,notnull" json:"position"`
	Title         string                   `bun:"title,notnull" json:"title"`
	State         string                   `bun:"state,notnull" json:"state" enum:"pending,ready,review,extension,library,imported,failed"`
	Selected      bool                     `bun:"selected,notnull" json:"selected"`
	Data          backupimport.BackupManga `bun:"data,notnull" json:"data"`
	Source        *ImportSource            `bun:"source" json:"source,omitempty"`
	Metadata      *ImportMetadata          `bun:"metadata" json:"metadata,omitempty"`
	Extension     *ImportExtension         `bun:"extension" json:"extension,omitempty"`
	SeriesID      *int64                   `bun:"series_id" json:"seriesId,omitempty"`
	Message       string                   `bun:"message,notnull" json:"message"`
	UpdatedAt     time.Time                `bun:"updated_at,notnull" json:"updatedAt"`
}

// How an entry was matched.
const (
	MatchExact   = "exact"   // same catalog id and url
	MatchRule    = "rule"    // a known url conversion (Aidoku)
	MatchPath    = "path"    // the web url's path, verified at the catalog
	MatchTitle   = "title"   // title search
	MatchTracker = "tracker" // tracker id in the backup
	MatchLookup  = "lookup"  // tracker id converted by a provider
	MatchManual  = "manual"  // picked by the user
)

// ImportSource is the catalog manga an entry maps to.
type ImportSource struct {
	ModuleID     int64   `json:"moduleId"`
	SourceID     string  `json:"sourceId"`
	SourceName   string  `json:"sourceName"`
	Lang         string  `json:"lang"`
	URL          string  `json:"url"`
	Title        string  `json:"title"`
	ThumbnailURL string  `json:"thumbnailUrl,omitempty"`
	How          string  `json:"how"`
	Score        float64 `json:"score,omitempty"`
}

// ImportMetadata is the metadata series an entry maps to.
type ImportMetadata struct {
	ModuleID int64   `json:"moduleId"`
	Provider string  `json:"provider"`
	ID       string  `json:"id"`
	Title    string  `json:"title,omitempty"`
	CoverURL string  `json:"coverUrl,omitempty"`
	How      string  `json:"how"`
	Score    float64 `json:"score,omitempty"`
}

// ImportExtension is an extension to install for an entry's catalog.
type ImportExtension struct {
	ModuleID int64  `json:"moduleId"`
	Pkg      string `json:"pkg"`
	Name     string `json:"name"`
	Lang     string `json:"lang"`
}
