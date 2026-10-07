// Package model holds the persisted entities (bun models). Business logic
// lives in the service packages; this package only describes data.
package model

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"time"

	"github.com/uptrace/bun"
)

// ---- Users / settings -------------------------------------------------------

type User struct {
	bun.BaseModel `bun:"table:users"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Username      string `bun:"username,notnull" json:"username"`
	PasswordHash  string `bun:"password_hash,notnull" json:"-"`
	// DisplayName is shown instead of the username when set.
	DisplayName string `bun:"display_name,notnull" json:"displayName"`
	GroupID     int64  `bun:"group_id,nullzero" json:"groupId"`
	// ReaderID is the user's own progress (their reader).
	ReaderID    int64      `bun:"reader_id,nullzero" json:"readerId"`
	Disabled    bool       `bun:"disabled,notnull" json:"disabled"`
	LastLoginAt *time.Time `bun:"last_login_at" json:"lastLoginAt,omitempty"`
	// OIDCSubject links the user to a single sign-on account.
	OIDCSubject string    `bun:"oidc_subject,notnull" json:"-"`
	CreatedBy   int64     `bun:"created_by,nullzero" json:"createdBy,omitempty"`
	CreatedAt   time.Time `bun:"created_at,notnull" json:"createdAt"`
}

// Group gives its users permissions (internal/access) and can limit the
// series they see to some tags or root folders.
type Group struct {
	bun.BaseModel `bun:"table:groups"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Name          string `bun:"name,notnull" json:"name"`
	// Builtin is "admins" or "users" for the groups mangarr creates.
	Builtin     string   `bun:"builtin,notnull" json:"builtin,omitempty"`
	Permissions []string `bun:"permissions,notnull" json:"permissions"`
	// IncludeTags (any of them) and ExcludeTags limit the series members
	// see by tag, RootFolders by root folder; empty means no limit.
	IncludeTags []int64 `bun:"include_tags,notnull" json:"includeTags"`
	ExcludeTags []int64 `bun:"exclude_tags,notnull" json:"excludeTags"`
	RootFolders []int64 `bun:"root_folders,notnull" json:"rootFolders"`
	// AutoApproveRequests adds members' requests without a manager.
	AutoApproveRequests bool      `bun:"auto_approve_requests,notnull" json:"autoApproveRequests"`
	CreatedAt           time.Time `bun:"created_at,notnull" json:"createdAt"`
}

// Built-in groups.
const (
	GroupAdmins = "admins"
	GroupUsers  = "users"
)

// Session is a web login; the cookie holds its id.
type Session struct {
	bun.BaseModel `bun:"table:sessions"`
	ID            string    `bun:"id,pk" json:"-"`
	UserID        int64     `bun:"user_id,notnull" json:"userId"`
	CreatedAt     time.Time `bun:"created_at,notnull" json:"createdAt"`
	LastSeenAt    time.Time `bun:"last_seen_at,notnull" json:"lastSeenAt"`
	ExpiresAt     time.Time `bun:"expires_at,notnull" json:"expiresAt"`
	IP            string    `bun:"ip,notnull" json:"ip"`
	UserAgent     string    `bun:"user_agent,notnull" json:"userAgent"`
}

// Invite is a link that lets someone create an account in a group.
type Invite struct {
	bun.BaseModel `bun:"table:invites"`
	ID            int64      `bun:"id,pk,autoincrement" json:"id"`
	TokenHash     string     `bun:"token_hash,notnull" json:"-"`
	GroupID       int64      `bun:"group_id,notnull" json:"groupId"`
	Note          string     `bun:"note,notnull" json:"note"`
	MaxUses       int        `bun:"max_uses,notnull" json:"maxUses"`
	Uses          int        `bun:"uses,notnull" json:"uses"`
	ExpiresAt     *time.Time `bun:"expires_at" json:"expiresAt,omitempty"`
	CreatedBy     int64      `bun:"created_by,nullzero" json:"createdBy,omitempty"`
	CreatedAt     time.Time  `bun:"created_at,notnull" json:"createdAt"`
}

// MessengerLink is a user's identity on an admin-owned Telegram or Discord
// bot. It deliberately contains no bot credentials or user-controlled URL.
type MessengerLink struct {
	bun.BaseModel `bun:"table:messenger_links"`
	ID            int64      `bun:"id,pk,autoincrement" json:"id"`
	UserID        int64      `bun:"user_id,notnull" json:"userId"`
	Kind          string     `bun:"kind,notnull" json:"kind"`
	ExternalID    string     `bun:"external_id,notnull" json:"-"`
	DisplayName   string     `bun:"display_name,notnull" json:"displayName"`
	Mode          string     `bun:"mode,notnull" json:"mode"`
	Events        []string   `bun:"events,notnull" json:"events"`
	Status        string     `bun:"status,notnull" json:"status"`
	LastError     string     `bun:"last_error,notnull" json:"lastError,omitempty"`
	LastAttemptAt *time.Time `bun:"last_attempt_at" json:"lastAttemptAt,omitempty"`
	// DigestThrough is how far the daily digest has covered (nil: since
	// the link was made).
	DigestThrough *time.Time `bun:"digest_through" json:"-"`
	CreatedAt     time.Time  `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt     time.Time  `bun:"updated_at,notnull" json:"updatedAt"`
}

const (
	MessengerTelegram = "telegram"
	MessengerDiscord  = "discord"
	DeliveryOff       = "off"
	DeliveryInstant   = "instant"
	DeliveryDigest    = "daily_digest"
	DeliveryBoth      = "instant_and_digest"
	LinkActive        = "active"
	LinkBroken        = "broken"
)

// MessengerLinkToken is a short-lived, single-use account-link token. Only
// its hash is persisted.
type MessengerLinkToken struct {
	bun.BaseModel `bun:"table:messenger_link_tokens"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	TokenHash     string    `bun:"token_hash,notnull" json:"-"`
	UserID        int64     `bun:"user_id,notnull" json:"userId"`
	Kind          string    `bun:"kind,notnull" json:"kind"`
	ExpiresAt     time.Time `bun:"expires_at,notnull" json:"expiresAt"`
	CreatedAt     time.Time `bun:"created_at,notnull" json:"createdAt"`
}

// NotificationDelivery is the durable per-user inbox entry. DedupeKey is
// unique for a user so replaying an event is idempotent.
type NotificationDelivery struct {
	bun.BaseModel `bun:"table:notification_deliveries"`
	ID            int64          `bun:"id,pk,autoincrement" json:"id"`
	UserID        int64          `bun:"user_id,notnull" json:"userId"`
	DedupeKey     string         `bun:"dedupe_key,notnull" json:"dedupeKey"`
	EventType     string         `bun:"event_type,notnull" json:"eventType"`
	SeriesID      *int64         `bun:"series_id" json:"seriesId,omitempty"`
	Payload       map[string]any `bun:"payload,notnull" json:"payload"`
	CreatedAt     time.Time      `bun:"created_at,notnull" json:"createdAt"`
}

// NotificationDispatch tracks one inbox entry's independent delivery to one
// linked messenger account.
type NotificationDispatch struct {
	bun.BaseModel `bun:"table:notification_dispatches"`
	ID            int64      `bun:"id,pk,autoincrement" json:"id"`
	DeliveryID    int64      `bun:"delivery_id,notnull" json:"deliveryId"`
	LinkID        int64      `bun:"link_id,notnull" json:"linkId"`
	AvailableAt   time.Time  `bun:"available_at,notnull" json:"availableAt"`
	SentAt        *time.Time `bun:"sent_at" json:"sentAt,omitempty"`
	Attempts      int        `bun:"attempts,notnull" json:"attempts"`
	LastError     string     `bun:"last_error,notnull" json:"lastError,omitempty"`
}

type Setting struct {
	bun.BaseModel `bun:"table:settings"`
	Key           string    `bun:"key,pk"`
	Value         string    `bun:"value,notnull"`
	UpdatedAt     time.Time `bun:"updated_at,notnull"`
}

type Tag struct {
	bun.BaseModel `bun:"table:tags"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Label         string `bun:"label,notnull" json:"label"`
}

type RootFolder struct {
	bun.BaseModel `bun:"table:root_folders"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id"`
	Path          string `bun:"path,notnull" json:"path"`
	Language      string `bun:"language,notnull" json:"language"`
	// ManagedBy is "env" when the folder comes from MANGARR_ROOT_FOLDERS.
	ManagedBy string    `bun:"managed_by,notnull" json:"managedBy,omitempty"`
	CreatedAt time.Time `bun:"created_at,notnull" json:"createdAt"`
}

// ---- Profiles ---------------------------------------------------------------

type Profile struct {
	bun.BaseModel `bun:"table:profiles"`
	ID            int64         `bun:"id,pk,autoincrement" json:"id"`
	Name          string        `bun:"name,notnull" json:"name"`
	IsDefault     bool          `bun:"is_default,notnull" json:"isDefault"`
	Config        ProfileConfig `bun:"config,notnull" json:"config"`
	CreatedAt     time.Time     `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt     time.Time     `bun:"updated_at,notnull" json:"updatedAt"`
}

type ProfileConfig struct {
	// PreferredScanlators are regexes; earlier entries rank higher.
	PreferredScanlators []string `json:"preferredScanlators"`
	// BlockedScanlators are regexes; matching releases are rejected.
	BlockedScanlators []string `json:"blockedScanlators"`
	// AllowUpgrades replaces an existing chapter when a better-ranked release appears.
	AllowUpgrades bool `json:"allowUpgrades"`
	// MinPages rejects chapters with fewer pages (0 = off).
	MinPages int `json:"minPages"`
	// Upscale settings applied to chapters downloaded with this profile.
	Upscale UpscaleConfig `json:"upscale"`
	// Encode re-encodes pages to save space (AVIF, JPEG XL).
	Encode EncodeConfig `json:"encode"`
	// Pages are size rules applied to every page, with or without upscaling.
	Pages PageRules `json:"pages"`
	// LowRes decides what happens to a release whose pages are mostly too narrow.
	LowRes LowResRule `json:"lowRes"`
	// ProcessTiming: "background" (default) imports the original and
	// processes it later; "inline" processes before import.
	ProcessTiming string `json:"processTiming,omitempty" enum:",background,inline"`
	// ProcessExisting also processes chapters imported before the processing
	// settings last changed (otherwise only newer chapters are processed).
	// The server clears it whenever those settings change again.
	ProcessExisting bool `json:"processExisting"`
	// ProcessChangedAt is set by the server when processing settings change.
	ProcessChangedAt *time.Time `json:"processChangedAt,omitempty"`
	// Cleanup overrides; nil fields inherit the global cleanup settings.
	Cleanup CleanupOverride `json:"cleanup"`
}

// ProcessParams identifies the processing a chapter file needs under this
// profile: a short hash of the processing settings that change the output,
// or "" when the profile doesn't process files at all. Files are processed
// again when their stored hash differs.
func (c ProfileConfig) ProcessParams() string {
	var parts struct {
		Upscale *UpscaleConfig `json:"u,omitempty"`
		Encode  *EncodeConfig  `json:"e,omitempty"`
		// only non-default page rules take part, so older profiles keep
		// their hash and their files aren't processed again
		Junk     int    `json:"j,omitempty"`
		MaxWidth int    `json:"w,omitempty"`
		Split    string `json:"s,omitempty"`
	}
	encoding := c.Encode.Format != "" && c.Encode.Format != "keep"
	parts.MaxWidth = c.Pages.MaxWidth
	if threshold, segment := c.Pages.SplitRatios(); threshold > 0 {
		parts.Split = strconv.FormatFloat(threshold, 'g', -1, 64) + ":" + strconv.FormatFloat(segment, 'g', -1, 64)
	}
	if j := c.Pages.JunkSize(); j != DefaultJunkUnder && (c.Upscale.Enabled || encoding || parts.MaxWidth > 0 || parts.Split != "") {
		parts.Junk = j
		if j == 0 {
			parts.Junk = -1
		}
	}
	if c.Upscale.Enabled {
		u := c.Upscale
		u.UpscalerID = 0 // which worker runs it doesn't change the result
		if encoding {
			u.Format, u.Quality = "", 0 // upscaled pages go to the encoder as PNG
		}
		parts.Upscale = &u
	}
	if encoding {
		e := c.Encode
		e.RecycleOriginals = false
		parts.Encode = &e
	}
	if parts.Upscale == nil && parts.Encode == nil && parts.MaxWidth == 0 && parts.Split == "" {
		return ""
	}
	b, _ := json.Marshal(parts)
	sum := sha1.Sum(b)
	return hex.EncodeToString(sum[:8])
}

// ProcessForce marks a file to be processed again regardless of its hash.
const ProcessForce = "force"

// Chapter file processing states.
const (
	ProcessDone   = "done"
	ProcessFailed = "failed"
)

// EncodeConfig re-encodes page images to save storage.
type EncodeConfig struct {
	// Format: keep, avif (lossy, much smaller) or jxl (lossless JPEG
	// recompression, ~20% smaller and reversible; lossy with Lossy).
	Format string `json:"format" enum:"keep,avif,jxl"`
	// Lossy writes JPEG XL at Quality instead of recompressing losslessly.
	// omitempty keeps the processing hash of profiles saved before it.
	Lossy bool `json:"lossy,omitempty"`
	// Preset trades speed for size: max (smallest), balanced, fast.
	Preset string `json:"preset" enum:"max,balanced,fast"`
	// Quality overrides the preset (AVIF and lossy JPEG XL 1-100; 0 = preset).
	Quality int `json:"quality"`
	// Speed overrides the preset (avifenc -s 0-10, cjxl effort 1-9; 0 = preset).
	Speed int `json:"speed"`
	// Grayscale encodes black-and-white pages without color (smaller AVIF).
	Grayscale bool `json:"grayscale"`
	// Progressive is ignored: AVIF is written layered whenever the encoder
	// supports it. Kept so older clients and backups still load.
	Progressive bool `json:"progressive"`
	// MinSavingsPct keeps a page's original unless re-encoding saves at least this much.
	MinSavingsPct int `json:"minSavingsPct"`
	// RecycleOriginals moves replaced files to the recycle bin (else they're deleted).
	RecycleOriginals bool `json:"recycleOriginals"`
}

// DefaultJunkUnder is the longest side (px) under which an image is junk
// (spacers, logos, tracking pixels) when a profile doesn't set its own.
const DefaultJunkUnder = 300

// DefaultSplitRatio is the height:width ratio above which a page counts as a
// webtoon strip. Manga pages (about 1.4) and double-height pages stay whole,
// however far they were upscaled.
const DefaultSplitRatio = 3.0

// DefaultSegmentRatio is the tallest height:width ratio of a split segment,
// about one phone screen, so segments keep full width and quality.
const DefaultSegmentRatio = 2.0

// PageRules are the page size limits of a profile.
type PageRules struct {
	// JunkUnder: images whose longest side is under this many pixels are
	// junk: never upscaled or re-encoded, and a chapter of nothing but junk
	// is rejected. 0 = DefaultJunkUnder, negative = off.
	JunkUnder int `json:"junkUnder"`
	// RemoveJunk drops junk images from the chapter file.
	RemoveJunk bool `json:"removeJunk"`
	// MaxWidth shrinks pages wider than this (landscape spreads may be twice
	// as wide). 0 = no limit.
	MaxWidth int `json:"maxWidth"`
	// SplitTall enables splitting long strips after upscaling and before
	// re-encoding. Only pages taller than SplitRatio times their width are
	// split, into segments at most SegmentRatio times their width tall.
	// 0 uses DefaultSplitRatio / DefaultSegmentRatio.
	SplitTall    bool    `json:"splitTall"`
	SplitRatio   float64 `json:"splitRatio" doc:"Split pages taller than this many times their width (0 = 3)"`
	SegmentRatio float64 `json:"segmentRatio" doc:"Segments are at most this many times their width tall (0 = 2)"`
}

// SplitRatios returns the height:width ratio above which a page is split and
// the tallest segment ratio, or zeros when splitting is off.
func (r PageRules) SplitRatios() (threshold, segment float64) {
	if !r.SplitTall {
		return 0, 0
	}
	threshold, segment = r.SplitRatio, r.SegmentRatio
	if threshold <= 0 {
		threshold = DefaultSplitRatio
	}
	if segment <= 0 {
		segment = DefaultSegmentRatio
	}
	threshold = max(threshold, 1)
	segment = min(max(segment, 0.5), threshold)
	return threshold, segment
}

// JunkSize is the junk threshold in pixels (0 = off).
func (r PageRules) JunkSize() int {
	switch {
	case r.JunkUnder < 0:
		return 0
	case r.JunkUnder == 0:
		return DefaultJunkUnder
	}
	return r.JunkUnder
}

// IsJunk reports whether a w×h image is junk under threshold junk.
func IsJunk(w, h, junk int) bool { return junk > 0 && w > 0 && h > 0 && max(w, h) < junk }

// PageWidth is the width of one page: a landscape image is a two-page
// spread, so each half counts.
func PageWidth(w, h int) int {
	if w > h && h > 0 {
		return w / 2
	}
	return w
}

// DefaultLowResWidth is the page width a low-resolution rule uses when it
// doesn't set one.
const DefaultLowResWidth = 720

// Low-resolution release actions.
const (
	LowResKeep   = "keep"   // import it anyway (the default)
	LowResRetry  = "retry"  // try another source, keep it when none is left
	LowResReject = "reject" // never import it
)

// LowResRule handles releases whose pages are mostly narrower than Width.
type LowResRule struct {
	// Width in pixels (0 = DefaultLowResWidth).
	Width  int    `json:"width"`
	Action string `json:"action" enum:",keep,retry,reject"`
}

// MinWidth is the rule's threshold, or 0 when low resolution is accepted.
func (r LowResRule) MinWidth() int {
	if r.Action != LowResRetry && r.Action != LowResReject {
		return 0
	}
	if r.Width <= 0 {
		return DefaultLowResWidth
	}
	return r.Width
}

type UpscaleConfig struct {
	Enabled bool `json:"enabled"`
	// UpscalerID is retained for backup/API compatibility. The server clears it
	// and selects an available installation-wide upscaler by priority.
	UpscalerID int64 `json:"upscalerId"`
	// MinWidth: pages narrower than this are upscaled.
	MinWidth int `json:"minWidth"`
	// MaxWidth caps output width (downscaled after upscaling). 0 = no cap.
	MaxWidth int `json:"maxWidth"`
	// Model name understood by the upscaler (e.g. "waifu2x-cunet", "realcugan", "realesr-animevideov3").
	Model string `json:"model"`
	// Noise reduction level (model dependent, -1..3).
	Noise int `json:"noise"`
	// Format of upscaled pages: "source" (each page's own format), "webp",
	// "jpeg" or "png". Ignored when re-encoding (pages go to the encoder as PNG).
	Format  string `json:"format"`
	Quality int    `json:"quality"`
}

type CleanupOverride struct {
	Enabled      *bool `json:"enabled,omitempty"`
	KeepLastRead *int  `json:"keepLastRead,omitempty"`
	GraceDays    *int  `json:"graceDays,omitempty"`
}

// ---- Modules (provider definitions) -----------------------------------------

type ProviderDefinition struct {
	bun.BaseModel  `bun:"table:provider_definitions"`
	ID             int64          `bun:"id,pk,autoincrement" json:"id"`
	Kind           string         `bun:"kind,notnull" json:"kind"`
	Implementation string         `bun:"implementation,notnull" json:"implementation"`
	Name           string         `bun:"name,notnull" json:"name"`
	Enabled        bool           `bun:"enabled,notnull" json:"enabled"`
	Priority       int            `bun:"priority,notnull" json:"priority"`
	Tags           []int64        `bun:"tags,notnull" json:"tags"`
	Events         []string       `bun:"events,notnull" json:"events"`
	Settings       map[string]any `bun:"settings,notnull" json:"settings"`
	// ManagedBy is "env:<NAME>" for instances defined by MANGARR_MODULE_<NAME>_* variables.
	ManagedBy string `bun:"managed_by,notnull" json:"managedBy,omitempty"`
	// UserID is set on a user's own notification targets (nil: the install's).
	UserID    *int64    `bun:"user_id" json:"userId,omitempty"`
	CreatedAt time.Time `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}

// CatalogPref holds per-catalog preferences and throttling state.
type CatalogPref struct {
	bun.BaseModel   `bun:"table:catalog_prefs"`
	ID              int64          `bun:"id,pk,autoincrement" json:"-"`
	ModuleID        int64          `bun:"module_id,notnull" json:"moduleId"`
	SourceID        string         `bun:"source_id,notnull" json:"sourceId"`
	Enabled         bool           `bun:"enabled,notnull" json:"enabled"`
	Priority        int            `bun:"priority,notnull" json:"priority"`
	Throttle        ThrottleConfig `bun:"throttle,notnull" json:"throttle"`
	CooldownUntil   *time.Time     `bun:"cooldown_until" json:"cooldownUntil,omitempty"`
	CooldownStrikes int            `bun:"cooldown_strikes,notnull" json:"cooldownStrikes"`
	LastThrottle    string         `bun:"last_throttle,notnull" json:"lastThrottle,omitempty"`
	UpdatedAt       time.Time      `bun:"updated_at,notnull" json:"updatedAt"`
}

// ThrottleConfig limits requests to one catalog. Zero values in an override
// inherit the preset; Preset selects gentle, normal or fast.
type ThrottleConfig struct {
	Preset string `json:"preset,omitempty" enum:",gentle,normal,fast" desc:"gentle, normal or fast (fast = like Mihon: no extra delays)."`
	// RequestsPerMinute caps requests (0 = no cap); Burst allows short bursts.
	RequestsPerMinute int `json:"requestsPerMinute,omitempty" desc:"Maximum requests per minute per catalog (0 = preset)."`
	Burst             int `json:"burst,omitempty" desc:"Requests allowed in a short burst."`
	// MinDelayMs is the minimum gap between requests; JitterMs adds a random 0..JitterMs.
	MinDelayMs int `json:"minDelayMs,omitempty" desc:"Minimum gap between requests (ms)."`
	JitterMs   int `json:"jitterMs,omitempty" desc:"Random extra delay per request, 0..N ms."`
	// MaxConcurrent caps simultaneous requests.
	MaxConcurrent int `json:"maxConcurrent,omitempty" desc:"Simultaneous requests per catalog."`
	// Random pause between chapters / between series refreshes of this catalog (seconds).
	ChapterGapMinSec int `json:"chapterGapMinSec,omitempty" desc:"Minimum random pause between chapters of a catalog (s)."`
	ChapterGapMaxSec int `json:"chapterGapMaxSec,omitempty" desc:"Maximum random pause between chapters of a catalog (s)."`
	RefreshGapMinSec int `json:"refreshGapMinSec,omitempty" desc:"Minimum random pause between series checks on a catalog (s)."`
	RefreshGapMaxSec int `json:"refreshGapMaxSec,omitempty" desc:"Maximum random pause between series checks on a catalog (s)."`
}

// ---- Series -----------------------------------------------------------------

// Work is a canonical title shared by one or more language editions. Series
// owns all operational state; Work owns only identity and shared display data.
type Work struct {
	bun.BaseModel `bun:"table:works"`
	ID            int64          `bun:"id,pk,autoincrement" json:"id"`
	Title         string         `bun:"title,notnull" json:"title"`
	SortTitle     string         `bun:"sort_title,notnull" json:"sortTitle"`
	Metadata      SeriesMetadata `bun:"metadata,notnull" json:"metadata"`
	CreatedAt     time.Time      `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt     time.Time      `bun:"updated_at,notnull" json:"updatedAt"`
}

const (
	StatusUnknown   = "unknown"
	StatusOngoing   = "ongoing"
	StatusCompleted = "completed"
	StatusHiatus    = "hiatus"
	StatusCancelled = "cancelled"
)

type Series struct {
	bun.BaseModel      `bun:"table:series"`
	ID                 int64          `bun:"id,pk,autoincrement" json:"id"`
	WorkID             int64          `bun:"work_id,nullzero" json:"workId,omitempty"`
	Title              string         `bun:"title,notnull" json:"title"`
	SortTitle          string         `bun:"sort_title,notnull" json:"sortTitle"`
	Status             string         `bun:"status,notnull" json:"status"`
	Monitored          bool           `bun:"monitored,notnull" json:"monitored"`
	MonitorNew         string         `bun:"monitor_new,notnull" json:"monitorNew"` // "all" | "none"
	RootFolderID       int64          `bun:"root_folder_id,notnull" json:"rootFolderId"`
	Path               string         `bun:"path,notnull" json:"path"` // folder name under the root folder
	ProfileID          int64          `bun:"profile_id,notnull" json:"profileId"`
	Language           string         `bun:"language,notnull" json:"language"`
	SourcePriorityMode string         `bun:"source_priority_mode,notnull,default:'custom'" json:"sourcePriorityMode" enum:"inherit,custom"`
	ReadingDirection   string         `bun:"reading_direction,notnull" json:"readingDirection"` // rtl | ltr | vertical | webtoon
	Tags               []int64        `bun:"tags,notnull" json:"tags"`
	Metadata           SeriesMetadata `bun:"metadata,notnull" json:"metadata"`
	AddOptions         AddOptions     `bun:"add_options,notnull" json:"addOptions"`
	// BlockedScanlators are scanlator names never downloaded for this series
	// (in addition to the profile's patterns).
	BlockedScanlators   []string   `bun:"blocked_scanlators,notnull" json:"blockedScanlators,omitempty"`
	AddedAt             time.Time  `bun:"added_at,notnull" json:"addedAt"`
	UpdatedAt           time.Time  `bun:"updated_at,notnull" json:"updatedAt"`
	LastMetadataRefresh *time.Time `bun:"last_metadata_refresh" json:"lastMetadataRefresh,omitempty"`
	// Preview marks a title opened from search without adding it: chapters
	// stream from the source, nothing is downloaded, and it is left out of
	// the library until added. PreviewSeenAt is when it was last opened.
	Preview       bool       `bun:"preview,notnull" json:"preview,omitempty"`
	PreviewSeenAt *time.Time `bun:"preview_seen_at" json:"-"`
}

// Monitor options applied after the first chapter sync of a new series.
const (
	MonitorAll    = "all"
	MonitorFuture = "future"
	MonitorLatest = "latest"
	MonitorFrom   = "from"
	MonitorNone   = "none"
)

// AddOptions are applied on the first successful sync, then cleared (Pending=false).
type AddOptions struct {
	Pending       bool    `json:"pending"`
	Monitor       string  `json:"monitor,omitempty"`
	LatestCount   int     `json:"latestCount,omitempty"`
	FromChapter   float64 `json:"fromChapter,omitempty"`
	SearchMissing bool    `json:"searchMissing,omitempty"`
}

// Adaptation is an anime related to a series by its metadata provider.
type Adaptation struct {
	Title       string            `json:"title"`
	Format      string            `json:"format"` // tv | tv_short | movie | ova | ona | special
	Year        int               `json:"year,omitempty"`
	CoverURL    string            `json:"coverUrl,omitempty"`
	ExternalIDs map[string]string `json:"externalIds"`
	Links       map[string]string `json:"links"`
}

// SeriesMetadata is the merged metadata of a series plus provenance and locks.
type SeriesMetadata struct {
	Adaptations   []Adaptation      `json:"adaptations,omitempty"`
	AltTitles     []string          `json:"altTitles,omitempty"`
	Description   string            `json:"description,omitempty"`
	Year          int               `json:"year,omitempty"`
	Authors       []string          `json:"authors,omitempty"`
	Artists       []string          `json:"artists,omitempty"`
	Genres        []string          `json:"genres,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	Publisher     string            `json:"publisher,omitempty"`
	CoverURL      string            `json:"coverUrl,omitempty"`
	Links         map[string]string `json:"links,omitempty"`       // label -> url
	ExternalIDs   map[string]string `json:"externalIds,omitempty"` // "anilist" -> "30013"
	AgeRating     string            `json:"ageRating,omitempty"`
	Format        string            `json:"format,omitempty"` // manga | manhwa | manhua | comic | oneshot
	TotalChapters int               `json:"totalChapters,omitempty"`
	// Provenance maps a field name to the module that supplied it ("anilist", "source", "user").
	Provenance map[string]string `json:"provenance,omitempty"`
	// Locks lists fields the user edited; refreshes do not overwrite them.
	Locks []string `json:"locks,omitempty"`
}

func (m SeriesMetadata) Locked(field string) bool {
	for _, l := range m.Locks {
		if l == field {
			return true
		}
	}
	return false
}

type SeriesSource struct {
	bun.BaseModel        `bun:"table:series_sources"`
	ID                   int64      `bun:"id,pk,autoincrement" json:"id"`
	SeriesID             int64      `bun:"series_id,notnull" json:"seriesId"`
	ModuleID             int64      `bun:"module_id,notnull" json:"moduleId"`
	SourceID             string     `bun:"source_id,notnull" json:"sourceId"`
	SourceName           string     `bun:"source_name,notnull" json:"sourceName"`
	Lang                 string     `bun:"lang,notnull" json:"lang"`
	MangaURL             string     `bun:"manga_url,notnull" json:"mangaUrl"`
	Title                string     `bun:"title,notnull" json:"title"`
	WebURL               string     `bun:"web_url,notnull" json:"webUrl"`
	EngineRef            string     `bun:"engine_ref,notnull" json:"-"`
	Priority             int        `bun:"priority,notnull" json:"priority"`
	Enabled              bool       `bun:"enabled,notnull" json:"enabled"`
	CheckIntervalMinutes int        `bun:"check_interval_minutes,notnull" json:"checkIntervalMinutes"` // 0 = automatic
	LastCheckedAt        *time.Time `bun:"last_checked_at" json:"lastCheckedAt,omitempty"`
	LastSuccessAt        *time.Time `bun:"last_success_at" json:"lastSuccessAt,omitempty"`
	NextCheckAt          time.Time  `bun:"next_check_at,notnull" json:"nextCheckAt"`
	ConsecutiveFailures  int        `bun:"consecutive_failures,notnull" json:"consecutiveFailures"`
	BackoffUntil         *time.Time `bun:"backoff_until" json:"backoffUntil,omitempty"`
	LastError            string     `bun:"last_error,notnull" json:"lastError"`
	CreatedAt            time.Time  `bun:"created_at,notnull" json:"createdAt"`
	EffectivePriority    *int       `bun:"-" json:"effectivePriority,omitempty"`
	// Chapters and Files are filled in series details: the chapters this
	// link offers, and the chapter files that were downloaded from it.
	Chapters *int `bun:"-" json:"chapters,omitempty"`
	Files    *int `bun:"-" json:"files,omitempty"`
}

// ---- Chapters ---------------------------------------------------------------

const (
	ChapterMissing     = "missing"
	ChapterQueued      = "queued"
	ChapterDownloading = "downloading"
	ChapterProcessing  = "processing"
	ChapterImported    = "imported"
	ChapterCleaned     = "cleaned"
	ChapterFailed      = "failed"
)

type Chapter struct {
	bun.BaseModel `bun:"table:chapters"`
	ID            int64      `bun:"id,pk,autoincrement" json:"id"`
	SeriesID      int64      `bun:"series_id,notnull" json:"seriesId"`
	NumberKey     string     `bun:"number_key,notnull" json:"number"`
	NumberSort    float64    `bun:"number_sort,notnull" json:"numberSort"`
	Volume        string     `bun:"volume,notnull" json:"volume"`
	Title         string     `bun:"title,notnull" json:"title"`
	Monitored     bool       `bun:"monitored,notnull" json:"monitored"`
	State         string     `bun:"state,notnull" json:"state"`
	FileID        *int64     `bun:"file_id" json:"fileId,omitempty"`
	CleanedAt     *time.Time `bun:"cleaned_at" json:"cleanedAt,omitempty"`
	ReleaseDate   *time.Time `bun:"release_date" json:"releaseDate,omitempty"`
	FirstSeenAt   time.Time  `bun:"first_seen_at,notnull" json:"firstSeenAt"`
	UpdatedAt     time.Time  `bun:"updated_at,notnull" json:"updatedAt"`
}

type ChapterRelease struct {
	bun.BaseModel  `bun:"table:chapter_releases"`
	ID             int64      `bun:"id,pk,autoincrement" json:"id"`
	SeriesID       int64      `bun:"series_id,notnull" json:"seriesId"`
	ChapterID      *int64     `bun:"chapter_id" json:"chapterId,omitempty"` // nil = unparsed number, needs manual mapping
	SeriesSourceID int64      `bun:"series_source_id,notnull" json:"seriesSourceId"`
	ChapterURL     string     `bun:"chapter_url,notnull" json:"chapterUrl"`
	WebURL         string     `bun:"web_url,notnull" json:"webUrl"`
	EngineRef      string     `bun:"engine_ref,notnull" json:"-"`
	Name           string     `bun:"name,notnull" json:"name"`
	Scanlator      string     `bun:"scanlator,notnull" json:"scanlator"`
	RawNumber      float64    `bun:"raw_number,notnull" json:"rawNumber"`
	UploadDate     *time.Time `bun:"upload_date" json:"uploadDate,omitempty"`
	Removed        bool       `bun:"removed,notnull" json:"removed"`
	CreatedAt      time.Time  `bun:"created_at,notnull" json:"createdAt"`
}

type ChapterFile struct {
	SourcePages   []int `bun:"source_pages" json:"sourcePages,omitempty"`
	bun.BaseModel `bun:"table:chapter_files"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id"`
	ChapterID     int64     `bun:"chapter_id,notnull" json:"chapterId"`
	SeriesID      int64     `bun:"series_id,notnull" json:"seriesId"`
	RelativePath  string    `bun:"relative_path,notnull" json:"relativePath"`
	Size          int64     `bun:"size,notnull" json:"size"`
	PageCount     int       `bun:"page_count,notnull" json:"pageCount"`
	AvgWidth      int       `bun:"avg_width,notnull" json:"avgWidth"`
	Format        string    `bun:"format,notnull" json:"format"`
	ReleaseID     *int64    `bun:"release_id" json:"releaseId,omitempty"`
	Scanlator     string    `bun:"scanlator,notnull" json:"scanlator"`
	SourceName    string    `bun:"source_name,notnull" json:"sourceName"`
	SHA256        string    `bun:"sha256,notnull" json:"sha256"`
	Upscaled      bool      `bun:"upscaled,notnull" json:"upscaled"`
	UpscaleModel  string    `bun:"upscale_model,notnull" json:"upscaleModel"`
	SizeBefore    int64     `bun:"size_before,notnull" json:"sizeBefore"`
	ImportedAt    time.Time `bun:"imported_at,notnull" json:"importedAt"`

	// Background processing state (see internal/processing).
	ProcessParams   string     `bun:"process_params,notnull" json:"-"`
	ProcessState    string     `bun:"process_state,notnull" json:"processState,omitempty" enum:",done,failed"`
	ProcessAttempts int        `bun:"process_attempts,notnull" json:"processAttempts"`
	ProcessRetryAt  *time.Time `bun:"process_retry_at" json:"processRetryAt,omitempty"`
	ProcessError    string     `bun:"process_error,notnull" json:"processError,omitempty"`
	ProcessedAt     *time.Time `bun:"processed_at" json:"processedAt,omitempty"`
	// SizeOriginal is the size as downloaded, before any processing.
	SizeOriginal int64 `bun:"size_original,notnull" json:"sizeOriginal"`
	// ProcessSeconds and ProcessPages describe the last processing run.
	ProcessSeconds float64 `bun:"process_seconds,notnull" json:"processSeconds,omitempty"`
	ProcessPages   int     `bun:"process_pages,notnull" json:"processPages,omitempty"`
}

// ---- Queue / history / blocklist ---------------------------------------------

const (
	JobQueued      = "queued"
	JobPaused      = "paused"
	JobDownloading = "downloading"
	JobProcessing  = "processing"
	JobImporting   = "importing"
	JobCompleted   = "completed"
	JobFailed      = "failed"

	JobKindDownload  = "download"
	JobKindReprocess = "reprocess" // re-run processing (e.g. upscale) on an existing file
)

// PriorityReading is the queue priority of a chapter someone opened: it goes
// before everything queued in the background.
const PriorityReading = 100

type DownloadJob struct {
	ProfileName    string         `bun:"profile_name,notnull" json:"profileName,omitempty"`
	RecycledFileID *int64         `bun:"recycled_file_id" json:"recycledFileId,omitempty"`
	ConfigOverride *ProfileConfig `bun:"config_override,type:jsonb" json:"configOverride,omitempty"`
	ForceDownload  bool           `bun:"force_download,notnull" json:"forceDownload"`
	PinRelease     bool           `bun:"pin_release,notnull" json:"pinRelease"`
	bun.BaseModel  `bun:"table:download_jobs"`
	ID             int64  `bun:"id,pk,autoincrement" json:"id"`
	Kind           string `bun:"kind,notnull" json:"kind"`
	SeriesID       int64  `bun:"series_id,notnull" json:"seriesId"`
	ChapterID      int64  `bun:"chapter_id,notnull" json:"chapterId"`
	ReleaseID      *int64 `bun:"release_id" json:"releaseId,omitempty"`
	Status         string `bun:"status,notnull" json:"status"`
	// Priority is the legacy enqueue hint (higher first), retained for clients.
	// Rank is the durable global order: lower first, with running jobs pinned.
	Rank       int64      `bun:"rank,notnull" json:"rank"`
	Priority   int        `bun:"priority,notnull" json:"priority"`
	Progress   int        `bun:"progress,notnull" json:"progress"`
	PagesDone  int        `bun:"pages_done,notnull" json:"pagesDone"`
	PagesTotal int        `bun:"pages_total,notnull" json:"pagesTotal"`
	Attempt    int        `bun:"attempt,notnull" json:"attempt"`
	IsUpgrade  bool       `bun:"is_upgrade,notnull" json:"isUpgrade"`
	Error      string     `bun:"error,notnull" json:"error"`
	NotBefore  time.Time  `bun:"not_before,notnull" json:"notBefore"`
	CreatedAt  time.Time  `bun:"created_at,notnull" json:"createdAt"`
	UpdatedAt  time.Time  `bun:"updated_at,notnull" json:"updatedAt"`
	StartedAt  *time.Time `bun:"started_at" json:"startedAt,omitempty"`
}

const (
	HistoryFound     = "found"
	HistoryGrabbed   = "grabbed"
	HistoryImported  = "imported"
	HistoryUpgraded  = "upgraded"
	HistoryFailed    = "failed"
	HistoryDeleted   = "deleted"
	HistoryCleaned   = "cleaned"
	HistoryRestored  = "restored"
	HistoryUpscaled  = "upscaled"
	HistoryProcessed = "processed"
	HistoryUnparsed  = "unparsed"
	HistoryRetitled  = "renamed"
	HistoryMoved     = "moved"
	HistoryProgress  = "progressRestored"
	HistoryBlocklist = "blocklisted"
	// HistoryReadAhead: chapters after a reader's position were monitored to be downloaded.
	HistoryReadAhead = "readAhead"
)

type History struct {
	bun.BaseModel `bun:"table:history"`
	ID            int64             `bun:"id,pk,autoincrement" json:"id"`
	SeriesID      int64             `bun:"series_id,notnull" json:"seriesId"`
	ChapterID     *int64            `bun:"chapter_id" json:"chapterId,omitempty"`
	EventType     string            `bun:"event_type,notnull" json:"eventType"`
	SourceTitle   string            `bun:"source_title,notnull" json:"sourceTitle"`
	Data          map[string]string `bun:"data,notnull" json:"data"`
	CreatedAt     time.Time         `bun:"created_at,notnull" json:"createdAt"`
}

type Blocklist struct {
	bun.BaseModel  `bun:"table:blocklist"`
	ID             int64     `bun:"id,pk,autoincrement" json:"id"`
	SeriesID       int64     `bun:"series_id,notnull" json:"seriesId"`
	ChapterID      *int64    `bun:"chapter_id" json:"chapterId,omitempty"`
	SeriesSourceID int64     `bun:"series_source_id,notnull" json:"seriesSourceId"`
	ChapterURL     string    `bun:"chapter_url,notnull" json:"chapterUrl"`
	Scanlator      string    `bun:"scanlator,notnull" json:"scanlator"`
	Reason         string    `bun:"reason,notnull" json:"reason"`
	CreatedAt      time.Time `bun:"created_at,notnull" json:"createdAt"`
}

// ---- Commands / tasks --------------------------------------------------------

const (
	CommandQueued    = "queued"
	CommandStarted   = "started"
	CommandCompleted = "completed"
	CommandFailed    = "failed"
	CommandOrphaned  = "orphaned"
)

type Command struct {
	bun.BaseModel `bun:"table:commands"`
	ID            int64          `bun:"id,pk,autoincrement" json:"id"`
	Name          string         `bun:"name,notnull" json:"name"`
	Body          map[string]any `bun:"body,notnull" json:"body"`
	Status        string         `bun:"status,notnull" json:"status"`
	Trigger       string         `bun:"trigger,notnull" json:"trigger"`
	Message       string         `bun:"message,notnull" json:"message"`
	QueuedAt      time.Time      `bun:"queued_at,notnull" json:"queuedAt"`
	StartedAt     *time.Time     `bun:"started_at" json:"startedAt,omitempty"`
	EndedAt       *time.Time     `bun:"ended_at" json:"endedAt,omitempty"`
	DurationMs    int64          `bun:"duration_ms,notnull" json:"durationMs"`
	Error         string         `bun:"error,notnull" json:"error"`
}

type ScheduledTask struct {
	bun.BaseModel   `bun:"table:scheduled_tasks"`
	Name            string     `bun:"name,pk" json:"name"`
	IntervalMinutes int        `bun:"interval_minutes,notnull" json:"intervalMinutes"`
	LastExecution   *time.Time `bun:"last_execution" json:"lastExecution,omitempty"`
	LastStart       *time.Time `bun:"last_start" json:"lastStart,omitempty"`
	// User overrides; a nil ScheduleKind means the code default (IntervalMinutes).
	Paused                bool      `bun:"paused,notnull" json:"paused"`
	ScheduleKind          *string   `bun:"schedule_kind" json:"scheduleKind,omitempty"`
	CustomIntervalMinutes *int      `bun:"custom_interval_minutes" json:"customIntervalMinutes,omitempty"`
	TimesOfDay            []string  `bun:"times_of_day,type:json" json:"timesOfDay"`
	Weekdays              []string  `bun:"weekdays,type:json" json:"weekdays"`
	UpdatedAt             time.Time `bun:"updated_at,notnull" json:"updatedAt"`
}

// ---- Readers / progress ------------------------------------------------------

type Reader struct {
	bun.BaseModel   `bun:"table:readers"`
	ID              int64     `bun:"id,pk,autoincrement" json:"id"`
	Name            string    `bun:"name,notnull" json:"name"`
	CountForCleanup bool      `bun:"count_for_cleanup,notnull" json:"countForCleanup"`
	CreatedAt       time.Time `bun:"created_at,notnull" json:"createdAt"`
}

type ReaderAccount struct {
	bun.BaseModel `bun:"table:reader_accounts"`
	ID            int64             `bun:"id,pk,autoincrement" json:"id"`
	ReaderID      int64             `bun:"reader_id,notnull" json:"readerId"`
	ModuleID      int64             `bun:"module_id,notnull" json:"moduleId"`
	Credentials   map[string]string `bun:"credentials,notnull" json:"-"`
	ExternalUser  string            `bun:"external_user,notnull" json:"externalUser"`
	LastSyncAt    *time.Time        `bun:"last_sync_at" json:"lastSyncAt,omitempty"`
	LastError     string            `bun:"last_error,notnull" json:"lastError"`
	CreatedAt     time.Time         `bun:"created_at,notnull" json:"createdAt"`
}

// ChapterReadState is a reader's progress in a chapter. Progress belongs to
// the title: chapter_read_states is a view that shows a TitleReadState on
// that chapter of every language edition, so this is read-only. Write
// through internal/readstate.
type ChapterReadState struct {
	bun.BaseModel `bun:"table:chapter_read_states"`
	// ID is the TitleReadState's: editions of a title share it.
	ID        int64      `bun:"id,pk" json:"id"`
	ReaderID  int64      `bun:"reader_id,notnull" json:"readerId"`
	ChapterID int64      `bun:"chapter_id,notnull" json:"chapterId"`
	SeriesID  int64      `bun:"series_id,notnull" json:"seriesId"`
	Completed bool       `bun:"completed,notnull" json:"completed"`
	Page      int        `bun:"page,notnull" json:"page"`
	ReadAt    *time.Time `bun:"read_at" json:"readAt,omitempty"`
	SyncedAt  time.Time  `bun:"synced_at,notnull" json:"syncedAt"`
	// Origin is "" for states read from a library server and ReadOriginBackup
	// for imported ones (kept until a server reports the chapter).
	Origin string `bun:"origin,notnull" json:"origin,omitempty"`
	// SourceChapterID is the chapter the state was last reported on; it
	// differs from ChapterID when it came from another language edition.
	SourceChapterID int64 `bun:"source_chapter_id" json:"-"`
}

// TitleReadState is a reader's progress in one chapter number of a title,
// shared by the title's language editions.
type TitleReadState struct {
	bun.BaseModel `bun:"table:title_read_states"`
	ID            int64 `bun:"id,pk,autoincrement"`
	ReaderID      int64 `bun:"reader_id,notnull"`
	// TitleID is the work's id, or minus the series id without a work.
	TitleID   int64  `bun:"title_id,notnull"`
	NumberKey string `bun:"number_key,notnull"`
	// ChapterID is the chapter the state was last reported on.
	ChapterID int64      `bun:"chapter_id,notnull"`
	Completed bool       `bun:"completed,notnull"`
	Page      int        `bun:"page,notnull"`
	ReadAt    *time.Time `bun:"read_at"`
	SyncedAt  time.Time  `bun:"synced_at,notnull"`
	Origin    string     `bun:"origin,notnull"`
}

// ReadOriginBackup marks read states imported from a backup.
const ReadOriginBackup = "backup"
