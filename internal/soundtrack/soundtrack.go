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
	slug, name, color, icon string
	genres                  []string
}

// Builtins are the moods mangarr seeds, without music: admins pick it.
var builtins = []builtin{
	{"calm", "Calm", "#3fb98a", "leaf", []string{"Slice of Life", "Iyashikei"}},
	{"melancholy", "Melancholy", "#58a6ff", "cloud-rain", []string{"Drama", "Tragedy"}},
	{"tense", "Tense", "#d29922", "zap", []string{"Thriller", "Suspense"}},
	{"action", "Action", "#f0883e", "flame", []string{"Action", "Shounen", "Sports"}},
	{"horror", "Horror", "#a371f7", "ghost", []string{"Horror", "Psychological"}},
	{"romance", "Romance", "#f778ba", "heart", []string{"Romance", "Shoujo"}},
	{"comedy", "Comedy", "#e3d34b", "smile", []string{"Comedy", "School Life"}},
	{"mystery", "Mystery", "#8b93ff", "moon", []string{"Mystery", "Supernatural"}},
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
	n, err := db.NewSelect().Model((*model.AudioMood)(nil)).Count(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		now := time.Now().UTC()
		moods := make([]model.AudioMood, len(builtins))
		for i, b := range builtins {
			moods[i] = model.AudioMood{Slug: b.slug, Name: b.name, Color: b.color, Icon: b.icon, Genres: b.genres, Shuffle: true, Position: i, CreatedAt: now, UpdatedAt: now}
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
	err := db.NewSelect().Model(&out).Order("position", "id").Scan(ctx)
	return out, err
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
	return &m, nil
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
	return nil
}

// CreateMood adds a mood at the end.
func CreateMood(ctx context.Context, db bun.IDB, m *model.AudioMood) error {
	if err := clean(m); err != nil {
		return err
	}
	var last sql.NullInt64
	if err := db.NewSelect().Model((*model.AudioMood)(nil)).ColumnExpr("MAX(position)").Scan(ctx, &last); err != nil {
		return err
	}
	now := time.Now().UTC()
	m.ID, m.Slug, m.CreatedAt, m.UpdatedAt = 0, "", now, now
	m.Position = int(last.Int64) + 1
	_, err := db.NewInsert().Model(m).Exec(ctx)
	return err
}

// UpdateMood changes a mood (built-in ones too: their slug stays).
func UpdateMood(ctx context.Context, db bun.IDB, id int64, m *model.AudioMood) error {
	old, err := Mood(ctx, db, id)
	if err != nil {
		return err
	}
	if err := clean(m); err != nil {
		return err
	}
	m.ID, m.Slug, m.Position, m.CreatedAt, m.UpdatedAt = old.ID, old.Slug, old.Position, old.CreatedAt, time.Now().UTC()
	_, err = db.NewUpdate().Model(m).WherePK().Exec(ctx)
	return err
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

// Cue starts a mood from a chapter, and a page in it. Chapter 0 without a
// page is the title's default.
type Cue struct {
	ChapterNumber float64 `json:"chapterNumber" minimum:"0"`
	Page          *int    `json:"page,omitempty" minimum:"1"`
	MoodID        *int64  `json:"moodId,omitempty" doc:"None: no music from here"`
}

// Doc is a soundtrack as people edit it.
type Doc struct {
	Cues []Cue `json:"cues" maxItems:"500"`
	// Muted plays nothing for the title (a reader's own version only).
	Muted     bool       `json:"muted" required:"false"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty" readOnly:"true"`
}

// DefaultMood is the title-wide cue's mood (chapter 0, no page).
func (d *Doc) DefaultMood() *int64 {
	for _, c := range d.Cues {
		if c.ChapterNumber == 0 && c.Page == nil {
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
		d.Cues = append(d.Cues, Cue{ChapterNumber: c.ChapterNumber, Page: c.Page, MoodID: c.MoodID})
	}
	sortCues(d.Cues)
	return d, nil
}

// sortCues orders cues by where they start: chapter, then page (the
// chapter's own cue first).
func sortCues(cues []Cue) {
	page := func(c Cue) int {
		if c.Page == nil {
			return 0
		}
		return *c.Page
	}
	sort.SliceStable(cues, func(i, j int) bool {
		if cues[i].ChapterNumber != cues[j].ChapterNumber {
			return cues[i].ChapterNumber < cues[j].ChapterNumber
		}
		return page(cues[i]) < page(cues[j])
	})
}

// Save replaces a series' shared soundtrack (userID nil) or a reader's own
// version.
func Save(ctx context.Context, db bun.IDB, seriesID int64, userID *int64, d Doc) error {
	seen := map[[2]float64]bool{}
	cues := make([]Cue, 0, len(d.Cues))
	for _, c := range d.Cues {
		if c.ChapterNumber < 0 || (c.Page != nil && *c.Page < 1) {
			return invalid("a cue needs a chapter number of 0 or more and a page of 1 or more")
		}
		page := 0.0
		if c.Page != nil {
			page = float64(*c.Page)
		}
		key := [2]float64{c.ChapterNumber, page}
		if seen[key] {
			return invalid("two cues start at chapter %g", c.ChapterNumber)
		}
		seen[key] = true
		if c.MoodID != nil {
			if _, err := Mood(ctx, db, *c.MoodID); err != nil {
				return err
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
			rows[i] = model.SoundtrackCue{SoundtrackID: st.ID, ChapterNumber: c.ChapterNumber, Page: c.Page, MoodID: c.MoodID}
		}
		_, err = tx.NewInsert().Model(&rows).Exec(ctx)
		return err
	})
}

// Reset drops a reader's own version, back to the shared soundtrack.
func Reset(ctx context.Context, db bun.IDB, seriesID, userID int64) error {
	_, err := db.NewDelete().Model((*model.Soundtrack)(nil)).Where("series_id = ? AND user_id = ?", seriesID, userID).Exec(ctx)
	return err
}

// Sources of a resolved soundtrack.
const (
	FromNone     = "none"
	FromShared   = "shared"
	FromPersonal = "personal"
)

// PageCue is a mood that starts on a page of the chapter.
type PageCue struct {
	Page int              `json:"page"`
	Mood *model.AudioMood `json:"mood,omitempty"`
}

// Resolved is what plays in one chapter for one reader.
type Resolved struct {
	Source string `json:"source" enum:"none,shared,personal"`
	Muted  bool   `json:"muted"`
	// Mood plays from the chapter's start (none: no music).
	Mood *model.AudioMood `json:"mood,omitempty"`
	// Pages are moods that start later in the chapter.
	Pages []PageCue `json:"pages"`
}

// Resolve picks what plays in a chapter: the reader's own version, else the
// shared soundtrack; the last cue at or before the chapter plays from its
// start.
func Resolve(ctx context.Context, db bun.IDB, seriesID, userID int64, chapter float64) (*Resolved, error) {
	out := &Resolved{Source: FromNone, Pages: []PageCue{}}
	d, err := Get(ctx, db, seriesID, &userID)
	if err != nil {
		return nil, err
	}
	out.Source = FromPersonal
	if d == nil {
		if d, err = Get(ctx, db, seriesID, nil); err != nil {
			return nil, err
		}
		out.Source = FromShared
	}
	if d == nil {
		out.Source = FromNone
		return out, nil
	}
	out.Muted = d.Muted
	moods := map[int64]*model.AudioMood{}
	mood := func(id *int64) (*model.AudioMood, error) {
		if id == nil {
			return nil, nil
		}
		if m, ok := moods[*id]; ok {
			return m, nil
		}
		m, err := Mood(ctx, db, *id)
		if errors.Is(err, ErrMoodNotFound) {
			m, err = nil, nil
		}
		moods[*id] = m
		return m, err
	}
	cues := slices.Clone(d.Cues)
	sortCues(cues)
	var start *Cue
	for i, c := range cues {
		switch {
		case c.ChapterNumber < chapter:
			// a page cue in an earlier chapter carries on to this one
			start = &cues[i]
		case c.ChapterNumber == chapter && c.Page == nil:
			start = &cues[i]
		case c.ChapterNumber == chapter:
			m, err := mood(c.MoodID)
			if err != nil {
				return nil, err
			}
			out.Pages = append(out.Pages, PageCue{Page: *c.Page, Mood: m})
		}
	}
	sort.Slice(out.Pages, func(i, j int) bool { return out.Pages[i].Page < out.Pages[j].Page })
	if start != nil {
		if out.Mood, err = mood(start.MoodID); err != nil {
			return nil, err
		}
	}
	return out, nil
}
