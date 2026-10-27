package model

import (
	"time"

	"github.com/uptrace/bun"
)

// A title's reading status, at most one per user and title.
const (
	ReadReading   = "reading"
	ReadPlanned   = "planned"
	ReadCompleted = "completed"
	ReadOnHold    = "onhold"
	ReadDropped   = "dropped"
)

// ReadingList kinds.
const (
	ListFavorites = "favorites" // the built-in Favorites
	ListOwn       = ""          // a list the user made
)

// ReadingStatuses are the statuses in the order they show.
var ReadingStatuses = []string{ReadReading, ReadPlanned, ReadCompleted, ReadOnHold, ReadDropped}

// TitleStatus is where a user is with a title (ReadReading…). TitleID is
// the title's work, or minus its series without one, like reading progress.
type TitleStatus struct {
	bun.BaseModel `bun:"table:title_statuses"`
	UserID        int64     `bun:"user_id,pk"`
	TitleID       int64     `bun:"title_id,pk"`
	Status        string    `bun:"status,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
}

// ReadingList is a user's list of titles: the built-in Favorites, or one
// the user made.
type ReadingList struct {
	bun.BaseModel `bun:"table:reading_lists"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	UserID        int64     `bun:"user_id,notnull" json:"-"`
	Name          string    `bun:"name,notnull" json:"name"`
	Kind          string    `bun:"kind,notnull" json:"kind" enum:"favorites,"`
	CreatedAt     time.Time `bun:"created_at,notnull" json:"createdAt"`
}

// ReadingListTitle puts a title in a list.
type ReadingListTitle struct {
	bun.BaseModel `bun:"table:reading_list_titles"`
	ListID        int64     `bun:"list_id,pk"`
	TitleID       int64     `bun:"title_id,pk"`
	AddedAt       time.Time `bun:"added_at,notnull"`
}
