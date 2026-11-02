package soundtrack

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

var ErrPackNotFound = errors.New("sound pack not found")

// AudioExts are the audio files packs may hold (played as they are).
var AudioExts = map[string]string{
	"ogg":  "audio/ogg",
	"opus": "audio/ogg",
	"mp3":  "audio/mpeg",
	"m4a":  "audio/mp4",
	"wav":  "audio/wav",
}

// Categories group tracks; each has a default icon (and a colour in the
// web UI).
var Categories = []string{"weather", "water", "nature", "city", "objects", "combat", "body", "magic", "voices", "other"}

// TrackIcons are the icons a track may have (lucide names).
var TrackIcons = []string{
	"cloud-rain", "cloud-lightning", "wind", "snowflake", "waves", "droplets", "trees", "bird", "flame",
	"building", "train-front", "car", "users", "bell", "door-open", "clock", "wine", "key", "swords",
	"bomb", "zap", "heart-pulse", "footprints", "sparkles", "wand-sparkles", "mic", "audio-lines",
}

var categoryIcon = map[string]string{
	"weather": "cloud-rain", "water": "waves", "nature": "trees", "city": "building", "objects": "bell",
	"combat": "swords", "body": "heart-pulse", "magic": "sparkles", "voices": "mic", "other": "audio-lines",
}

// Pack limits.
const (
	maxTrackSize = 300 << 20
	maxPackSize  = 2 << 30
	maxTracks    = 2000
)

// Manifest is a pack's pack.json.
type Manifest struct {
	ID          string          `json:"id"`
	Version     string          `json:"version"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	License     string          `json:"license"`
	SourceURL   string          `json:"source_url,omitempty"`
	Tracks      []ManifestTrack `json:"tracks"`
	Scenes      []ManifestScene `json:"scenes,omitempty"`
}

// ManifestTrack is one sound in pack.json; File is its path in the zip.
type ManifestTrack struct {
	File        string   `json:"file"`
	Kind        string   `json:"kind"`
	Category    string   `json:"category,omitempty"`
	Icon        string   `json:"icon,omitempty"`
	Title       string   `json:"title"`
	Tags        []string `json:"tags,omitempty"`
	License     string   `json:"license,omitempty"`
	Attribution string   `json:"attribution,omitempty"`
	SourceURL   string   `json:"source_url,omitempty"`
	GainDB      float64  `json:"gain_db,omitempty"`
}

// ManifestScene is a ready-made scene in pack.json.
type ManifestScene struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Mood is a built-in mood's slug (calm, tense…).
	Mood       string `json:"mood,omitempty"`
	SpotifyURI string `json:"spotify_uri,omitempty"`
	Ambience   []struct {
		File   string  `json:"file"`
		Volume float64 `json:"volume"`
	} `json:"ambience,omitempty"`
	SFX []struct {
		File  string `json:"file"`
		Label string `json:"label,omitempty"`
	} `json:"sfx,omitempty"`
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

func (m *Manifest) check() error {
	m.ID = strings.ToLower(strings.TrimSpace(m.ID))
	if !slugRe.MatchString(m.ID) {
		return invalid("pack.json needs an id of letters, digits, dots and dashes")
	}
	if strings.TrimSpace(m.Name) == "" {
		m.Name = m.ID
	}
	if len(m.Tracks) == 0 || len(m.Tracks) > maxTracks {
		return invalid("pack.json lists no sounds (or more than %d)", maxTracks)
	}
	for i := range m.Tracks {
		t := &m.Tracks[i]
		if t.Kind != model.TrackAmbience && t.Kind != model.TrackSFX {
			return invalid("%s: kind is ambience or sfx", t.File)
		}
		if _, ok := AudioExts[extOf(t.File)]; !ok {
			return invalid("%s: sounds are Ogg/Opus, MP3, M4A or WAV", t.File)
		}
		if strings.TrimSpace(t.Title) == "" {
			t.Title = titleOf(t.File)
		}
		if !slices.Contains(Categories, t.Category) {
			t.Category = categoryOf(strings.ToLower(t.File + " " + t.Title + " " + strings.Join(t.Tags, " ")))
		}
		if !slices.Contains(TrackIcons, t.Icon) {
			t.Icon = categoryIcon[t.Category]
		}
		if t.GainDB < -30 || t.GainDB > 12 {
			return invalid("%s: gain_db is between -30 and 12", t.File)
		}
		if t.License == "" {
			t.License = m.License
		}
	}
	return nil
}

func extOf(name string) string { return strings.TrimPrefix(strings.ToLower(path.Ext(name)), ".") }

// titleOf makes a title from a file name ("heavy_rain-loop.ogg": "Heavy rain loop").
func titleOf(name string) string {
	base := strings.TrimSuffix(path.Base(filepath.ToSlash(name)), path.Ext(name))
	base = strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(base)), " ")
	if base == "" {
		return name
	}
	return strings.ToUpper(base[:1]) + base[1:]
}

var categoryWords = []struct {
	category string
	words    []string
}{
	{"weather", []string{"rain", "storm", "thunder", "wind", "snow", "hail", "blizzard", "drizzle"}},
	{"water", []string{"ocean", "sea", "wave", "river", "stream", "water", "lake", "harbor", "harbour", "drip", "creek", "underwater"}},
	{"combat", []string{"sword", "blade", "punch", "kick", "gun", "shot", "explosion", "explode", "impact", "fight", "battle", "arrow", "hit", "slash", "clash"}},
	{"body", []string{"heartbeat", "heart", "breath", "footstep", "step", "walk", "run", "pulse"}},
	{"magic", []string{"magic", "spell", "portal", "sparkle", "chime", "mystic", "fairy"}},
	{"voices", []string{"voice", "scream", "laugh", "whisper", "shout", "gasp", "chatter", "murmur"}},
	{"city", []string{"city", "street", "traffic", "crowd", "market", "cafe", "café", "restaurant", "train", "subway", "car", "station", "school", "town", "office"}},
	{"objects", []string{"door", "bell", "clock", "glass", "book", "paper", "key", "chair", "cup", "metal", "wood", "lock", "drawer"}},
	{"nature", []string{"forest", "bird", "insect", "cricket", "frog", "night", "jungle", "leaves", "fire", "campfire", "meadow", "cave"}},
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

// categoryOf guesses a category from words in a name.
func categoryOf(text string) string {
	words := wordRe.FindAllString(strings.ToLower(text), -1)
	for _, c := range categoryWords {
		for _, w := range words {
			for _, k := range c.words {
				if w == k || strings.HasPrefix(w, k) && len(w) <= len(k)+2 {
					return c.category
				}
			}
		}
	}
	return "other"
}

var (
	sfxWords   = []string{"sfx", "effect", "effects", "fx", "impact", "hit", "oneshot", "shot", "foley", "punch", "whoosh", "swoosh", "click", "door", "bell", "explosion", "gunshot", "footstep", "footsteps", "slam", "knock"}
	ambWords   = []string{"amb", "ambience", "ambiance", "ambient", "atmos", "atmosphere", "background", "bg", "loop", "loops", "roomtone", "drone", "rain", "wind", "forest", "ocean", "city", "crowd", "night"}
	musicWords = []string{"music", "bgm", "song", "songs", "soundtrack", "ost", "theme"}
)

// kindOf guesses ambience or sfx from a file's path; music is skipped
// (music comes from Spotify).
func kindOf(rel string, size int64) (string, bool) {
	words := wordRe.FindAllString(strings.ToLower(rel), -1)
	has := func(list []string) bool {
		for _, w := range words {
			if slices.Contains(list, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has(musicWords):
		return "", false
	case has(sfxWords) && !has([]string{"loop", "loops", "ambience", "ambient", "amb"}):
		return model.TrackSFX, true
	case has(ambWords):
		return model.TrackAmbience, true
	case size > 2<<20:
		return model.TrackAmbience, true
	}
	return model.TrackSFX, true
}

// wavDuration reads a WAV file's length from its header (0 when unknown).
func wavDuration(f io.ReadSeeker) int {
	var hdr [12]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil || string(hdr[:4]) != "RIFF" || string(hdr[8:]) != "WAVE" {
		return 0
	}
	var byteRate uint32
	for {
		var ch [8]byte
		if _, err := io.ReadFull(f, ch[:]); err != nil {
			return 0
		}
		size := binary.LittleEndian.Uint32(ch[4:])
		switch string(ch[:4]) {
		case "fmt ":
			var fmtb [16]byte
			if size < 16 {
				return 0
			}
			if _, err := io.ReadFull(f, fmtb[:]); err != nil {
				return 0
			}
			byteRate = binary.LittleEndian.Uint32(fmtb[8:])
			if _, err := f.Seek(int64(size-16+size%2), io.SeekCurrent); err != nil {
				return 0
			}
		case "data":
			if byteRate == 0 {
				return 0
			}
			return int(uint64(size) * 1000 / uint64(byteRate))
		default:
			if _, err := f.Seek(int64(size+size%2), io.SeekCurrent); err != nil {
				return 0
			}
		}
	}
}

// Packs installs and keeps sound packs under Dir (<data>/audio/packs).
type Packs struct {
	DB       bun.IDB
	Settings *settings.Store
	Dir      string
	HTTP     *http.Client
}

// NewPacks returns the pack store for a data folder.
func NewPacks(db bun.IDB, st *settings.Store, dataDir string) *Packs {
	return &Packs{DB: db, Settings: st, Dir: filepath.Join(dataDir, "audio", "packs"), HTTP: &http.Client{Timeout: 30 * time.Minute}}
}

// Path is where a track's file is.
func (p *Packs) Path(slug string, t *model.AudioTrack) string {
	return filepath.Join(p.Dir, slug, t.SHA256+"."+t.Ext)
}

// store copies r into the pack's folder under its hash.
func (p *Packs) store(slug, ext string, r io.Reader) (sum string, size int64, err error) {
	dir := filepath.Join(p.Dir, slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, err
	}
	tmp, err := os.CreateTemp(dir, ".incoming-*")
	if err != nil {
		return "", 0, err
	}
	defer func() {
		tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	h := sha256.New()
	size, err = io.Copy(io.MultiWriter(tmp, h), io.LimitReader(r, maxTrackSize+1))
	if err != nil {
		return "", 0, err
	}
	if size > maxTrackSize {
		return "", 0, invalid("a sound is larger than %d MB", maxTrackSize>>20)
	}
	if err := tmp.Close(); err != nil {
		return "", 0, err
	}
	sum = hex.EncodeToString(h.Sum(nil))
	if err := os.Rename(tmp.Name(), filepath.Join(dir, sum+"."+ext)); err != nil {
		return "", 0, err
	}
	return sum, size, nil
}

// incoming is a sound to install: its manifest entry and how to read it.
type incoming struct {
	ManifestTrack
	open func() (io.ReadCloser, error)
	size int64
}

// install writes a pack's sounds and rows, keeping the tracks (and the
// scenes using them) that a reinstall leaves unchanged.
func (p *Packs) install(ctx context.Context, m Manifest, origin string, files []incoming) (*model.AudioPack, error) {
	type stored struct {
		incoming
		sum      string
		size     int64
		duration int
	}
	var all []stored
	var total int64
	for _, f := range files {
		rc, err := f.open()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.File, err)
		}
		sum, size, err := p.store(m.ID, extOf(f.File), rc)
		rc.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.File, err)
		}
		total += size
		if total > maxPackSize {
			return nil, invalid("the pack is larger than %d GB", maxPackSize>>30)
		}
		dur := 0
		if extOf(f.File) == "wav" {
			if fh, err := os.Open(filepath.Join(p.Dir, m.ID, sum+".wav")); err == nil {
				dur = wavDuration(fh)
				fh.Close()
			}
		}
		all = append(all, stored{incoming: f, sum: sum, size: size, duration: dur})
	}
	pack := &model.AudioPack{Slug: m.ID, Version: m.Version, Name: strings.TrimSpace(m.Name), Description: m.Description, License: m.License,
		SourceURL: m.SourceURL, Origin: origin, Size: total, InstalledAt: time.Now().UTC()}
	err := p.DB.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var old model.AudioPack
		err := tx.NewSelect().Model(&old).Where("slug = ?", m.ID).Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.NewInsert().Model(pack).Exec(ctx); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			pack.ID = old.ID
			if _, err := tx.NewUpdate().Model(pack).WherePK().Exec(ctx); err != nil {
				return err
			}
		}
		var existing []model.AudioTrack
		if err := tx.NewSelect().Model(&existing).Where("pack_id = ?", pack.ID).Scan(ctx); err != nil {
			return err
		}
		bySum := map[string]*model.AudioTrack{}
		for i := range existing {
			bySum[existing[i].SHA256] = &existing[i]
		}
		byFile := map[string]int64{}
		keep := map[int64]bool{}
		for _, s := range all {
			tags := s.Tags
			if tags == nil {
				tags = []string{}
			}
			t := model.AudioTrack{PackID: pack.ID, SHA256: s.sum, Ext: extOf(s.File), Kind: s.Kind, Category: s.Category, Icon: s.Icon,
				Title: s.Title, DurationMS: s.duration, Size: s.size, GainDB: s.GainDB, Tags: tags, License: s.License,
				Attribution: s.Attribution, SourceURL: s.SourceURL}
			if old, ok := bySum[s.sum]; ok {
				t.ID = old.ID
				if _, err := tx.NewUpdate().Model(&t).WherePK().Exec(ctx); err != nil {
					return err
				}
			} else if _, err := tx.NewInsert().Model(&t).Exec(ctx); err != nil {
				return err
			}
			bySum[s.sum] = &t
			keep[t.ID] = true
			byFile[s.File] = t.ID
		}
		for _, t := range existing {
			if !keep[t.ID] {
				if _, err := tx.NewDelete().Model((*model.AudioTrack)(nil)).Where("id = ?", t.ID).Exec(ctx); err != nil {
					return err
				}
			}
		}
		return p.installScenes(ctx, tx, pack, m.Scenes, byFile)
	})
	if err != nil {
		return nil, err
	}
	return pack, nil
}

// installScenes makes (or remakes) a pack's ready-made scenes.
func (p *Packs) installScenes(ctx context.Context, tx bun.Tx, pack *model.AudioPack, scenes []ManifestScene, byFile map[string]int64) error {
	for _, ms := range scenes {
		in := SceneInput{Name: ms.Name, Description: ms.Description, SpotifyURI: ms.SpotifyURI, Shuffle: true}
		if ms.Mood != "" {
			var m model.AudioMood
			if err := tx.NewSelect().Model(&m).Where("slug = ?", ms.Mood).Limit(1).Scan(ctx); err == nil {
				in.MoodID = &m.ID
			}
		}
		for _, a := range ms.Ambience {
			if id, ok := byFile[a.File]; ok {
				in.Ambience = append(in.Ambience, SceneLayer{TrackID: id, Volume: a.Volume})
			}
		}
		for _, s := range ms.SFX {
			if id, ok := byFile[s.File]; ok {
				in.SFX = append(in.SFX, ScenePad{TrackID: id, Label: s.Label})
			}
		}
		var old model.AudioScene
		err := tx.NewSelect().Model(&old).Where("pack_id = ? AND name = ?", pack.ID, ms.Name).Limit(1).Scan(ctx)
		packID := pack.ID
		if err == nil {
			if _, err := saveScene(ctx, tx, &old.ID, &packID, in); err != nil {
				return err
			}
		} else if _, err := saveScene(ctx, tx, nil, &packID, in); err != nil {
			return err
		}
	}
	return nil
}

// InstallZip installs (or updates) a pack from a zip holding pack.json
// and its sounds.
func (p *Packs) InstallZip(ctx context.Context, zipPath, origin string) (*model.AudioPack, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, invalid("not a zip file")
	}
	defer zr.Close()
	var manifest *zip.File
	for _, f := range zr.File {
		name := strings.TrimPrefix(filepath.ToSlash(f.Name), "./")
		if path.Base(name) == "pack.json" && strings.Count(name, "/") <= 1 && (manifest == nil || len(name) < len(manifest.Name)) {
			manifest = f
		}
	}
	if manifest == nil {
		return nil, invalid("the zip has no pack.json")
	}
	prefix := path.Dir(strings.TrimPrefix(filepath.ToSlash(manifest.Name), "./"))
	if prefix == "." {
		prefix = ""
	} else {
		prefix += "/"
	}
	rc, err := manifest.Open()
	if err != nil {
		return nil, err
	}
	var m Manifest
	err = json.NewDecoder(io.LimitReader(rc, 4<<20)).Decode(&m)
	rc.Close()
	if err != nil {
		return nil, invalid("pack.json: %v", err)
	}
	if err := m.check(); err != nil {
		return nil, err
	}
	entries := map[string]*zip.File{}
	for _, f := range zr.File {
		entries[strings.TrimPrefix(filepath.ToSlash(f.Name), "./")] = f
	}
	var files []incoming
	for _, t := range m.Tracks {
		clean := path.Clean("/" + filepath.ToSlash(t.File))[1:]
		f, ok := entries[prefix+clean]
		if !ok || f.FileInfo().IsDir() {
			return nil, invalid("pack.json lists %s, which isn't in the zip", t.File)
		}
		if f.UncompressedSize64 > maxTrackSize {
			return nil, invalid("%s is larger than %d MB", t.File, maxTrackSize>>20)
		}
		files = append(files, incoming{ManifestTrack: t, open: f.Open})
	}
	return p.install(ctx, m, origin, files)
}

// CatalogEntry is a pack a catalog offers.
type CatalogEntry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	License     string `json:"license,omitempty"`
	URL         string `json:"url" doc:"The pack's zip (relative to the catalog)"`
	Size        int64  `json:"size,omitempty"`
	Sounds      int    `json:"sounds,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
}

// Catalog lists the packs the configured catalog offers (none when no
// catalog is set).
func (p *Packs) Catalog(ctx context.Context) ([]CatalogEntry, error) {
	cfg, err := p.Settings.Sound(ctx)
	if err != nil {
		return nil, err
	}
	out := []CatalogEntry{}
	if strings.TrimSpace(cfg.CatalogURL) == "" {
		return out, nil
	}
	base, err := url.Parse(strings.TrimSpace(cfg.CatalogURL))
	if err != nil || (base.Scheme != "https" && base.Scheme != "http") {
		return nil, invalid("the catalog URL must be an http(s) link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sound catalog: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sound catalog answered %d", resp.StatusCode)
	}
	var doc struct {
		Packs []CatalogEntry `json:"packs"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&doc); err != nil {
		return nil, fmt.Errorf("sound catalog: %w", err)
	}
	for _, e := range doc.Packs {
		u, err := base.Parse(e.URL)
		if err != nil || e.ID == "" {
			continue
		}
		e.URL = u.String()
		out = append(out, e)
	}
	return out, nil
}

// InstallFromCatalog downloads a catalog's pack and installs it.
func (p *Packs) InstallFromCatalog(ctx context.Context, id string, progress func(string)) (*model.AudioPack, error) {
	entries, err := p.Catalog(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(entries, func(e CatalogEntry) bool { return e.ID == id })
	if i < 0 {
		return nil, ErrPackNotFound
	}
	e := entries[i]
	if progress != nil {
		progress("downloading " + e.Name)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %d", e.Name, resp.StatusCode)
	}
	if err := os.MkdirAll(p.Dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.CreateTemp(p.Dir, ".download-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, maxPackSize+1))
	tmp.Close()
	if err != nil {
		return nil, err
	}
	if n > maxPackSize {
		return nil, invalid("the pack is larger than %d GB", maxPackSize>>30)
	}
	if e.SHA256 != "" && !strings.EqualFold(e.SHA256, hex.EncodeToString(h.Sum(nil))) {
		return nil, errors.New("the downloaded pack doesn't match the catalog's checksum")
	}
	if progress != nil {
		progress("installing " + e.Name)
	}
	pack, err := p.InstallZip(ctx, tmp.Name(), "catalog")
	if err != nil {
		return nil, err
	}
	if pack.SourceURL == "" {
		pack.SourceURL = e.URL
		_, _ = p.DB.NewUpdate().Model(pack).Column("source_url").WherePK().Exec(ctx)
	}
	return pack, nil
}

var folderSlugRe = regexp.MustCompile(`[^a-z0-9]+`)

// ImportFolder adds the sounds in a folder on the server as a pack,
// guessing ambience or effect and a category from file and folder names.
// Music files are skipped.
func (p *Packs) ImportFolder(ctx context.Context, dir string, progress func(done, total int)) (*model.AudioPack, int, error) {
	dir = filepath.Clean(dir)
	st, err := os.Stat(dir)
	if err != nil || !st.IsDir() {
		return nil, 0, invalid("%s isn't a folder mangarr can read", dir)
	}
	type found struct {
		rel  string
		size int64
	}
	var list []found
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if _, ok := AudioExts[extOf(d.Name())]; !ok {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxTrackSize {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		list = append(list, found{filepath.ToSlash(rel), info.Size()})
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(list, func(i, j int) bool { return list[i].rel < list[j].rel })
	slug := strings.Trim(folderSlugRe.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "-"), "-")
	if slug == "" {
		slug = "sounds"
	}
	m := Manifest{ID: "folder-" + slug, Name: filepath.Base(dir), SourceURL: "", Version: time.Now().UTC().Format("2006-01-02")}
	var files []incoming
	skipped := 0
	for _, f := range list {
		kind, ok := kindOf(f.rel, f.size)
		if !ok {
			skipped++
			continue
		}
		cat := categoryOf(f.rel)
		full := filepath.Join(dir, filepath.FromSlash(f.rel))
		files = append(files, incoming{ManifestTrack: ManifestTrack{File: f.rel, Kind: kind, Category: cat, Icon: categoryIcon[cat], Title: titleOf(f.rel), Tags: []string{}},
			open: func() (io.ReadCloser, error) { return os.Open(full) }, size: f.size})
		if len(files) >= maxTracks {
			break
		}
	}
	if len(files) == 0 {
		return nil, skipped, invalid("no sounds found in %s (Ogg/Opus, MP3, M4A or WAV; music is skipped)", dir)
	}
	// report progress while copying
	total := len(files)
	for i := range files {
		open, n := files[i].open, i+1
		files[i].open = func() (io.ReadCloser, error) {
			if progress != nil {
				progress(n, total)
			}
			return open()
		}
	}
	pack, err := p.install(ctx, m, "folder", files)
	return pack, skipped, err
}

// PackView is an installed pack with how many sounds it has.
type PackView struct {
	model.AudioPack
	Ambience int `json:"ambience"`
	SFX      int `json:"sfx"`
}

// Usage is how much room the packs take.
type Usage struct {
	Size      int64 `json:"size"`
	QuotaSize int64 `json:"quotaSize" doc:"0: no limit"`
	Over      bool  `json:"over"`
}

// List returns the installed packs and the room they take.
func (p *Packs) List(ctx context.Context) ([]PackView, Usage, error) {
	var packs []model.AudioPack
	if err := p.DB.NewSelect().Model(&packs).Order("name").Scan(ctx); err != nil {
		return nil, Usage{}, err
	}
	var counts []struct {
		PackID int64  `bun:"pack_id"`
		Kind   string `bun:"kind"`
		N      int    `bun:"n"`
	}
	if err := p.DB.NewSelect().Model((*model.AudioTrack)(nil)).Column("pack_id", "kind").ColumnExpr("COUNT(*) AS n").Group("pack_id", "kind").Scan(ctx, &counts); err != nil {
		return nil, Usage{}, err
	}
	out := make([]PackView, 0, len(packs))
	var u Usage
	for _, pk := range packs {
		v := PackView{AudioPack: pk}
		for _, c := range counts {
			if c.PackID == pk.ID && c.Kind == model.TrackAmbience {
				v.Ambience = c.N
			} else if c.PackID == pk.ID {
				v.SFX = c.N
			}
		}
		u.Size += pk.Size
		out = append(out, v)
	}
	if cfg, err := p.Settings.Sound(ctx); err == nil && cfg.QuotaMB > 0 {
		u.QuotaSize = int64(cfg.QuotaMB) << 20
		u.Over = u.Size > u.QuotaSize
	}
	return out, u, nil
}

// Delete removes a pack, its sounds and its files; scenes lose those layers.
func (p *Packs) Delete(ctx context.Context, id int64) error {
	var pk model.AudioPack
	if err := p.DB.NewSelect().Model(&pk).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPackNotFound
		}
		return err
	}
	if _, err := p.DB.NewDelete().Model((*model.AudioPack)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return err
	}
	if slugRe.MatchString(pk.Slug) {
		return os.RemoveAll(filepath.Join(p.Dir, pk.Slug))
	}
	return nil
}

// Track returns a track and its pack's slug.
func (p *Packs) Track(ctx context.Context, id int64) (*model.AudioTrack, string, error) {
	var t model.AudioTrack
	if err := p.DB.NewSelect().Model(&t).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", ErrPackNotFound
		}
		return nil, "", err
	}
	var slug string
	if err := p.DB.NewSelect().Model((*model.AudioPack)(nil)).Column("slug").Where("id = ?", t.PackID).Scan(ctx, &slug); err != nil {
		return nil, "", err
	}
	return &t, slug, nil
}

// TrackFilter narrows the track list.
type TrackFilter struct {
	Kind     string
	Category string
	PackID   int64
	Query    string
}

// TrackView is a track with its pack's name.
type TrackView struct {
	model.AudioTrack
	Pack string `json:"pack"`
}

// Tracks lists sounds, ambience and effects by pack and title.
func (p *Packs) Tracks(ctx context.Context, f TrackFilter) ([]TrackView, error) {
	out := []TrackView{}
	q := p.DB.NewSelect().TableExpr("audio_tracks AS t").ColumnExpr("t.*").ColumnExpr("p.name AS pack").
		Join("JOIN audio_packs AS p ON p.id = t.pack_id").OrderExpr("p.name, t.title").Limit(2000)
	if f.Kind != "" {
		q = q.Where("t.kind = ?", f.Kind)
	}
	if f.Category != "" {
		q = q.Where("t.category = ?", f.Category)
	}
	if f.PackID > 0 {
		q = q.Where("t.pack_id = ?", f.PackID)
	}
	if s := strings.ToLower(strings.TrimSpace(f.Query)); s != "" {
		q = q.Where("LOWER(t.title) LIKE ?", "%"+s+"%")
	}
	return out, q.Scan(ctx, &out)
}

// Leftover is a file in the packs folder no installed sound uses.
type Leftover struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// Leftovers lists files under the packs folder that no track points at
// (left by an interrupted install or a pack removed by hand).
func (p *Packs) Leftovers(ctx context.Context) ([]Leftover, error) {
	type row struct {
		Slug   string `bun:"slug"`
		SHA256 string `bun:"sha256"`
		Ext    string `bun:"ext"`
	}
	var rows []row
	if err := p.DB.NewSelect().TableExpr("audio_tracks AS t").ColumnExpr("p.slug, t.sha256, t.ext").Join("JOIN audio_packs AS p ON p.id = t.pack_id").Scan(ctx, &rows); err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, r := range rows {
		used[filepath.Join(r.Slug, r.SHA256+"."+r.Ext)] = true
	}
	out := []Leftover{}
	err := filepath.WalkDir(p.Dir, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(p.Dir, full)
		if used[rel] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && time.Since(info.ModTime()) < time.Hour {
			return nil // an install in progress
		}
		out = append(out, Leftover{Path: full, Size: info.Size()})
		return nil
	})
	return out, err
}

// RemoveLeftovers deletes the files Leftovers lists, and empty folders.
func (p *Packs) RemoveLeftovers(ctx context.Context) (int, int64, error) {
	list, err := p.Leftovers(ctx)
	if err != nil {
		return 0, 0, err
	}
	n, freed := 0, int64(0)
	for _, l := range list {
		if err := os.Remove(l.Path); err == nil {
			n++
			freed += l.Size
			_ = os.Remove(filepath.Dir(l.Path)) // only goes when empty
		}
	}
	return n, freed, nil
}
