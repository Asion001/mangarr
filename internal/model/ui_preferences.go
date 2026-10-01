package model

import (
	"time"

	"github.com/uptrace/bun"
)

// UIPreferences belong to the account, independently of its reader settings.
type UIPreferences struct {
	bun.BaseModel `bun:"table:user_ui_preferences"`
	UserID        int64     `bun:"user_id,pk" json:"-"`
	Locale        string    `bun:"locale,notnull" json:"locale" enum:"auto,en,ru,uk"`
	Mode          string    `bun:"mode,notnull" json:"mode" enum:"reading,editing"`
	Options       UIOptions `bun:"options,notnull" json:"options"`
	UpdatedAt     time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}

// UIOptions are the account's interface options; a missing one means the
// default.
type UIOptions struct {
	// OtherLanguageChapters lists chapters only another language edition
	// of the title has in an edition's chapter list (nil = on).
	OtherLanguageChapters *bool `json:"otherLanguageChapters,omitempty"`
	// Theme, Accent and StartPage override the instance's (empty = its).
	Theme     string `json:"theme,omitempty" enum:"dark,light,system,"`
	Accent    string `json:"accent,omitempty" pattern:"^(#[0-9a-fA-F]{6})?$"`
	StartPage string `json:"startPage,omitempty" enum:"series,discover,updates,continue,"`
	// SidebarCollapsed starts the sidebar narrow; HiddenNav hides sidebar
	// items (by path, e.g. "/discover").
	SidebarCollapsed *bool    `json:"sidebarCollapsed,omitempty"`
	HiddenNav        []string `json:"hiddenNav,omitempty" maxItems:"20"`
	// Library view defaults for a device that has none of its own.
	LibraryView     string `json:"libraryView,omitempty" enum:"posters,table,"`
	LibrarySort     string `json:"librarySort,omitempty" maxLength:"20"`
	LibraryPageSize string `json:"libraryPageSize,omitempty" maxLength:"4"`
}
