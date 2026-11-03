package soundtrack

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/testutil/tonepack"
)

func trackByTitle(t *testing.T, d *db.DB, title string) model.AudioTrack {
	t.Helper()
	var tr model.AudioTrack
	if err := d.NewSelect().Model(&tr).Where("title = ?", title).Scan(context.Background()); err != nil {
		t.Fatalf("track %s: %v", title, err)
	}
	return tr
}

// TestPackInstall: a pack zip installs content-addressed files, guesses
// what pack.json leaves out, makes its scenes as moods (and gives a
// built-in mood without sounds theirs), and a reinstall keeps the tracks
// moods use; bad packs are refused; leftovers are found.
func TestPackInstall(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		if err := EnsureMoods(ctx, d, st); err != nil {
			t.Fatal(err)
		}
		p := NewPacks(d, st, t.TempDir())
		pack, err := p.InstallZip(ctx, tonepack.Zip(t, "test-tones", "1.0.0"), "upload")
		if err != nil {
			t.Fatal(err)
		}
		if pack.Slug != "test-tones" || pack.Version != "1.0.0" || pack.Size == 0 {
			t.Fatalf("pack %+v", pack)
		}
		hum := trackByTitle(t, d, "Low hum")
		sum := sha256.Sum256(tonepack.Files()["loops/low-hum.wav"])
		if hum.SHA256 != hex.EncodeToString(sum[:]) || hum.Kind != model.TrackAmbience || hum.DurationMS != 1000 || hum.GainDB != -3 || hum.Attribution != "Generated tone" {
			t.Fatalf("low hum %+v", hum)
		}
		if _, err := os.Stat(p.Path("test-tones", &hum)); err != nil {
			t.Fatalf("file: %v", err)
		}
		if hi := trackByTitle(t, d, "High hum"); hi.Category != "weather" || hi.Icon != "cloud-rain" || hi.License != "CC0-1.0" {
			t.Fatalf("guessed high hum %+v", hi)
		}
		if boop := trackByTitle(t, d, "Boop"); boop.Kind != model.TrackSFX || boop.Icon != "swords" {
			t.Fatalf("boop %+v", boop)
		}
		room := moodByName(t, d, st, "Humming room")
		if room.PackID == nil || room.Color != "#3fb98a" || len(room.Ambience) != 1 || room.Ambience[0].Volume != 0.6 || len(room.SFX) != 1 || room.SFX[0].Label != "Beep" {
			t.Fatalf("pack mood %+v", room)
		}
		if calm := moodByName(t, d, st, "Calm"); len(calm.Ambience) != 1 || len(calm.SFX) != 1 {
			t.Fatalf("the built-in mood didn't get the pack's sounds: %+v", calm)
		}

		// an update keeps unchanged tracks (and the moods using them)
		if pack, err = p.InstallZip(ctx, tonepack.Zip(t, "test-tones", "1.1.0"), "upload"); err != nil || pack.Version != "1.1.0" {
			t.Fatalf("reinstall %v %+v", err, pack)
		}
		if again := trackByTitle(t, d, "Low hum"); again.ID != hum.ID {
			t.Fatalf("reinstall changed the track id %d → %d", hum.ID, again.ID)
		}
		if room := moodByName(t, d, st, "Humming room"); len(room.Ambience) != 1 {
			t.Fatalf("mood after reinstall %+v", room)
		}
		views, usage, err := p.List(ctx)
		if err != nil || len(views) != 1 || views[0].Ambience != 2 || views[0].SFX != 2 || usage.Size != pack.Size || usage.Over {
			t.Fatalf("list %v %+v %+v", err, views, usage)
		}

		// files nothing uses are leftovers
		junk := filepath.Join(p.Dir, "test-tones", "deadbeef.ogg")
		if err := os.WriteFile(junk, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		left, err := p.Leftovers(ctx)
		if err != nil || len(left) != 1 || left[0].Path != junk {
			t.Fatalf("leftovers %v %+v", err, left)
		}
		if n, _, err := p.RemoveLeftovers(ctx); err != nil || n != 1 {
			t.Fatalf("remove leftovers %d %v", n, err)
		}
		if _, err := os.Stat(p.Path("test-tones", &hum)); err != nil {
			t.Fatalf("a used file went: %v", err)
		}

		if err := p.Delete(ctx, pack.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(p.Dir, "test-tones")); !os.IsNotExist(err) {
			t.Fatalf("pack folder left: %v", err)
		}
		if room := moodByName(t, d, st, "Humming room"); len(room.Ambience) != 0 || room.PackID != nil {
			t.Fatalf("a mood keeps a deleted pack's sounds: %+v", room)
		}
	})
}

func TestPackRefusals(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	p := NewPacks(d, settings.NewStore(d), t.TempDir())
	notZip := filepath.Join(t.TempDir(), "x.zip")
	_ = os.WriteFile(notZip, []byte("nope"), 0o644)
	if _, err := p.InstallZip(ctx, notZip, "upload"); err == nil {
		t.Fatal("installed a file that isn't a zip")
	}
	m := Manifest{ID: "Bad Id!", Tracks: []ManifestTrack{{File: "a.wav", Kind: "ambience"}}}
	if err := m.check(); err == nil {
		t.Fatal("accepted a bad id")
	}
	m = Manifest{ID: "ok", Tracks: []ManifestTrack{{File: "a.flac", Kind: "ambience"}}}
	if err := m.check(); err == nil {
		t.Fatal("accepted FLAC")
	}
	m = Manifest{ID: "ok", Tracks: []ManifestTrack{{File: "a.ogg", Kind: "music"}}}
	if err := m.check(); err == nil {
		t.Fatal("accepted a music track")
	}
}

// TestImportFolder: loose files become a pack, sorted into ambience and
// effects with categories from their names; music is skipped.
func TestImportFolder(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	p := NewPacks(d, settings.NewStore(d), t.TempDir())
	calls := 0
	pack, skipped, err := p.ImportFolder(ctx, tonepack.Folder(t), func(int, int) { calls++ })
	if err != nil {
		t.Fatal(err)
	}
	if pack.Slug != "folder-my-sounds" || pack.Origin != "folder" || skipped != 1 || calls != 4 {
		t.Fatalf("pack %+v skipped %d calls %d", pack, skipped, calls)
	}
	for title, want := range map[string][2]string{
		"Heavy Rain Loop": {model.TrackAmbience, "weather"},
		"City Street":     {model.TrackAmbience, "city"},
		"Door Slam":       {model.TrackSFX, "objects"},
		"Sword clash 01":  {model.TrackSFX, "combat"},
	} {
		if tr := trackByTitle(t, d, title); tr.Kind != want[0] || tr.Category != want[1] {
			t.Errorf("%s: %s/%s, want %v", title, tr.Kind, tr.Category, want)
		}
	}
	if _, _, err := p.ImportFolder(ctx, filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("imported a missing folder")
	}
}

// TestCatalog: the catalog lists packs with links relative to it, and an
// install checks the zip's hash.
func TestCatalog(t *testing.T) {
	d := dbtest.SQLite(t)
	ctx := context.Background()
	st := settings.NewStore(d)
	zipPath := tonepack.Zip(t, "test-tones", "2.0.0")
	data, _ := os.ReadFile(zipPath)
	sum := sha256.Sum256(data)
	good := hex.EncodeToString(sum[:])
	mux := http.NewServeMux()
	mux.HandleFunc("/sounds/catalog.json", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"packs":[{"id":"test-tones","name":"Test tones","version":"2.0.0","url":"packs/test-tones.zip","sha256":%q},{"id":"broken","name":"Broken","url":"packs/test-tones.zip","sha256":"00"}]}`, good)
	})
	mux.HandleFunc("/sounds/packs/test-tones.zip", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	p := NewPacks(d, st, t.TempDir())
	if err := st.Set(ctx, settings.KeySound, settings.Sound{}); err != nil {
		t.Fatal(err)
	}
	if list, err := p.Catalog(ctx); err != nil || len(list) != 0 {
		t.Fatalf("no catalog set: %v %+v", err, list)
	}
	if err := st.Set(ctx, settings.KeySound, settings.Sound{CatalogURL: srv.URL + "/sounds/catalog.json"}); err != nil {
		t.Fatal(err)
	}
	list, err := p.Catalog(ctx)
	if err != nil || len(list) != 2 || list[0].URL != srv.URL+"/sounds/packs/test-tones.zip" {
		t.Fatalf("catalog %v %+v", err, list)
	}
	pack, err := p.InstallFromCatalog(ctx, "test-tones", nil)
	if err != nil || pack.Origin != "catalog" || pack.Version != "2.0.0" {
		t.Fatalf("install %v %+v", err, pack)
	}
	if _, err := p.InstallFromCatalog(ctx, "broken", nil); err == nil {
		t.Fatal("installed a pack whose hash doesn't match")
	}
	if _, err := p.InstallFromCatalog(ctx, "missing", nil); err != ErrPackNotFound {
		t.Fatalf("a pack not in the catalog: %v", err)
	}
}

func seedChapters(t *testing.T, d *db.DB, series int64, pages map[float64]int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	for n, count := range pages {
		ch := &model.Chapter{SeriesID: series, NumberKey: fmt.Sprint(n), NumberSort: n, State: model.ChapterImported, FirstSeenAt: now, UpdatedAt: now}
		if _, err := d.NewInsert().Model(ch).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		f := &model.ChapterFile{ChapterID: ch.ID, SeriesID: series, RelativePath: fmt.Sprintf("%g.cbz", n), PageCount: count, ImportedAt: now}
		if _, err := d.NewInsert().Model(f).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model(ch).Set("file_id = ?", f.ID).WherePK().Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// TestCuesScenesAndSuggestions: moods check their sounds; a reader's cue
// beats an admin cue, which beats the title mood; page cues remember the
// page count and get flagged; genres suggest a mood when asked to; an
// exported soundtrack imports back by chapter numbers and sound hashes.
func TestCuesScenesAndSuggestions(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		moods, err := Moods(ctx, d, st)
		if err != nil {
			t.Fatal(err)
		}
		calm, tense, horror := moods[0], moods[2], moods[4]
		p := NewPacks(d, st, t.TempDir())
		if _, err := p.InstallZip(ctx, tonepack.Zip(t, "test-tones", "1.0.0"), "upload"); err != nil {
			t.Fatal(err)
		}
		low, high, beep := trackByTitle(t, d, "Low hum"), trackByTitle(t, d, "High hum"), trackByTitle(t, d, "Beep")

		newMood := func(name string) *model.AudioMood {
			return &model.AudioMood{Name: name, Color: tense.Color, Icon: "zap", Genres: []string{}, Keywords: []string{}, Prompts: []string{}}
		}
		if err := CreateMood(ctx, d, newMood("Wrong"), &Sounds{Ambience: []MoodLayer{{TrackID: beep.ID, Volume: 1}}}); err == nil {
			t.Fatal("an effect as an ambience layer")
		}
		five := slices.Repeat([]MoodLayer{{TrackID: low.ID, Volume: 1}}, 5)
		if err := CreateMood(ctx, d, newMood("Crowded"), &Sounds{Ambience: five}); err == nil {
			t.Fatal("five ambience layers")
		}
		storm := newMood("Storm")
		storm.SpotifyURI, storm.Shuffle = "spotify:playlist:37i9dQZF1DX4sWSpwq3LiO", true
		err = CreateMood(ctx, d, storm, &Sounds{Ambience: []MoodLayer{{TrackID: low.ID, Volume: 0.5}, {TrackID: high.ID, Volume: 0.3}}, SFX: []MoodPad{{TrackID: beep.ID, Label: "Bell"}}})
		if err != nil || len(storm.Ambience) != 2 || storm.SFX[0].Label != "Bell" {
			t.Fatalf("storm %v %+v", err, storm)
		}
		// an update that leaves the sounds out keeps them
		storm.Description = "Rain on the harbor"
		if err := UpdateMood(ctx, d, storm.ID, storm, nil); err != nil || len(storm.Ambience) != 2 || storm.Description == "" {
			t.Fatalf("update %v %+v", err, storm)
		}

		user, series := seedLibrary(t, d)
		seedChapters(t, d, series, map[float64]int{3: 20, 5: 30})
		page := 10
		shared := Doc{Cues: []Cue{{ChapterNumber: 0, MoodID: &calm.ID}, {ChapterNumber: 5, MoodID: &storm.ID}, {ChapterNumber: 5, Page: &page, MoodID: &calm.ID}}}
		if err := Save(ctx, d, series, nil, shared); err != nil {
			t.Fatal(err)
		}
		got, _ := Get(ctx, d, series, nil)
		if pc := got.Cues[2].PageCount; pc == nil || *pc != 30 {
			t.Fatalf("page count not kept: %+v", got.Cues[2])
		}

		at := func(ch float64) *Resolved {
			t.Helper()
			r, err := Resolve(ctx, d, st, series, user, nil, ch)
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		if r := at(3); r.Start == nil || r.Start.Mood.ID != calm.ID || r.Start.Source != FromShared {
			t.Fatalf("ch 3 plays the title mood: %+v", r.Start)
		}
		r := at(5)
		if r.Start == nil || r.Start.Mood.ID != storm.ID || len(r.Start.Mood.Ambience) != 2 ||
			r.Start.Music == nil || r.Start.Music.URI != "spotify:playlist:37i9dQZF1DX4sWSpwq3LiO" || len(r.Pages) != 1 || r.Pages[0].Page != 10 {
			t.Fatalf("ch 5 plays the admin's mood: %+v", r)
		}
		// the reader's own cues win where they sit
		if _, err := SetCue(ctx, d, series, &user, Cue{ChapterNumber: 5, MoodID: &horror.ID}); err != nil {
			t.Fatal(err)
		}
		if r := at(5); r.Start.Mood.ID != horror.ID || r.Start.Source != FromPersonal || r.Source != FromPersonal {
			t.Fatalf("my cue beats the admin's: %+v", r.Start)
		}
		if r := at(3); r.Start.Mood.ID != calm.ID {
			t.Fatalf("elsewhere the admin's still play: %+v", r.Start)
		}
		// removing it (no mood) brings the admin's back
		if _, err := SetCue(ctx, d, series, &user, Cue{ChapterNumber: 5}); err != nil {
			t.Fatal(err)
		}
		if r := at(5); r.Start.Mood.ID != storm.ID {
			t.Fatalf("after removing my cue: %+v", r.Start)
		}

		// a chapter that changed its pages flags the page cue
		if _, err := d.NewUpdate().Model((*model.ChapterFile)(nil)).Set("page_count = 31").Where("series_id = ? AND page_count = 30", series).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		counts, _ := PageCounts(ctx, d, series)
		got, _ = Get(ctx, d, series, nil)
		got.Flag(counts)
		if !got.Cues[2].NeedsReview || got.Cues[1].NeedsReview {
			t.Fatalf("flags %+v", got.Cues)
		}

		// export, wipe, import: moods come back from installed sounds
		exp, err := MakeExport(ctx, d, "Harbor", got)
		if err != nil || len(exp.Cues) != 3 || exp.Cues[1].Mood == nil || exp.Cues[1].Mood.Name != "Storm" || exp.Cues[1].Mood.Ambience[0].SHA256 != low.SHA256 || exp.Cues[0].Mood.Slug != "calm" {
			t.Fatalf("export %v %+v", err, exp)
		}
		exp.Cues[1].Mood.Ambience = append(exp.Cues[1].Mood.Ambience, ExportedSound{SHA256: "ff00", Volume: 1})
		if err := DeleteMood(ctx, d, storm.ID); err != nil {
			t.Fatal(err)
		}
		doc, res, err := ReadExport(ctx, d, *exp, true)
		if err != nil || res.MoodsCreated != 1 || res.MissingSounds != 1 || res.Cues != 3 || doc.Cues[1].MoodID == nil {
			t.Fatalf("import %v %+v %+v", err, res, doc)
		}
		if err := Save(ctx, d, series, nil, *doc); err != nil {
			t.Fatal(err)
		}
		if r := at(5); r.Start.Mood.Name != "Storm" || len(r.Start.Mood.Ambience) != 2 {
			t.Fatalf("imported mood: %+v", r.Start)
		}
		// a file from before moods and scenes were one: its scene is a mood
		old := Export{Format: ExportFormat, Version: 1, Cues: []ExportedCue{{Chapter: 7, Scene: &ExportedScene{Name: "Old rain", Mood: &ExportedMood{Slug: "tense", Name: "Tense"},
			Ambience: []ExportedSound{{SHA256: high.SHA256, Volume: 0.7}}}}}}
		odoc, ores, err := ReadExport(ctx, d, old, true)
		if err != nil || ores.MoodsCreated != 1 || odoc.Cues[0].MoodID == nil {
			t.Fatalf("old import %v %+v", err, ores)
		}
		if m, _ := Mood(ctx, d, *odoc.Cues[0].MoodID); m.Name != "Old rain" || m.Color != tense.Color || len(m.Ambience) != 1 {
			t.Fatalf("old scene as a mood %+v", m)
		}
		credits, err := Credits(ctx, d, doc)
		if err != nil || len(credits) != 3 {
			t.Fatalf("credits %v %+v", err, credits)
		}
		if _, _, err := ReadExport(ctx, d, Export{Format: "other"}, true); err == nil {
			t.Fatal("imported a file of another kind")
		}

		// genres suggest a mood only when asked and only without a title mood
		if err := Save(ctx, d, series, nil, Doc{}); err != nil {
			t.Fatal(err)
		}
		if err := Reset(ctx, d, series, user); err != nil {
			t.Fatal(err)
		}
		r, _ = Resolve(ctx, d, st, series, user, []string{"Psychological", "Seinen"}, 3)
		if r.Start != nil {
			t.Fatalf("suggested while off: %+v", r.Start)
		}
		if err := st.Set(ctx, settings.KeySound, settings.Sound{SuggestFromGenres: true}); err != nil {
			t.Fatal(err)
		}
		r, _ = Resolve(ctx, d, st, series, user, []string{"Psychological", "Seinen"}, 3)
		if r.Start == nil || r.Start.Mood.ID != horror.ID || r.Source != FromSuggested {
			t.Fatalf("suggestion %+v", r)
		}
	})
}

func TestCategoryAndKind(t *testing.T) {
	for name, want := range map[string]string{"Heavy Rain Loop": "weather", "harbor_waves": "water", "Sword Clash": "combat", "door-knock": "objects", "xyz": "other"} {
		if got := categoryOf(name); got != want {
			t.Errorf("categoryOf(%q) = %s, want %s", name, got, want)
		}
	}
	if k, ok := kindOf("Music/Battle Theme.ogg", 1<<20); ok {
		t.Errorf("music kept as %s", k)
	}
	if k, _ := kindOf("SFX/Thunder Loop.ogg", 100); k != model.TrackAmbience {
		t.Errorf("a loop under SFX is %s", k)
	}
	if k, _ := kindOf("misc/long.ogg", 5<<20); k != model.TrackAmbience {
		t.Errorf("a long unnamed file is %s", k)
	}
}

func moodByName(t *testing.T, d *db.DB, st *settings.Store, name string) model.AudioMood {
	t.Helper()
	moods, err := Moods(context.Background(), d, st)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range moods {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no mood %q", name)
	return model.AudioMood{}
}
