package model

import (
	"time"

	"github.com/uptrace/bun"
)

// ReadingKey is an API key a reading app uses with the Komga-compatible API.
// Only a hash is stored; Prefix identifies the key in the UI.
type ReadingKey struct {
	bun.BaseModel `bun:"table:reading_keys"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	KeyHash       string `bun:"key_hash,notnull" json:"-"`
	// KOReader hashes the password client-side with MD5. This SHA-256 verifier
	// is populated for newly issued device keys without storing the key itself.
	KOReaderHash string `bun:"koreader_hash,notnull" json:"-"`
	// UserID is the user the device belongs to (0: from before accounts).
	UserID int64  `bun:"user_id,nullzero" json:"userId,omitempty"`
	Prefix string `bun:"prefix,notnull" json:"prefix"`
	// Comment names the device ("KMReader iPad").
	Comment string `bun:"comment,notnull" json:"comment"`
	// LastClient is the app that last used the key (from its User-Agent).
	LastClient string `bun:"last_client,notnull" json:"lastClient"`
	// Languages is the device's language order. With one, a title that has
	// several language editions is shown once, its chapters in the first of
	// these languages that has them; empty lists every edition on its own.
	Languages  []string   `bun:"languages,notnull" json:"languages"`
	CreatedAt  time.Time  `bun:"created_at,notnull" json:"createdAt"`
	LastUsedAt *time.Time `bun:"last_used_at" json:"lastUsedAt,omitempty"`
}

// KOReaderDocument maps KOReader's file digest to a chapter for one reader.
type KOReaderDocument struct {
	bun.BaseModel `bun:"table:koreader_documents"`
	ReaderID      int64  `bun:"reader_id,pk"`
	Document      string `bun:"document,pk"`
	ChapterID     int64  `bun:"chapter_id,notnull"`
}

// ReadOriginApp marks read states written by reading apps through the
// Komga-compatible API (kept until a library server reports the chapter,
// like ReadOriginBackup).
const ReadOriginApp = "app"

// Read event origins (ReadEvent.Origin).
const (
	EventOriginApp    = "app"    // a reading app through the Komga-compatible API
	EventOriginServer = "server" // a library server (Komga, Kavita)
	EventOriginBackup = "backup" // an imported backup
)

// Read event outcomes.
const (
	OutcomeApplied   = "applied"
	OutcomeKept      = "kept"      // lower than mangarr's state: mangarr kept its own
	OutcomeUnread    = "unread"    // explicitly marked unread
	OutcomeUnchanged = "unchanged" // nothing new
)

// ReadEvent is one progress report, for sync health per device.
type ReadEvent struct {
	bun.BaseModel `bun:"table:read_events"`
	ID            int64 `bun:"id,pk,autoincrement" json:"id"`
	ReaderID      int64 `bun:"reader_id,notnull" json:"readerId"`
	SeriesID      int64 `bun:"series_id,notnull" json:"seriesId"`
	ChapterID     int64 `bun:"chapter_id,notnull" json:"chapterId"`
	// Chapters is how many chapters the event covers (ChapterID is the
	// highest-numbered one).
	Chapters  int       `bun:"chapters,notnull" json:"chapters"`
	Completed bool      `bun:"completed,notnull" json:"completed"`
	Page      int       `bun:"page,notnull" json:"page"`
	Origin    string    `bun:"origin,notnull" json:"origin"`
	Client    string    `bun:"client,notnull" json:"client"`
	Device    string    `bun:"device,notnull" json:"device"`
	Outcome   string    `bun:"outcome,notnull" json:"outcome"`
	At        time.Time `bun:"at,notnull" json:"at"`
}

// ReadingSession is active time reported by one web-reader chapter visit.
// ActiveSeconds is cumulative so a retried heartbeat is idempotent.
type ReadingSession struct {
	bun.BaseModel `bun:"table:reading_sessions"`
	ID            string    `bun:"id,pk" json:"id"`
	ReaderID      int64     `bun:"reader_id,notnull" json:"readerId"`
	SeriesID      int64     `bun:"series_id,notnull" json:"seriesId"`
	ChapterID     int64     `bun:"chapter_id,notnull" json:"chapterId"`
	ActiveSeconds int       `bun:"active_seconds,notnull" json:"activeSeconds"`
	StartedAt     time.Time `bun:"started_at,notnull" json:"startedAt"`
	UpdatedAt     time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}

// ReaderPrefs are a user's web reader settings: defaults (SeriesID 0) or
// for one series. Data is the UI's settings object.
type ReaderPrefs struct {
	bun.BaseModel `bun:"table:reader_prefs"`
	UserID        int64     `bun:"user_id,pk" json:"-"`
	SeriesID      int64     `bun:"series_id,pk" json:"seriesId"`
	Data          string    `bun:"data,notnull" json:"-"`
	UpdatedAt     time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}
