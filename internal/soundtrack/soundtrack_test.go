package soundtrack

import (
	"context"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

func seedLibrary(t *testing.T, d *db.DB) (user, series int64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	u := &model.User{Username: "reader", PasswordHash: "x", CreatedAt: now}
	if _, err := d.NewInsert().Model(u).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	rf := &model.RootFolder{Path: t.TempDir(), CreatedAt: now}
	if _, err := d.NewInsert().Model(rf).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	p := &model.Profile{Name: "Default", CreatedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(p).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	s := &model.Series{Title: "Harbor", SortTitle: "harbor", Status: model.StatusOngoing, RootFolderID: rf.ID, Path: "Harbor", ProfileID: p.ID, Tags: []int64{}, AddedAt: now, UpdatedAt: now}
	if _, err := d.NewInsert().Model(s).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	return u.ID, s.ID
}

// TestSoundtrackStorage: moods are seeded once, soundtracks keep one shared
// and one per reader, and they go with their series.
func TestSoundtrackStorage(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		moods, err := Moods(ctx, d, st)
		if err != nil || len(moods) != 8 || moods[1].Slug != "melancholy" || moods[1].Genres[0] != "Drama" {
			t.Fatalf("seeded moods %v %+v", err, moods)
		}
		for _, m := range moods {
			if err := DeleteMood(ctx, d, m.ID); err != nil {
				t.Fatal(err)
			}
		}
		if moods, _ = Moods(ctx, d, st); len(moods) != 0 {
			t.Fatalf("deleted built-ins came back: %+v", moods)
		}
		m := &model.AudioMood{Name: " Storm ", Color: "#AABBCC", Icon: "zap", Genres: []string{"Action", " Action ", ""}, SpotifyURI: "spotify:playlist:37i9dQZF1DX4sWSpwq3LiO"}
		if err := CreateMood(ctx, d, m); err != nil || m.Name != "Storm" || m.Color != "#aabbcc" || len(m.Genres) != 1 {
			t.Fatalf("create %v %+v", err, m)
		}

		user, series := seedLibrary(t, d)
		page := 3
		shared := Doc{Cues: []Cue{{ChapterNumber: 10, Page: &page, MoodID: &m.ID}, {ChapterNumber: 0, MoodID: &m.ID}, {ChapterNumber: 10}}}
		if err := Save(ctx, d, series, nil, shared); err != nil {
			t.Fatal(err)
		}
		// saving again replaces the cues (and the unique index keeps one shared soundtrack)
		if err := Save(ctx, d, series, nil, shared); err != nil {
			t.Fatal(err)
		}
		got, err := Get(ctx, d, series, nil)
		if err != nil || got == nil || len(got.Cues) != 3 || got.Cues[0].ChapterNumber != 0 || got.Cues[1].Page != nil || *got.Cues[2].Page != 3 {
			t.Fatalf("shared %v %+v", err, got)
		}
		if err := Save(ctx, d, series, &user, Doc{Muted: true}); err != nil {
			t.Fatal(err)
		}
		if mine, _ := Get(ctx, d, series, &user); mine == nil || !mine.Muted || len(mine.Cues) != 0 {
			t.Fatalf("own version %+v", mine)
		}
		var n int64
		if n, err = d.NewSelect().Model((*model.Soundtrack)(nil)).Count(ctx); err != nil || n != 2 {
			t.Fatalf("soundtracks %d %v", n, err)
		}
		r, err := Resolve(ctx, d, st, series, user, nil, 10)
		if err != nil || !r.Muted {
			t.Fatalf("resolve own %v %+v", err, r)
		}
		if err := Reset(ctx, d, series, user); err != nil {
			t.Fatal(err)
		}
		r, err = Resolve(ctx, d, st, series, user, nil, 10)
		if err != nil || r.Source != FromShared || r.Start != nil || len(r.Pages) != 1 || r.Pages[0].Sound.Mood.ID != m.ID {
			t.Fatalf("resolve shared at 10 %v %+v", err, r)
		}
		if r, _ = Resolve(ctx, d, st, series, user, nil, 11); r.Start == nil || r.Start.Mood.ID != m.ID {
			t.Fatalf("a page cue carries on into the next chapter: %+v", r)
		}

		if _, err := d.NewDelete().Model((*model.Series)(nil)).Where("id = ?", series).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, _ = d.NewSelect().Model((*model.SoundtrackCue)(nil)).Count(ctx); n != 0 {
			t.Fatalf("cues outlived their series: %d", n)
		}
	})
}

// TestTokenSealing: refresh tokens are stored encrypted and only open with
// the instance secret they were sealed with.
func TestTokenSealing(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	st := settings.NewStore(d)
	if _, err := st.EnsureSecrets(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(d, st)
	sealed, err := s.seal(ctx, "refresh-secret")
	if err != nil || sealed == "" || sealed == "refresh-secret" {
		t.Fatalf("seal %v %q", err, sealed)
	}
	if plain, err := s.open(ctx, sealed); err != nil || plain != "refresh-secret" {
		t.Fatalf("open %v %q", err, plain)
	}
	g, _ := st.General(ctx)
	g.SessionSecret = "another"
	if err := st.Set(ctx, settings.KeyGeneral, g); err != nil {
		t.Fatal(err)
	}
	if _, err := s.open(ctx, sealed); err == nil {
		t.Fatal("opened with another secret")
	}
}

func TestCover(t *testing.T) {
	for _, c := range []struct {
		images []image
		want   string
	}{
		{nil, ""},
		{[]image{{"big", 640, 640}, {"mid", 300, 300}, {"small", 64, 64}}, "mid"},
		{[]image{{"small", 64, 64}, {"tiny", 32, 32}}, "small"},
		{[]image{{"unsized", 0, 0}}, "unsized"},
	} {
		if got := cover(c.images); got != c.want {
			t.Errorf("cover(%v) = %q, want %q", c.images, got, c.want)
		}
	}
}
