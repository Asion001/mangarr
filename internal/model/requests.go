package model

import (
	"time"

	"github.com/uptrace/bun"
)

// Request statuses.
const (
	RequestPending   = "pending"   // waiting for someone to add it
	RequestApproved  = "approved"  // added; waiting for the first chapter
	RequestAvailable = "available" // a chapter is in the library
	RequestDeclined  = "declined"
)

// RequestKindMonitor asks for a title in the library to be downloaded.
const RequestKindMonitor = "monitor"

// Request is a series someone asked for.
type Request struct {
	bun.BaseModel `bun:"table:requests"`
	ID            int64           `bun:"id,pk,autoincrement" json:"id"`
	Title         string          `bun:"title,notnull" json:"title"`
	Metadata      RequestMetadata `bun:"metadata,notnull" json:"metadata"`
	// Kind is "" for a title or language, or "monitor": download a title
	// that is here but not monitored (SeriesID is that title).
	Kind string `bun:"kind,notnull" json:"kind,omitempty" enum:",monitor"`
	// Language is the edition asked for (empty: the server's default languages).
	Language string `bun:"language,notnull" json:"language,omitempty"`
	// WorkID is the title in the library a new language is asked for.
	WorkID      *int64     `bun:"work_id" json:"workId,omitempty"`
	Status      string     `bun:"status,notnull" json:"status" enum:"pending,approved,available,declined"`
	Reason      string     `bun:"reason,notnull" json:"reason,omitempty"`
	SeriesID    *int64     `bun:"series_id" json:"seriesId,omitempty"`
	HandledBy   *int64     `bun:"handled_by" json:"-"`
	CreatedAt   time.Time  `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt   time.Time  `bun:"updated_at,notnull" json:"updatedAt"`
	HandledAt   *time.Time `bun:"handled_at" json:"handledAt,omitempty"`
	AvailableAt *time.Time `bun:"available_at" json:"availableAt,omitempty"`
}

// RequestMetadata is what the metadata search said about the series.
type RequestMetadata struct {
	ModuleID    int64             `json:"moduleId,omitempty"`
	Provider    string            `json:"provider,omitempty"`
	ID          string            `json:"id,omitempty"`
	AltTitles   []string          `json:"altTitles,omitempty"`
	Year        int               `json:"year,omitempty"`
	Format      string            `json:"format,omitempty"`
	Status      string            `json:"status,omitempty"`
	Description string            `json:"description,omitempty"`
	CoverURL    string            `json:"coverUrl,omitempty"`
	URL         string            `json:"url,omitempty"`
	Genres      []string          `json:"genres,omitempty"`
	Adult       bool              `json:"adult,omitempty"`
	ExternalIDs map[string]string `json:"externalIds,omitempty"`
}

// RequestUser is one person who asked for a request's series.
type RequestUser struct {
	bun.BaseModel `bun:"table:request_users"`
	RequestID     int64     `bun:"request_id,pk"`
	UserID        int64     `bun:"user_id,pk"`
	Note          string    `bun:"note,notnull"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
}

// Follow marks a series a user wants to hear about.
type Follow struct {
	bun.BaseModel `bun:"table:follows"`
	UserID        int64     `bun:"user_id,pk"`
	SeriesID      int64     `bun:"series_id,pk"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
}
