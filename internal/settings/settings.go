// Package settings stores runtime-editable global settings as JSON documents
// in the settings table, with typed accessors and defaults.
package settings

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/model"
)

// General holds server-wide settings.
type General struct {
	APIKey        string `json:"apiKey" env:"=API_KEY" secret:"true" desc:"API key for X-Api-Key (generated on first start)."`
	SessionSecret string `json:"sessionSecret" env:"-"`
	// InstanceName is shown in the UI and notifications.
	InstanceName string `json:"instanceName" desc:"Name shown in the UI and notifications."`
	// PublicURL is used in notification links (e.g. https://mangarr.example.com).
	PublicURL string `json:"publicUrl" desc:"External URL used in notification links."`
	// BackupRetention is the number of scheduled backups to keep.
	BackupRetention int `json:"backupRetention" desc:"Number of scheduled backups to keep."`
	// ImageCacheMaxMB caps the thumbnail/cover cache (oldest files go first).
	ImageCacheMaxMB int `json:"imageCacheMaxMb" desc:"Maximum size of the image cache (MB, 0 = unlimited)."`
}

// Appearance is how the web UI looks for everyone, and the defaults for
// people who haven't chosen their own.
type Appearance struct {
	// Accent is the instance's accent colour (#rrggbb, empty = mangarr orange).
	Accent string `json:"accent" pattern:"^(#[0-9a-fA-F]{6})?$" desc:"Accent colour (#rrggbb, empty = default)."`
	// LoginMessage is shown on the sign-in page.
	LoginMessage string `json:"loginMessage" maxLength:"500" desc:"Message on the sign-in page."`
	// Theme, StartPage and Locale are the defaults for new users.
	Theme     string `json:"theme" enum:"dark,light,system" desc:"Default theme."`
	StartPage string `json:"startPage" enum:"series,discover,updates,continue" desc:"Default start page."`
	Locale    string `json:"locale" enum:"auto,en,ru,uk" desc:"Default interface language."`
}

func DefaultAppearance() Appearance {
	return Appearance{Theme: "dark", StartPage: "series", Locale: "auto"}
}

func (s *Store) Appearance(ctx context.Context) (Appearance, error) {
	v := DefaultAppearance()
	return v, s.Get(ctx, KeyAppearance, &v)
}

// MediaManagement controls file naming and import behavior.
type MediaManagement struct {
	// ChapterFormat is the file name template (without extension).
	ChapterFormat string `json:"chapterFormat" desc:"Chapter file name template (without extension)."`
	// SeriesFolderFormat is the folder name template for new series.
	SeriesFolderFormat string `json:"seriesFolderFormat" desc:"Folder name template for new series."`
	// RecycleBinPath receives replaced/cleaned files; empty = <dataDir>/recycle.
	RecycleBinPath string `json:"recycleBinPath" desc:"Where replaced and cleaned files go (empty = <data dir>/recycle)."`
	// RecycleBinDays purges recycled files older than N days (0 = never).
	RecycleBinDays int `json:"recycleBinDays" desc:"Purge recycled files older than N days (0 = never)."`
	// MinFreeSpaceMB aborts downloads when a root folder has less free space.
	MinFreeSpaceMB int `json:"minFreeSpaceMb" desc:"Stop downloading when a root folder has less free space (MB)."`
	// WriteSeriesJSON writes a Mylar-style series.json (read by Komga).
	WriteSeriesJSON bool `json:"writeSeriesJson" desc:"Write a Mylar series.json (read by Komga)."`
	// WriteCover writes cover.jpg into series folders.
	WriteCover bool `json:"writeCover" desc:"Write cover.jpg into series folders."`
	// WriteVolume writes <Volume> into ComicInfo.xml. Off by default: volume
	// numbers change Kavita's grouping and Komga's default series titles.
	WriteVolume bool `json:"writeVolume" desc:"Write <Volume> into ComicInfo.xml."`
	// RenameFolderOnTitleChange renames the series folder when its title changes.
	RenameFolderOnTitleChange bool `json:"renameFolderOnTitleChange" desc:"Rename the series folder when its title changes."`
	// FileMode / DirMode for created files (octal strings like "0664").
	FileMode string `json:"fileMode" desc:"Mode of created files (octal, e.g. 0664)."`
	DirMode  string `json:"dirMode" desc:"Mode of created directories (octal, e.g. 0775)."`
}

// Downloads controls queue behavior.
type Downloads struct {
	// MaxConcurrent is the number of chapters downloaded in parallel (global).
	MaxConcurrent int `json:"maxConcurrent" desc:"Chapters downloaded in parallel (all sources)."`
	// MaxPerSource is the number of chapters downloaded in parallel per source.
	MaxPerSource int `json:"maxPerSource" desc:"Chapters downloaded in parallel per source."`
	// PageConcurrency is the number of pages fetched in parallel within a chapter.
	PageConcurrency int `json:"pageConcurrency" desc:"Pages fetched in parallel within a chapter."`
	// PageRetries per page before the chapter attempt fails.
	PageRetries int `json:"pageRetries" desc:"Retries per page before the attempt fails."`
	// MaxAttempts per release before it is blocklisted and the next source is tried.
	MaxAttempts int `json:"maxAttempts" desc:"Attempts per release before it is blocklisted."`
	// DefaultCheckIntervalMinutes for ongoing series.
	DefaultCheckIntervalMinutes int `json:"defaultCheckIntervalMinutes" desc:"Minutes between checks of ongoing series."`
	// WorkerPlacement decides where a chapter is downloaded: "auto" hands it
	// to a worker when one is online and takes it back when none is,
	// "workers" waits for one, "local" never uses them.
	WorkerPlacement string `json:"workerPlacement" desc:"Where chapters are downloaded: auto, workers or local."`
	// MaxConcurrentPerWorker is how many chapters one worker downloads at a
	// time unless that worker has its own override.
	MaxConcurrentPerWorker int `json:"maxConcurrentPerWorker" desc:"Default tasks one worker runs at a time."`
	// MaxWorkerTasks caps tasks leased across all remote workers.
	MaxWorkerTasks int `json:"maxWorkerTasks" desc:"Tasks all remote workers run at once."`
	// MaxConcurrentProcessing is how many existing chapter files may be
	// prepared and handed to local or remote processing engines at once.
	// It also caps the downloaded chapters waiting for a worker to process
	// them, which no longer hold a download slot.
	MaxConcurrentProcessing int `json:"maxConcurrentProcessing" desc:"Chapter files processed at once."`
	// WorkerPrefetch is how many pages a worker fetches ahead of what it has
	// uploaded.
	WorkerPrefetch int `json:"workerPrefetch" desc:"Pages a worker fetches ahead of what it has uploaded."`
	// MaxLocalTasks caps the downloads and chapter files this server works on
	// itself, not counting what it hands to workers; 0 sets no cap of its own
	// and a negative value (LocalOff) switches this server's own work off.
	MaxLocalTasks int `json:"maxLocalTasks" desc:"Tasks this server runs itself at once (0 = only the other limits, -1 = none: downloads and processing wait for the workers)."`
	// WorkerUpdates offers this server's build to workers running an
	// earlier one, which desktop workers then move to on their own.
	WorkerUpdates bool `json:"workerUpdates" desc:"Offer this server's build to workers on an earlier one; desktop workers update themselves."`
	// UpscaleRoutes send pages of a given scale or width to a chosen
	// upscaler and model, ahead of the priority order (System → Workers).
	UpscaleRoutes []model.UpscaleRoute `json:"upscaleRoutes" env:"-"`
}

// LocalOff reports whether this server does no downloading or processing
// of its own: chapters wait for a worker with the download role, and pages
// are processed by one with the encode role.
func (d Downloads) LocalOff() bool { return d.MaxLocalTasks < 0 }

// Validate rejects upscale routes that could never match or point nowhere.
func (d Downloads) Validate() error {
	for i, r := range d.UpscaleRoutes {
		switch r.Match {
		case model.RouteScale:
			if len(r.Scales) == 0 {
				return fmt.Errorf("upscale rule %d: pick at least one scale", i+1)
			}
			for _, s := range r.Scales {
				if s < 2 || s > 16 {
					return fmt.Errorf("upscale rule %d: %d is not a scale", i+1, s)
				}
			}
		case model.RouteWidth:
			if r.BelowWidth <= 0 {
				return fmt.Errorf("upscale rule %d: the width must be above 0", i+1)
			}
		default:
			return fmt.Errorf("upscale rule %d: match by scale or width", i+1)
		}
		if r.Target < model.RouteThisServer {
			return fmt.Errorf("upscale rule %d: no such upscaler", i+1)
		}
	}
	return nil
}

// Placements a chapter's download can be given.
const (
	PlaceAuto    = "auto"
	PlaceWorkers = "workers"
	PlaceLocal   = "local"
)

// Cleanup holds global read-based cleanup rules (off by default).
type Cleanup struct {
	Enabled bool `json:"enabled" desc:"Delete chapters every reader has finished."`
	DryRun  bool `json:"dryRun" desc:"Only report what cleanup would delete."`
	// Statuses the rule applies to ("ongoing" by default).
	Statuses []string `json:"statuses" desc:"Series statuses cleanup applies to (comma-separated)."`
	// ReaderIDs restricts the required readers (empty = all readers counting for cleanup).
	ReaderIDs               []int64  `json:"readerIds" desc:"Required reader IDs (empty = all)."`
	IgnoreReadersNotStarted bool     `json:"ignoreReadersNotStarted" desc:"Readers who never opened a series don't block its cleanup."`
	KeepLastRead            int      `json:"keepLastRead" desc:"Keep the last N read chapters."`
	GraceDays               int      `json:"graceDays" desc:"Days to wait after the last reader finished a chapter."`
	MinFreeSpaceGB          int      `json:"minFreeSpaceGb" desc:"Only clean up when free space is below this (GB, 0 = always)."`
	ExcludeTags             []string `json:"excludeTags" desc:"Series with these tags are never cleaned up."`
	UseRecycleBin           bool     `json:"useRecycleBin" desc:"Move cleaned files to the recycle bin."`
}

// ReadSync controls progress polling from library servers.
type ReadSync struct {
	IntervalMinutes int `json:"intervalMinutes" desc:"Minutes between reader progress syncs."`
}

// Reading controls the Komga-compatible API that reading apps (Mihon's
// Komga extension, KMReader, Paperback) use to read from mangarr directly.
type Reading struct {
	Enabled bool `json:"enabled" desc:"Allow Komga apps (Mihon's Komga extension, KMReader, Paperback) to connect."`
	// ReaderID is the reader whose progress the apps read and write (0 = the first reader).
	ReaderID int64 `json:"readerId" desc:"Reader whose progress Komga apps read and write (0 = the first reader)."`
	// PublicURL is the address shown in setup guides.
	PublicURL string `json:"publicUrl" desc:"Address Komga apps should use, e.g. https://manga.example.com:25600 (for the setup guides)."`
	// DownloadOnOpen queues a download when a chapter that isn't downloaded is opened.
	DownloadOnOpen bool `json:"downloadOnOpen" desc:"Queue a download when an app opens a chapter that isn't downloaded yet."`
	// ResizePages serves a smaller copy of a page when the reader asks for one.
	ResizePages bool `json:"resizePages" desc:"Serve phones and tablets a copy of the page at their screen size instead of the full scan."`
	// CDNImages gives covers and downloaded pages signed links that a CDN in
	// front of mangarr may cache.
	CDNImages bool      `json:"cdnImages" desc:"Give covers and downloaded pages signed links that a CDN in front of mangarr may cache. Anyone holding such a link can open that image without logging in."`
	ReadAhead ReadAhead `json:"readAhead"`
}

// ReadAhead downloads the chapters after the one being read.
type ReadAhead struct {
	Enabled  bool `json:"enabled" desc:"Monitor and download the next chapters after the one a reader is on."`
	Chapters int  `json:"chapters" desc:"How many chapters to keep downloaded ahead of the reader."`
}

// Sources controls which catalogs are used, quick search and throttling.
type Sources struct {
	HideNSFW bool `json:"hideNsfw" desc:"Hide NSFW catalogs in search and browse."`
	// DefaultLanguages limits searches to these catalog languages (empty = all).
	DefaultLanguages []string          `json:"defaultLanguages" desc:"Catalog languages searched by default (empty = all)."`
	LanguageDefaults []LanguageDefault `json:"languageDefaults" desc:"Default catalogs, root folder and profile for each edition language."`
	QuickSearch      QuickSearch       `json:"quickSearch"`
	// Throttle is the default request throttling for every catalog.
	Throttle model.ThrottleConfig `json:"throttle"`
}

// LanguageDefault configures the normal add path for one edition language.
// Sources contains ordered "moduleId:sourceId" catalog keys.
type LanguageDefault struct {
	Language string   `json:"language"`
	Sources  []string `json:"sources"`
	// RootFolderID is superseded by the folder's own language (#17); kept
	// until the add screen stops sending it.
	RootFolderID     int64  `json:"rootFolderId,omitempty"`
	ProfileID        int64  `json:"profileId,omitempty"`
	ReadingDirection string `json:"readingDirection,omitempty" enum:",rtl,ltr,webtoon"`
}

// ForLanguage returns a case-insensitive language default.
func (s Sources) ForLanguage(language string) (LanguageDefault, bool) {
	for _, preset := range s.LanguageDefaults {
		if strings.EqualFold(strings.TrimSpace(preset.Language), strings.TrimSpace(language)) {
			return preset, true
		}
	}
	return LanguageDefault{}, false
}

func (s Sources) Validate() error {
	seen := map[string]bool{}
	for _, preset := range s.LanguageDefaults {
		language := strings.ToLower(strings.TrimSpace(preset.Language))
		if language == "" {
			return errors.New("language defaults require a language")
		}
		if seen[language] {
			return fmt.Errorf("language %q has more than one default", preset.Language)
		}
		seen[language] = true
		if preset.ReadingDirection != "" && preset.ReadingDirection != "rtl" && preset.ReadingDirection != "ltr" && preset.ReadingDirection != "webtoon" {
			return fmt.Errorf("invalid reading direction for language %q", preset.Language)
		}
		for _, key := range preset.Sources {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("language %q has an empty source", preset.Language)
			}
		}
	}
	return nil
}

// QuickSearch searches catalogs one by one by priority and stops at the
// first confident title match.
type QuickSearch struct {
	Enabled   bool    `json:"enabled" desc:"Search catalogs one by one and stop at the first confident match."`
	Threshold float64 `json:"threshold" desc:"Title similarity (0-1) that counts as a confident match."`
	// Details fetches chapter counts (one extra request per result): none, best or top.
	Details       string `json:"details" enum:"none,best,top" desc:"Fetch chapter counts for: none, the best match, or the top N results."`
	TopN          int    `json:"topN" desc:"Results to fetch chapter counts for when details=top (1-5)."`
	BudgetSeconds int    `json:"budgetSeconds" desc:"Time limit for the one-by-one search (seconds)."`
}

// Schedule holds time windows that pause work or tighten throttling
// (e.g. "upscale only at night", "gentle during the day").
type Schedule struct {
	// Timezone is an IANA name ("Europe/Madrid"); empty = the server's local time (TZ).
	Timezone string           `json:"timezone" desc:"IANA time zone for the windows (empty = server time, TZ)."`
	Windows  []ScheduleWindow `json:"windows" desc:"Time windows as JSON: [{\"name\":\"Night\",\"days\":[\"mon\"],\"start\":\"01:00\",\"end\":\"07:00\",\"pauseDownloads\":true}]"`
}

// ScheduleWindow applies its effects between Start and End on Days.
type ScheduleWindow struct {
	Name string `json:"name"`
	// Days are mon…sun; empty = every day. A window crossing midnight
	// belongs to the day it starts.
	Days []string `json:"days"`
	// Start/End are "HH:MM"; End before Start crosses midnight.
	Start string `json:"start"`
	End   string `json:"end"`
	// PauseDownloads stops new chapter downloads; PauseProcessing stops
	// upscaling/re-encoding; Throttle applies a throttle preset.
	PauseDownloads  bool   `json:"pauseDownloads"`
	PauseProcessing bool   `json:"pauseProcessing"`
	Throttle        string `json:"throttle,omitempty" enum:",gentle,normal,fast"`
}

// ParseClock parses "HH:MM" into minutes after midnight.
func ParseClock(s string) (int, error) {
	var h, m int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d:%d", &h, &m); err != nil || h < 0 || h > 24 || m < 0 || m > 59 || (h == 24 && m != 0) {
		return 0, fmt.Errorf("invalid time %q (want HH:MM)", s)
	}
	return h*60 + m, nil
}

var weekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}

// Validate checks times, days and the time zone.
func (s Schedule) Validate() error {
	if s.Timezone != "" {
		if _, err := time.LoadLocation(s.Timezone); err != nil {
			return fmt.Errorf("unknown time zone %q", s.Timezone)
		}
	}
	for i, w := range s.Windows {
		if _, err := ParseClock(w.Start); err != nil {
			return fmt.Errorf("window %d: %w", i+1, err)
		}
		if _, err := ParseClock(w.End); err != nil {
			return fmt.Errorf("window %d: %w", i+1, err)
		}
		for _, d := range w.Days {
			if !slices.Contains(weekdays, strings.ToLower(d)) {
				return fmt.Errorf("window %d: unknown day %q (use mon…sun)", i+1, d)
			}
		}
	}
	return nil
}

// QueueState is the download queue's global pause (not a user settings
// document; changed through the queue API).
type QueueState struct {
	Paused      bool       `json:"paused"`
	PausedUntil *time.Time `json:"pausedUntil,omitempty"`
}

// Active reports whether the queue is paused at now.
func (q QueueState) Active(now time.Time) bool {
	return q.Paused && (q.PausedUntil == nil || now.Before(*q.PausedUntil))
}

func DefaultSources() Sources {
	return Sources{
		HideNSFW: true, DefaultLanguages: []string{}, LanguageDefaults: []LanguageDefault{},
		QuickSearch: QuickSearch{Enabled: true, Threshold: 0.88, Details: "best", TopN: 3, BudgetSeconds: 45},
		Throttle:    model.ThrottleConfig{Preset: "normal"},
	}
}

func DefaultGeneral() General {
	return General{InstanceName: "mangarr", BackupRetention: 7, ImageCacheMaxMB: 512}
}

func DefaultMediaManagement() MediaManagement {
	return MediaManagement{
		ChapterFormat:      "{Series Title} Ch.{Chapter:0000}",
		SeriesFolderFormat: "{Series Title}",
		RecycleBinDays:     7,
		MinFreeSpaceMB:     1024,
		WriteSeriesJSON:    true,
		WriteCover:         true,
		FileMode:           "0664",
		DirMode:            "0775",
	}
}

func DefaultDownloads() Downloads {
	return Downloads{MaxConcurrent: 3, MaxPerSource: 1, PageConcurrency: 3, PageRetries: 3, MaxAttempts: 3, DefaultCheckIntervalMinutes: 360,
		WorkerPlacement: PlaceAuto, MaxConcurrentPerWorker: 2, MaxWorkerTasks: 8, MaxConcurrentProcessing: 4, WorkerPrefetch: 50,
		WorkerUpdates: true, UpscaleRoutes: []model.UpscaleRoute{}}
}

func DefaultCleanup() Cleanup {
	return Cleanup{
		Enabled: false, DryRun: true, Statuses: []string{model.StatusOngoing},
		IgnoreReadersNotStarted: true, KeepLastRead: 1, GraceDays: 7,
		ExcludeTags: []string{"keep"}, UseRecycleBin: true,
	}
}

func DefaultReadSync() ReadSync { return ReadSync{IntervalMinutes: 30} }

func DefaultReading() Reading {
	return Reading{DownloadOnOpen: true, ResizePages: true, ReadAhead: ReadAhead{Enabled: true, Chapters: 3}}
}

// Store caches settings documents in memory.
type Store struct {
	db     *db.DB
	mu     sync.RWMutex
	cache  map[string]json.RawMessage
	warmed bool
	// overlays hold values pinned by environment variables per document.
	overlays map[string]overlay
}

type overlay struct {
	raw   json.RawMessage
	locks []Lock
}

// Lock is a settings field pinned by an environment variable.
type Lock struct {
	// Path is the JSON field path inside the document ("maxConcurrent", "quickSearch.threshold").
	Path string `json:"path"`
	Env  string `json:"env"`
}

func NewStore(d *db.DB) *Store {
	return &Store{db: d, cache: map[string]json.RawMessage{}, overlays: map[string]overlay{}}
}

// DocInfo describes a settings document.
type DocInfo struct {
	// Key is the database key, Name the API path segment, EnvPrefix the
	// variable prefix (MANGARR_<EnvPrefix>_<FIELD>).
	Key, Name, EnvPrefix string
	// Default returns a pointer to the document with defaults.
	Default func() any
}

// Docs lists every settings document.
var Docs = []DocInfo{
	{KeyGeneral, "general", "GENERAL", func() any { v := DefaultGeneral(); return &v }},
	{KeyMediaManagement, "media", "MEDIA", func() any { v := DefaultMediaManagement(); return &v }},
	{KeyDownloads, "downloads", "DOWNLOADS", func() any { v := DefaultDownloads(); return &v }},
	{KeyCleanup, "cleanup", "CLEANUP", func() any { v := DefaultCleanup(); return &v }},
	{KeyReadSync, "readsync", "READSYNC", func() any { v := DefaultReadSync(); return &v }},
	{KeySources, "sources", "SOURCES", func() any { v := DefaultSources(); return &v }},
	{KeySchedule, "schedule", "SCHEDULE", func() any { v := Schedule{Windows: []ScheduleWindow{}}; return &v }},
	{KeyReading, "reading", "READING", func() any { v := DefaultReading(); return &v }},
	{KeyAppearance, "appearance", "APPEARANCE", func() any { v := DefaultAppearance(); return &v }},
	{KeyMessenger, "messenger", "MESSENGER", func() any { v := DefaultMessenger(); return &v }},
}

// SetOverlay pins fields of document key: raw is a partial JSON object that
// is applied on top of the stored document on every read.
func (s *Store) SetOverlay(key string, raw json.RawMessage, locks []Lock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(locks) == 0 {
		delete(s.overlays, key)
		return
	}
	s.overlays[key] = overlay{raw: raw, locks: locks}
}

// Locks returns the fields of document key pinned by the environment.
func (s *Store) Locks(key string) []Lock {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Lock(nil), s.overlays[key].locks...)
}

const (
	KeyGeneral         = "general"
	KeyMediaManagement = "media_management"
	KeyDownloads       = "downloads"
	KeyCleanup         = "cleanup"
	KeyReadSync        = "read_sync"
	KeySources         = "sources"
	KeySchedule        = "schedule"
	KeyReading         = "reading"
	KeyQueueState      = "queue_state"
	KeyAppearance      = "appearance"
)

// Warm loads every stored document into the cache. Afterwards Get never
// queries the database (writes go through Set), so settings can be read
// safely while a write transaction holds a pooled SQLite connection.
func (s *Store) Warm(ctx context.Context) error {
	var rows []model.Setting
	if err := s.db.NewSelect().Model(&rows).Scan(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		s.cache[r.Key] = json.RawMessage(r.Value)
	}
	s.warmed = true
	return nil
}

// Get decodes the document at key into out (which must hold defaults).
func (s *Store) Get(ctx context.Context, key string, out any) error {
	s.mu.RLock()
	raw, ok := s.cache[key]
	warmed := s.warmed
	s.mu.RUnlock()
	s.mu.RLock()
	ov := s.overlays[key]
	s.mu.RUnlock()
	if !ok && warmed {
		return applyOverlay(ov, out) // not stored: keep defaults
	}
	if !ok {
		var row model.Setting
		err := s.db.NewSelect().Model(&row).Where("key = ?", key).Scan(ctx)
		if errors.Is(err, sql.ErrNoRows) {
			// remember "not stored" so defaults don't cost a query each time
			s.mu.Lock()
			s.cache[key] = json.RawMessage("null")
			s.mu.Unlock()
			return nil
		}
		if err != nil {
			return err
		}
		raw = json.RawMessage(row.Value)
		s.mu.Lock()
		s.cache[key] = raw
		s.mu.Unlock()
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return err
	}
	return applyOverlay(ov, out)
}

func applyOverlay(ov overlay, out any) error {
	if len(ov.raw) == 0 {
		return nil
	}
	return json.Unmarshal(ov.raw, out)
}

// Set stores v at key. Fields pinned by the environment keep their stored
// value, so removing the variable later restores what was saved before.
func (s *Store) Set(ctx context.Context, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if locks := s.Locks(key); len(locks) > 0 {
		s.mu.RLock()
		old := s.cache[key]
		s.mu.RUnlock()
		if b, err = keepLocked(b, old, locks); err != nil {
			return err
		}
	}
	row := &model.Setting{Key: key, Value: string(b), UpdatedAt: time.Now().UTC()}
	_, err = s.db.NewInsert().Model(row).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("updated_at = EXCLUDED.updated_at").
		Exec(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.cache[key] = b
	s.mu.Unlock()
	return nil
}

func (s *Store) General(ctx context.Context) (General, error) {
	v := DefaultGeneral()
	return v, s.Get(ctx, KeyGeneral, &v)
}

func (s *Store) MediaManagement(ctx context.Context) (MediaManagement, error) {
	v := DefaultMediaManagement()
	return v, s.Get(ctx, KeyMediaManagement, &v)
}

func (s *Store) Downloads(ctx context.Context) (Downloads, error) {
	v := DefaultDownloads()
	return v, s.Get(ctx, KeyDownloads, &v)
}

func (s *Store) Cleanup(ctx context.Context) (Cleanup, error) {
	v := DefaultCleanup()
	return v, s.Get(ctx, KeyCleanup, &v)
}

func (s *Store) Sources(ctx context.Context) (Sources, error) {
	v := DefaultSources()
	return v, s.Get(ctx, KeySources, &v)
}

func (s *Store) Schedule(ctx context.Context) (Schedule, error) {
	v := Schedule{Windows: []ScheduleWindow{}}
	return v, s.Get(ctx, KeySchedule, &v)
}

func (s *Store) QueueState(ctx context.Context) (QueueState, error) {
	var v QueueState
	return v, s.Get(ctx, KeyQueueState, &v)
}

func (s *Store) ReadSync(ctx context.Context) (ReadSync, error) {
	v := DefaultReadSync()
	if err := s.Get(ctx, KeyReadSync, &v); err != nil {
		return v, err
	}
	var task model.ScheduledTask
	err := s.db.NewSelect().Model(&task).Where("name = ?", "SyncReadProgress").Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	if err != nil {
		return v, err
	}
	v.IntervalMinutes = task.IntervalMinutes
	if task.CustomIntervalMinutes != nil {
		v.IntervalMinutes = *task.CustomIntervalMinutes
	}
	s.mu.RLock()
	ov := s.overlays[KeyReadSync]
	s.mu.RUnlock()
	return v, applyOverlay(ov, &v)
}

func (s *Store) Reading(ctx context.Context) (Reading, error) {
	v := DefaultReading()
	return v, s.Get(ctx, KeyReading, &v)
}

// EnsureSecrets generates the API key and session secret on first start.
func (s *Store) EnsureSecrets(ctx context.Context) (General, error) {
	g, err := s.General(ctx)
	if err != nil {
		return g, err
	}
	changed := false
	if g.APIKey == "" {
		g.APIKey = RandomHex(16)
		changed = true
	}
	if g.SessionSecret == "" {
		g.SessionSecret = RandomHex(32)
		changed = true
	}
	if changed {
		return g, s.Set(ctx, KeyGeneral, g)
	}
	return g, nil
}

func RandomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// keepLocked copies the locked paths of old into doc (removing them when old
// doesn't have them).
func keepLocked(doc, old json.RawMessage, locks []Lock) (json.RawMessage, error) {
	var d, o map[string]any
	if err := json.Unmarshal(doc, &d); err != nil {
		return nil, err
	}
	if len(old) > 0 {
		_ = json.Unmarshal(old, &o)
	}
	for _, l := range locks {
		parts := strings.Split(l.Path, ".")
		if v, ok := getPath(o, parts); ok {
			setPath(d, parts, v)
		} else {
			deletePath(d, parts)
		}
	}
	return json.Marshal(d)
}

func getPath(m map[string]any, parts []string) (any, bool) {
	for i, p := range parts {
		v, ok := m[p]
		if !ok {
			return nil, false
		}
		if i == len(parts)-1 {
			return v, true
		}
		if m, ok = v.(map[string]any); !ok {
			return nil, false
		}
	}
	return nil, false
}

func setPath(m map[string]any, parts []string, v any) {
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}
		m = next
	}
	m[parts[len(parts)-1]] = v
}

func deletePath(m map[string]any, parts []string) {
	for _, p := range parts[:len(parts)-1] {
		next, ok := m[p].(map[string]any)
		if !ok {
			return
		}
		m = next
	}
	delete(m, parts[len(parts)-1])
}
