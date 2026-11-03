// Package soundtrack keeps soundtracks: music that plays while reading.
// Moods are presets (a Spotify playlist, album or track with a colour and
// an icon); a series' soundtrack is a list of cues that start a mood from a
// chapter (and page). Admins set the shared soundtrack, and each reader may
// keep their own version of it.
package soundtrack

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

var (
	ErrMoodNotFound = errors.New("mood not found")
	ErrInvalid      = errors.New("invalid")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Icons are the icons a mood may have (lucide names; the web UI draws them).
var Icons = []string{
	"leaf", "cloud-rain", "zap", "flame", "ghost", "heart", "smile", "moon",
	"music", "sun", "snowflake", "swords", "waves", "wind", "coffee", "sparkles",
	"skull", "mountain", "castle", "rocket", "trees", "drama", "eye", "sunrise",
}

// builtin is a mood mangarr starts with.
type builtin struct {
	slug, name, color, icon   string
	genres, keywords, prompts []string
}

// Builtins are the moods mangarr seeds, without music: admins pick it.
var builtins = []builtin{
	{"calm", "Calm", "#3fb98a", "leaf", []string{"Slice of Life", "Iyashikei"}, []string{"morning", "home", "tea", "garden", "rest", "peace", "peaceful", "everyday", "daily", "quiet", "picnic", "holiday", "sunny", "spring", "breakfast", "cooking"}, []string{"a peaceful manga page of a quiet everyday moment", "a calm manga panel of people relaxing at home", "a gentle slice of life manga scene in the sunshine"}},
	{"melancholy", "Melancholy", "#58a6ff", "cloud-rain", []string{"Drama", "Tragedy"}, []string{"farewell", "goodbye", "funeral", "memory", "memories", "loss", "lost", "rain", "tears", "alone", "lonely", "grave", "regret", "parting", "sorrow", "grief"}, []string{"a sad manga page with someone crying", "a lonely melancholic manga panel in the rain", "a manga page of a tearful farewell"}},
	{"tense", "Tense", "#d29922", "zap", []string{"Thriller", "Suspense"}, []string{"chase", "trap", "escape", "hunt", "hunted", "threat", "ambush", "deadline", "hostage", "siege", "pursuit", "danger", "crisis", "standoff", "countdown", "betrayal"}, []string{"a tense manga page of a dangerous standoff", "a suspenseful manga panel with a worried face and sweat", "a manga page of someone being chased"}},
	{"action", "Action", "#f0883e", "flame", []string{"Action", "Shounen", "Sports"}, []string{"battle", "fight", "war", "duel", "clash", "attack", "raid", "tournament", "match", "versus", "vs", "combat", "showdown", "assault", "boss", "strike"}, []string{"a manga page of an intense fight with speed lines", "an action manga panel with an explosion", "a manga page of a sword battle"}},
	{"horror", "Horror", "#a371f7", "ghost", []string{"Horror", "Psychological"}, []string{"blood", "death", "dead", "ghost", "curse", "cursed", "monster", "nightmare", "haunted", "corpse", "demon", "zombie", "massacre", "horror", "scream", "grave"}, []string{"a scary horror manga page with a monster in the dark", "a creepy manga panel with a terrified face", "a gory horror manga scene"}},
	{"romance", "Romance", "#f778ba", "heart", []string{"Romance", "Shoujo"}, []string{"date", "confession", "kiss", "love", "wedding", "heart", "sweetheart", "crush", "proposal", "valentine", "bride", "lovers", "romance", "promise", "together", "dance"}, []string{"a romantic manga page of a blushing couple", "a manga panel of a love confession with sparkles", "a manga page of two people about to kiss"}},
	{"comedy", "Comedy", "#e3d34b", "smile", []string{"Comedy", "School Life"}, []string{"festival", "party", "prank", "beach", "vacation", "trip", "pool", "karaoke", "costume", "mischief", "shopping", "game", "club", "dorm", "sports", "cultural"}, []string{"a funny manga page with exaggerated comic expressions", "a chibi comedy manga panel", "a silly gag manga scene with people laughing"}},
	{"mystery", "Mystery", "#8b93ff", "moon", []string{"Mystery", "Supernatural"}, []string{"secret", "secrets", "mystery", "clue", "truth", "riddle", "investigation", "unknown", "shadow", "shadows", "disappearance", "missing", "detective", "hidden", "puzzle", "enigma"}, []string{"a mysterious manga page with dark shadows", "a manga panel of a detective looking for clues", "an eerie mysterious manga scene at night"}},
}

// EnsureMoods makes the built-in moods the first time moods are needed.
func EnsureMoods(ctx context.Context, db bun.IDB, st *settings.Store) error {
	var done settings.Soundtracks
	if err := st.Get(ctx, settings.KeySoundtracks, &done); err != nil {
		return err
	}
	if done.MoodsSeeded {
		return nil
	}
	n, err := db.NewSelect().Model((*model.AudioMood)(nil)).Where("slug <> ''").Count(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		now := time.Now().UTC()
		moods := make([]model.AudioMood, len(builtins))
		for i, b := range builtins {
			moods[i] = model.AudioMood{Slug: b.slug, Name: b.name, Color: b.color, Icon: b.icon, Genres: b.genres, Keywords: b.keywords, Prompts: b.prompts, Shuffle: true, Position: i, CreatedAt: now, UpdatedAt: now}
		}
		if _, err := db.NewInsert().Model(&moods).Exec(ctx); err != nil {
			return err
		}
	}
	return st.Set(ctx, settings.KeySoundtracks, settings.Soundtracks{MoodsSeeded: true})
}

// Moods lists the moods in their order.
func Moods(ctx context.Context, db bun.IDB, st *settings.Store) ([]model.AudioMood, error) {
	if err := EnsureMoods(ctx, db, st); err != nil {
		return nil, err
	}
	out := []model.AudioMood{}
	if err := db.NewSelect().Model(&out).Order("position", "id").Scan(ctx); err != nil {
		return nil, err
	}
	return out, withSounds(ctx, db, out)
}

// Mood returns one mood.
func Mood(ctx context.Context, db bun.IDB, id int64) (*model.AudioMood, error) {
	var m model.AudioMood
	if err := db.NewSelect().Model(&m).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrMoodNotFound
		}
		return nil, err
	}
	list := []model.AudioMood{m}
	if err := withSounds(ctx, db, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

var (
	colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
	// uriRe is what can play: Spotify plays a playlist or album as a
	// context, a track on its own
	uriRe = regexp.MustCompile(`^spotify:(playlist|album|track):[A-Za-z0-9]{10,40}$`)
)

// ValidURI reports whether uri is a playlist, album or track ("" is none).
func ValidURI(uri string) bool { return uri == "" || uriRe.MatchString(uri) }

// clean checks a mood and tidies its fields.
func clean(m *model.AudioMood) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" || len([]rune(m.Name)) > 60 {
		return invalid("give the mood a name of up to 60 characters")
	}
	m.Description = strings.TrimSpace(m.Description)
	if len([]rune(m.Description)) > 500 {
		return invalid("a description is up to 500 characters")
	}
	if !colorRe.MatchString(m.Color) {
		return invalid("the colour must look like #3fb98a")
	}
	m.Color = strings.ToLower(m.Color)
	if !slices.Contains(Icons, m.Icon) {
		return invalid("unknown icon %q", m.Icon)
	}
	m.SpotifyURI = strings.TrimSpace(m.SpotifyURI)
	if !ValidURI(m.SpotifyURI) {
		return invalid("the music must be a Spotify playlist, album or track")
	}
	if m.SpotifyURI == "" {
		m.SpotifyName, m.SpotifyImageURL = "", ""
	}
	if m.SpotifyImageURL != "" && !strings.HasPrefix(m.SpotifyImageURL, "https://") {
		return invalid("the cover must be an https link")
	}
	genres := []string{}
	for _, g := range m.Genres {
		if g = strings.TrimSpace(g); g != "" && !slices.Contains(genres, g) && len(genres) < 20 {
			genres = append(genres, g)
		}
	}
	m.Genres = genres
	words := []string{}
	for _, w := range m.Keywords {
		if w = strings.ToLower(strings.TrimSpace(w)); w != "" && !slices.Contains(words, w) && len(words) < 100 {
			words = append(words, w)
		}
	}
	m.Keywords = words
	prompts := []string{}
	for _, p := range m.Prompts {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(prompts, p) && len(prompts) < 10 {
			if len([]rune(p)) > 200 {
				return invalid("a prompt is up to 200 characters")
			}
			prompts = append(prompts, p)
		}
	}
	m.Prompts = prompts
	return nil
}

// CreateMood adds a mood at the end, with its sounds (if any).
func CreateMood(ctx context.Context, db bun.IDB, m *model.AudioMood, sounds *Sounds) error {
	if err := clean(m); err != nil {
		return err
	}
	if sounds != nil {
		if err := checkSounds(ctx, db, *sounds); err != nil {
			return err
		}
	}
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var last sql.NullInt64
		if err := tx.NewSelect().Model((*model.AudioMood)(nil)).ColumnExpr("MAX(position)").Scan(ctx, &last); err != nil {
			return err
		}
		now := time.Now().UTC()
		m.ID, m.Slug, m.CreatedAt, m.UpdatedAt = 0, "", now, now
		m.Position = int(last.Int64) + 1
		if _, err := tx.NewInsert().Model(m).Exec(ctx); err != nil {
			return err
		}
		if sounds != nil {
			if err := setSounds(ctx, tx, m.ID, *sounds); err != nil {
				return err
			}
		}
		list := []model.AudioMood{*m}
		if err := withSounds(ctx, tx, list); err != nil {
			return err
		}
		*m = list[0]
		return nil
	})
}

// UpdateMood changes a mood (built-in ones too: their slug stays), and its
// sounds unless sounds is nil.
func UpdateMood(ctx context.Context, db bun.IDB, id int64, m *model.AudioMood, sounds *Sounds) error {
	old, err := Mood(ctx, db, id)
	if err != nil {
		return err
	}
	// lists left out keep what the mood has (an older client doesn't send them)
	if m.Keywords == nil {
		m.Keywords = old.Keywords
	}
	if m.Prompts == nil {
		m.Prompts = old.Prompts
	}
	if err := clean(m); err != nil {
		return err
	}
	if sounds != nil {
		if err := checkSounds(ctx, db, *sounds); err != nil {
			return err
		}
	}
	m.ID, m.Slug, m.Position, m.PackID, m.CreatedAt, m.UpdatedAt = old.ID, old.Slug, old.Position, old.PackID, old.CreatedAt, time.Now().UTC()
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		if _, err := tx.NewUpdate().Model(m).WherePK().Exec(ctx); err != nil {
			return err
		}
		if sounds != nil {
			if err := setSounds(ctx, tx, m.ID, *sounds); err != nil {
				return err
			}
		}
		list := []model.AudioMood{*m}
		if err := withSounds(ctx, tx, list); err != nil {
			return err
		}
		*m = list[0]
		return nil
	})
}

// DeleteMood removes a mood; cues that used it fall back to the title's
// default.
func DeleteMood(ctx context.Context, db bun.IDB, id int64) error {
	res, err := db.NewDelete().Model((*model.AudioMood)(nil)).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMoodNotFound
	}
	return nil
}

// Cue starts a mood from a chapter, and a page in it. Chapter
// 0 without a page is the title's mood.
type Cue struct {
	ChapterNumber float64 `json:"chapterNumber" minimum:"0"`
	Page          *int    `json:"page,omitempty" minimum:"1"`
	// PageCount is the chapter's page count when the cue was placed (filled
	// in when left out).
	PageCount *int   `json:"pageCount,omitempty"`
	MoodID    *int64 `json:"moodId,omitempty" doc:"None: nothing plays from here"`
	// Source is manual (placed by someone) or auto (found by mangarr);
	// editing an auto cue makes it manual.
	Source       string   `json:"source,omitempty" enum:"manual,auto,"`
	Confidence   *float64 `json:"confidence,omitempty" minimum:"0" maximum:"1"`
	ModelVersion string   `json:"modelVersion,omitempty"`
	// NeedsReview: the chapter's page count changed since the page cue was
	// placed.
	NeedsReview bool `json:"needsReview,omitempty" readOnly:"true"`
}

func (c Cue) auto() bool { return c.Source == model.CueAuto }

// Doc is a soundtrack as people edit it.
type Doc struct {
	Cues []Cue `json:"cues" maxItems:"500"`
	// Muted plays nothing for the title (a reader's own version only).
	Muted     bool       `json:"muted" required:"false"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty" readOnly:"true"`
}

func (c Cue) title() bool { return c.ChapterNumber == 0 && c.Page == nil }

func (c Cue) key() [2]float64 {
	page := 0.0
	if c.Page != nil {
		page = float64(*c.Page)
	}
	return [2]float64{c.ChapterNumber, page}
}

// DefaultMood is the title-wide cue's mood (chapter 0, no page).
func (d *Doc) DefaultMood() *int64 {
	for _, c := range d.Cues {
		if c.title() {
			return c.MoodID
		}
	}
	return nil
}

func owner(q *bun.SelectQuery, seriesID int64, userID *int64) *bun.SelectQuery {
	q = q.Where("series_id = ?", seriesID)
	if userID == nil {
		return q.Where("user_id IS NULL")
	}
	return q.Where("user_id = ?", *userID)
}

// PageCounts maps a series' chapter numbers to their files' page counts.
func PageCounts(ctx context.Context, db bun.IDB, seriesID int64) (map[float64]int, error) {
	var rows []struct {
		Number float64 `bun:"number_sort"`
		Pages  int     `bun:"page_count"`
	}
	err := db.NewSelect().TableExpr("chapters AS c").ColumnExpr("c.number_sort, f.page_count").
		Join("JOIN chapter_files AS f ON f.id = c.file_id").Where("c.series_id = ?", seriesID).Scan(ctx, &rows)
	out := map[float64]int{}
	for _, r := range rows {
		if r.Pages > 0 {
			out[r.Number] = r.Pages
		}
	}
	return out, err
}

// Flag marks page cues whose chapter's page count changed.
func (d *Doc) Flag(counts map[float64]int) {
	for i := range d.Cues {
		c := &d.Cues[i]
		now, ok := counts[c.ChapterNumber]
		c.NeedsReview = c.Page != nil && c.PageCount != nil && ok && now != *c.PageCount
	}
}

// Get returns a series' shared soundtrack (userID nil) or a reader's own
// version; nil when there is none.
func Get(ctx context.Context, db bun.IDB, seriesID int64, userID *int64) (*Doc, error) {
	var st model.Soundtrack
	if err := owner(db.NewSelect().Model(&st), seriesID, userID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var cues []model.SoundtrackCue
	if err := db.NewSelect().Model(&cues).Where("soundtrack_id = ?", st.ID).Order("id").Scan(ctx); err != nil {
		return nil, err
	}
	at := st.UpdatedAt
	d := &Doc{Cues: make([]Cue, 0, len(cues)), Muted: st.Muted, UpdatedAt: &at}
	for _, c := range cues {
		d.Cues = append(d.Cues, Cue{ChapterNumber: c.ChapterNumber, Page: c.Page, PageCount: c.PageCount, MoodID: c.MoodID,
			Source: c.Source, Confidence: c.Confidence, ModelVersion: c.ModelVersion})
	}
	sortCues(d.Cues)
	return d, nil
}

// sortCues orders cues by where they start: chapter, then page (the
// chapter's own cue first).
func sortCues(cues []Cue) {
	sort.SliceStable(cues, func(i, j int) bool {
		a, b := cues[i].key(), cues[j].key()
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		return a[1] < b[1]
	})
}

// Save replaces a series' shared soundtrack (userID nil) or a reader's own
// version.
func Save(ctx context.Context, db bun.IDB, seriesID int64, userID *int64, d Doc) error {
	seen := map[[2]float64]bool{}
	cues := make([]Cue, 0, len(d.Cues))
	var counts map[float64]int
	for _, c := range d.Cues {
		if c.ChapterNumber < 0 || (c.Page != nil && *c.Page < 1) {
			return invalid("a cue needs a chapter number of 0 or more and a page of 1 or more")
		}
		if seen[c.key()] {
			return invalid("two cues start at chapter %g", c.ChapterNumber)
		}
		seen[c.key()] = true
		if c.MoodID != nil {
			if _, err := Mood(ctx, db, *c.MoodID); err != nil {
				return err
			}
		}
		if c.Page == nil {
			c.PageCount = nil
		} else if c.PageCount == nil {
			if counts == nil {
				var err error
				if counts, err = PageCounts(ctx, db, seriesID); err != nil {
					return err
				}
			}
			if n, ok := counts[c.ChapterNumber]; ok {
				c.PageCount = &n
			}
		}
		cues = append(cues, c)
	}
	if userID == nil {
		d.Muted = false
	}
	return db.RunInTx(ctx, nil, func(ctx context.Context, tx bun.Tx) error {
		var st model.Soundtrack
		err := owner(tx.NewSelect().Model(&st), seriesID, userID).Scan(ctx)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			st = model.Soundtrack{SeriesID: seriesID, UserID: userID}
		case err != nil:
			return err
		}
		st.Muted, st.UpdatedAt = d.Muted, time.Now().UTC()
		if st.ID == 0 {
			if _, err := tx.NewInsert().Model(&st).Exec(ctx); err != nil {
				return err
			}
		} else if _, err := tx.NewUpdate().Model(&st).Column("muted", "updated_at").WherePK().Exec(ctx); err != nil {
			return err
		}
		if _, err := tx.NewDelete().Model((*model.SoundtrackCue)(nil)).Where("soundtrack_id = ?", st.ID).Exec(ctx); err != nil {
			return err
		}
		if len(cues) == 0 {
			return nil
		}
		rows := make([]model.SoundtrackCue, len(cues))
		for i, c := range cues {
			src := model.CueManual
			if c.auto() {
				src = model.CueAuto
			}
			rows[i] = model.SoundtrackCue{SoundtrackID: st.ID, ChapterNumber: c.ChapterNumber, Page: c.Page, PageCount: c.PageCount, MoodID: c.MoodID,
				Source: src, Confidence: c.Confidence, ModelVersion: c.ModelVersion}
		}
		_, err = tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

// SetCue adds or replaces one cue in a soundtrack (a cue with neither a
// mood removes the one there).
func SetCue(ctx context.Context, db bun.IDB, seriesID int64, userID *int64, c Cue) (*Doc, error) {
	d, err := Get(ctx, db, seriesID, userID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		d = &Doc{}
	}
	// a cue someone sets is theirs, even where mangarr had found one
	c.Source, c.Confidence, c.ModelVersion = model.CueManual, nil, ""
	cues := slices.DeleteFunc(slices.Clone(d.Cues), func(x Cue) bool { return x.key() == c.key() })
	if c.MoodID != nil {
		cues = append(cues, c)
	}
	d.Cues = cues
	if err := Save(ctx, db, seriesID, userID, *d); err != nil {
		return nil, err
	}
	return Get(ctx, db, seriesID, userID)
}

// ReplaceAutoCues swaps a detector's automatic cues (by its version) in a
// series' shared soundtrack for new ones. Places someone set a cue at stay
// theirs; another detector's cue at the same place gives way.
func ReplaceAutoCues(ctx context.Context, db bun.IDB, seriesID int64, version string, cues []Cue) error {
	d, err := Get(ctx, db, seriesID, nil)
	if err != nil {
		return err
	}
	if d == nil {
		if len(cues) == 0 {
			return nil
		}
		d = &Doc{}
	}
	taken := map[[2]float64]bool{}
	for _, c := range d.Cues {
		if !c.auto() {
			taken[c.key()] = true
		}
	}
	mine := map[[2]float64]bool{}
	for _, c := range cues {
		if !taken[c.key()] {
			mine[c.key()] = true
		}
	}
	kept := []Cue{}
	for _, c := range d.Cues {
		if c.auto() && (c.ModelVersion == version || mine[c.key()]) {
			continue
		}
		kept = append(kept, c)
	}
	for _, c := range cues {
		if mine[c.key()] {
			c.Source, c.ModelVersion = model.CueAuto, version
			kept = append(kept, c)
		}
	}
	d.Cues = kept
	return Save(ctx, db, seriesID, nil, *d)
}

// Reset drops a reader's own version, back to the shared soundtrack.
func Reset(ctx context.Context, db bun.IDB, seriesID, userID int64) error {
	_, err := db.NewDelete().Model((*model.Soundtrack)(nil)).Where("series_id = ? AND user_id = ?", seriesID, userID).Exec(ctx)
	return err
}

// Sources of what plays.
const (
	FromNone      = "none"
	FromShared    = "shared"
	FromPersonal  = "personal"
	FromSuggested = "suggested"
	FromAuto      = "auto"
)

// Music is what Spotify plays.
type Music struct {
	URI      string `json:"uri"`
	Name     string `json:"name,omitempty"`
	ImageURL string `json:"imageUrl,omitempty"`
	Shuffle  bool   `json:"shuffle"`
}

// Sound is what plays from a cue: a mood (with its ambience and effects)
// and its Spotify music.
type Sound struct {
	Source string `json:"source" enum:"shared,personal,suggested,auto"`
	// Confidence is how sure mangarr is of a mood it found (auto only).
	Confidence *float64         `json:"confidence,omitempty"`
	Mood       *model.AudioMood `json:"mood,omitempty"`
	Music      *Music           `json:"music,omitempty"`
}

// PageCue is a sound that starts on a page of the chapter.
type PageCue struct {
	Page  int    `json:"page"`
	Sound *Sound `json:"sound,omitempty" doc:"None: no music from this page"`
}

// Resolved is what plays in one chapter for one reader.
type Resolved struct {
	Source string `json:"source" enum:"none,shared,personal,suggested,auto"`
	Muted  bool   `json:"muted"`
	// Start plays from the chapter's start (none: nothing).
	Start *Sound `json:"start,omitempty"`
	// Pages are sounds that start later in the chapter.
	Pages []PageCue `json:"pages"`
}

// resolver loads moods once per resolve.
type resolver struct {
	ctx   context.Context
	db    bun.IDB
	moods map[int64]*model.AudioMood
}

func (r *resolver) mood(id *int64) (*model.AudioMood, error) {
	if id == nil {
		return nil, nil
	}
	if m, ok := r.moods[*id]; ok {
		return m, nil
	}
	m, err := Mood(r.ctx, r.db, *id)
	if errors.Is(err, ErrMoodNotFound) {
		m, err = nil, nil
	}
	r.moods[*id] = m
	return m, err
}

func (r *resolver) sound(c Cue, source string) (*Sound, error) {
	m, err := r.mood(c.MoodID)
	if err != nil || m == nil {
		return nil, err
	}
	s := &Sound{Source: source, Mood: m}
	if m.SpotifyURI != "" {
		s.Music = &Music{URI: m.SpotifyURI, Name: m.SpotifyName, ImageURL: m.SpotifyImageURL, Shuffle: m.Shuffle}
	}
	return s, nil
}

// mergedCue is a cue and whose it is.
type mergedCue struct {
	Cue
	source string
}

// Merge lays a reader's cues over the shared ones: at the same place the
// reader's wins.
func merge(shared, personal *Doc) []mergedCue {
	at := map[[2]float64]mergedCue{}
	if shared != nil {
		for _, c := range shared.Cues {
			at[c.key()] = mergedCue{c, FromShared}
		}
	}
	if personal != nil {
		for _, c := range personal.Cues {
			at[c.key()] = mergedCue{c, FromPersonal}
		}
	}
	out := make([]mergedCue, 0, len(at))
	for _, c := range at {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].key(), out[j].key()
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		return a[1] < b[1]
	})
	return out
}

// SuggestMood picks the mood whose genres best match a title's genres and
// tags (nil when none match).
func SuggestMood(moods []model.AudioMood, genres []string) *model.AudioMood {
	have := map[string]bool{}
	for _, g := range genres {
		have[strings.ToLower(strings.TrimSpace(g))] = true
	}
	var best *model.AudioMood
	bestN := 0
	for i := range moods {
		n := 0
		for _, g := range moods[i].Genres {
			if have[strings.ToLower(g)] {
				n++
			}
		}
		if n > bestN {
			best, bestN = &moods[i], n
		}
	}
	return best
}

// Resolve picks what plays in a chapter for a reader, in this order: the
// reader's own cues, the shared (admin) cues, cues mangarr found on pages,
// then in chapters, then the title's mood, then a mood its genres suggest.
// A cue someone placed carries on until the next one someone placed; found
// cues only fill in before any. At the same place a reader's cue wins.
func Resolve(ctx context.Context, db bun.IDB, st *settings.Store, seriesID, userID int64, genres []string, chapter float64) (*Resolved, error) {
	out := &Resolved{Source: FromNone, Pages: []PageCue{}}
	personal, err := Get(ctx, db, seriesID, &userID)
	if err != nil {
		return nil, err
	}
	shared, err := Get(ctx, db, seriesID, nil)
	if err != nil {
		return nil, err
	}
	if personal != nil {
		out.Muted = personal.Muted
	}
	r := &resolver{ctx: ctx, db: db, moods: map[int64]*model.AudioMood{}}
	var humanStart, autoStart, title *mergedCue
	var humanPages, autoPages []mergedCue
	cues := merge(shared, personal)
	for i, c := range cues {
		// a page cue in an earlier chapter carries on into this one
		at := c.ChapterNumber < chapter || c.ChapterNumber == chapter && c.Page == nil
		inChapter := c.ChapterNumber == chapter && c.Page != nil
		switch {
		case c.title():
			title = &cues[i]
		case c.auto() && at:
			autoStart = &cues[i]
		case c.auto() && inChapter:
			autoPages = append(autoPages, c)
		case at:
			humanStart = &cues[i]
		case inChapter:
			humanPages = append(humanPages, c)
		}
	}
	sound := func(c *mergedCue) (*Sound, error) {
		src := c.source
		if c.auto() {
			src = FromAuto
		}
		s, err := r.sound(c.Cue, src)
		if s != nil && c.auto() {
			s.Confidence = c.Confidence
		}
		return s, err
	}
	pages := humanPages
	if humanStart == nil {
		human := map[int]bool{}
		for _, c := range humanPages {
			human[*c.Page] = true
		}
		for _, c := range autoPages {
			if !human[*c.Page] {
				pages = append(pages, c)
			}
		}
	}
	for i := range pages {
		s, err := sound(&pages[i])
		if err != nil {
			return nil, err
		}
		out.Pages = append(out.Pages, PageCue{Page: *pages[i].Page, Sound: s})
	}
	sort.Slice(out.Pages, func(i, j int) bool { return out.Pages[i].Page < out.Pages[j].Page })
	for _, c := range []*mergedCue{humanStart, autoStart, title} {
		if c == nil {
			continue
		}
		if out.Start, err = sound(c); err != nil {
			return nil, err
		}
		out.Source = out.Start.sourceOr(c.source)
		return out, nil
	}
	if st != nil {
		if cfg, err := st.Sound(ctx); err == nil && cfg.SuggestFromGenres {
			moods, err := Moods(ctx, db, st)
			if err != nil {
				return nil, err
			}
			if m := SuggestMood(moods, genres); m != nil {
				id := m.ID
				if out.Start, err = r.sound(Cue{MoodID: &id}, FromSuggested); err != nil {
					return nil, err
				}
				out.Source = FromSuggested
			}
		}
	}
	if out.Source == FromNone && len(out.Pages) > 0 {
		out.Source = out.Pages[0].Sound.sourceOr(FromShared)
	}
	return out, nil
}

func (s *Sound) sourceOr(def string) string {
	if s == nil {
		return def
	}
	return s.Source
}

// Credit is a pack sound a soundtrack uses, with its licence.
type Credit struct {
	Title       string `json:"title"`
	Pack        string `json:"pack"`
	License     string `json:"license"`
	Attribution string `json:"attribution,omitempty"`
	SourceURL   string `json:"sourceUrl,omitempty"`
}

// Credits lists the pack sounds the moods in these soundtracks use.
func Credits(ctx context.Context, db bun.IDB, docs ...*Doc) ([]Credit, error) {
	var ids []int64
	for _, d := range docs {
		if d == nil {
			continue
		}
		for _, c := range d.Cues {
			if c.MoodID != nil && !slices.Contains(ids, *c.MoodID) {
				ids = append(ids, *c.MoodID)
			}
		}
	}
	out := []Credit{}
	if len(ids) == 0 {
		return out, nil
	}
	var moods []model.AudioMood
	if err := db.NewSelect().Model(&moods).Where("id IN (?)", bun.In(ids)).Scan(ctx); err != nil {
		return nil, err
	}
	if err := withSounds(ctx, db, moods); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, m := range moods {
		for _, t := range slices.Concat(m.Ambience, m.SFX) {
			if seen[t.SHA256] {
				continue
			}
			seen[t.SHA256] = true
			out = append(out, Credit{Title: t.Title, Pack: t.Pack, License: t.License, Attribution: t.Attribution, SourceURL: t.SourceURL})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}
