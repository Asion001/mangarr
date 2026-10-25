// Package backupimport reads library backups of other manga apps (Mihon,
// Tachiyomi and its forks, Suwayomi, Aidoku) into one normalized model.
// Mapping the entries to catalogs and adding them lives in internal/imports.
package backupimport

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// Formats.
const (
	FormatMihon  = "mihon"  // Mihon, Tachiyomi, forks and Suwayomi (.tachibk, .proto.gz)
	FormatAidoku = "aidoku" // Aidoku (.aib)
	// FormatAccount is a manga site account read through its source (not a
	// file): entries carry the source id and ReadUpTo instead of chapters.
	FormatAccount = "account"
)

// Tracker names used in BackupManga.Trackers (match metadata provider names).
const (
	TrackerAniList      = "anilist"
	TrackerMAL          = "mal"
	TrackerMangaUpdates = "mangaupdates"
	TrackerKitsu        = "kitsu"
)

// Backup is a parsed backup.
type Backup struct {
	Format string `json:"format"`
	// CreatedAt is when the backup was made (zero when unknown).
	CreatedAt time.Time `json:"createdAt,omitzero"`
	// Sources maps source ids to their names.
	Sources    map[string]string `json:"sources"`
	Categories []string          `json:"categories"`
	Entries    []BackupManga     `json:"entries"`
}

// BackupManga is one manga of the backup.
type BackupManga struct {
	// SourceID is the app's source id: a Mihon source id (decimal int64,
	// the same as Keiyoushi/Suwayomi catalog ids) or an Aidoku source id
	// such as "multi.mangadex".
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName,omitempty"`
	// URL is the source's manga key: a path for Mihon ("/manga/123"), the
	// manga id for Aidoku.
	URL string `json:"url"`
	// WebURL is the full web address when the app stores it (Aidoku).
	WebURL string `json:"webUrl,omitempty"`
	Title  string `json:"title"`
	// AltTitles are other titles the source knows (a site account).
	AltTitles    []string  `json:"altTitles,omitempty"`
	Author       string    `json:"author,omitempty"`
	Artist       string    `json:"artist,omitempty"`
	Description  string    `json:"description,omitempty"`
	Genres       []string  `json:"genres,omitempty"`
	Status       string    `json:"status"`
	ThumbnailURL string    `json:"thumbnailUrl,omitempty"`
	Favorite     bool      `json:"favorite"`
	AddedAt      time.Time `json:"addedAt,omitzero"`
	Categories   []string  `json:"categories,omitempty"`
	// Trackers maps a tracker (TrackerAniList, ...) to the series id there.
	Trackers           map[string]string `json:"trackers,omitempty"`
	ExcludedScanlators []string          `json:"excludedScanlators,omitempty"`
	Chapters           []BackupChapter   `json:"chapters,omitempty"`
	// ReadUpTo is how far the title was read, for sources that keep only
	// that (a site account) instead of each chapter's state.
	ReadUpTo *ReadUpTo `json:"readUpTo,omitempty"`
}

// ReadUpTo is a reading position: chapters numbered below Chapter are read,
// and Chapter is open at Page (1-based, 0 when not started).
type ReadUpTo struct {
	Chapter float64 `json:"chapter"`
	Page    int     `json:"page,omitempty"`
	// ChapterRef is the source's id of the chapter reached when only that is
	// known (Chapter is then -1 until the chapter list resolves it).
	ChapterRef string `json:"chapterRef,omitempty"`
	// ChapterPlace is that chapter's place in the title's list (1-based),
	// used when its id can't be found.
	ChapterPlace int `json:"chapterPlace,omitempty"`
}

// readBelow is whether n counts as read under u.
func (u *ReadUpTo) readBelow(n float64) bool { return n >= 0 && n < u.Chapter-0.0005 }

// BackupChapter is one chapter of an entry with its read state.
type BackupChapter struct {
	URL       string  `json:"url"`
	Name      string  `json:"name,omitempty"`
	Scanlator string  `json:"scanlator,omitempty"`
	Lang      string  `json:"lang,omitempty"`
	Number    float64 `json:"number"` // -1 when unknown
	Read      bool    `json:"read,omitempty"`
	// LastPageRead is the 0-based page reached (0 when not started).
	LastPageRead int        `json:"lastPageRead,omitempty"`
	ReadAt       *time.Time `json:"readAt,omitempty"`
}

// ReadCount returns how many chapters are read (estimated from ReadUpTo
// when there is no chapter list: chapters usually start at 1).
func (e BackupManga) ReadCount() int {
	n := 0
	for _, c := range e.Chapters {
		if c.Read {
			n++
		}
	}
	if len(e.Chapters) == 0 && e.ReadUpTo != nil {
		n = max(int(math.Ceil(e.ReadUpTo.Chapter-0.0005))-1, 0)
	}
	return n
}

// ReadsChapter reports whether the chapter numbered n is read under ReadUpTo.
func (e BackupManga) ReadsChapter(n float64) bool {
	return e.ReadUpTo != nil && e.ReadUpTo.readBelow(n)
}

// ErrLegacyJSON is returned for Tachiyomi's old JSON backups.
var ErrLegacyJSON = errors.New("this is an old Tachiyomi JSON backup; restore it in Mihon and create a new backup")

// ErrUnknownFormat is returned for files that aren't a supported backup.
var ErrUnknownFormat = errors.New("not a Mihon, Tachiyomi, Suwayomi or Aidoku backup")

// MaxSize is the largest backup accepted (decompressed).
const MaxSize = 256 << 20

// Parse detects the format and parses a backup.
func Parse(data []byte) (*Backup, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		raw, err := io.ReadAll(io.LimitReader(zr, MaxSize+1))
		if err != nil {
			return nil, fmt.Errorf("gzip: %w", err)
		}
		if len(raw) > MaxSize {
			return nil, errors.New("backup is too large")
		}
		data = raw
	}
	switch {
	case bytes.HasPrefix(data, []byte("bplist00")):
		return parseAidoku(data)
	case bytes.HasPrefix(data, []byte("<?xml")):
		return parseAidoku(data)
	case len(bytes.TrimSpace(data)) > 0 && bytes.TrimSpace(data)[0] == '{':
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(data, &probe); err != nil {
			return nil, ErrUnknownFormat
		}
		if _, ok := probe["mangas"]; ok {
			return nil, ErrLegacyJSON
		}
		if _, ok := probe["version"]; ok && probe["library"] == nil && probe["manga"] == nil {
			return nil, ErrLegacyJSON
		}
		return parseAidokuJSON(data)
	case len(data) > 0 && data[0]&7 == 2 && data[0]>>3 > 0: // a length-delimited protobuf field
		return parseMihon(data)
	case len(data) == 0:
		return nil, errors.New("the backup is empty")
	}
	return nil, ErrUnknownFormat
}

// ResumeFrom returns the chapter number to monitor from: just after the
// highest read chapter. ok is false when nothing was read.
func (e BackupManga) ResumeFrom() (from float64, ok bool) {
	if u := e.ReadUpTo; len(e.Chapters) == 0 && u != nil {
		// the chapter reached is unread or half read: monitor from it
		if u.Chapter < 0 || (u.Chapter <= 1 && u.Page == 0) {
			return 0, false
		}
		return u.Chapter - 0.001, true
	}
	maxRead := -1.0
	for _, c := range e.Chapters {
		if c.Read && c.Number >= 0 && c.Number > maxRead {
			maxRead = c.Number
		}
	}
	if maxRead < 0 {
		return 0, false
	}
	next := -1.0
	for _, c := range e.Chapters {
		if !c.Read && c.Number > maxRead && (next < 0 || c.Number < next) {
			next = c.Number
		}
	}
	if next < 0 {
		// everything known is read: only chapters after the last one
		return maxRead + 0.001, true
	}
	// float32 numbers in backups (10.1 -> 10.100000381) must not skip 10.1
	return next - 0.001, true
}
