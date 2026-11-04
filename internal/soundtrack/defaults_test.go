package soundtrack

import (
	"context"
	"slices"
	"testing"

	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/dbtest"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// TestMoodDefaults: built-in moods follow the defaults in the code unless
// an admin edited a field, which keeps their value until a reset; a server
// that kept edits in the moods before is read as edited where they differ.
func TestMoodDefaults(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		moods, err := Moods(ctx, d, st)
		if err != nil {
			t.Fatal(err)
		}
		calm, sad := moods[0], moods[1]
		if !calm.HasDefaults || len(calm.Edited) != 0 {
			t.Fatalf("seeded %+v", calm)
		}

		// an admin changes the prompts: only those are edited
		calm.Prompts = []string{"a quiet tea ceremony"}
		if err := UpdateMood(ctx, d, calm.ID, &calm, nil); err != nil || !slices.Equal(calm.Edited, []string{FieldPrompts}) {
			t.Fatalf("update %v %+v", err, calm.Edited)
		}
		// setting a field back to its default isn't an edit
		calm.Prompts = slices.Clone(builtins[0].prompts)
		if err := UpdateMood(ctx, d, calm.ID, &calm, nil); err != nil || len(calm.Edited) != 0 {
			t.Fatalf("back to the default %v %+v", err, calm.Edited)
		}
		calm.Prompts = []string{"a quiet tea ceremony"}
		calm.Icon = "sun"
		if err := UpdateMood(ctx, d, calm.ID, &calm, nil); err != nil {
			t.Fatal(err)
		}

		// a new version changes the defaults: unedited fields follow
		old := builtins
		t.Cleanup(func() { builtins = old })
		builtins = slices.Clone(old)
		builtins[0].prompts = []string{"new calm prompt"}
		builtins[0].keywords = []string{"tea"}
		builtins[1].prompts = []string{"new sad prompt"}
		if moods, err = Moods(ctx, d, st); err != nil {
			t.Fatal(err)
		}
		if got := moods[0]; got.Prompts[0] != "a quiet tea ceremony" || got.Icon != "sun" || !slices.Equal(got.Keywords, []string{"tea"}) {
			t.Fatalf("edited calm after new defaults %+v", got)
		}
		if got := moods[1]; got.ID != sad.ID || !slices.Equal(got.Prompts, []string{"new sad prompt"}) {
			t.Fatalf("unedited mood after new defaults %+v", got)
		}

		// reset: back to the defaults, nothing edited
		got, err := ResetMood(ctx, d, calm.ID)
		if err != nil || len(got.Edited) != 0 || got.Icon != "leaf" || !slices.Equal(got.Prompts, []string{"new calm prompt"}) {
			t.Fatalf("reset %v %+v", err, got)
		}
		// a mood an admin made has no defaults
		mine := &model.AudioMood{Name: "Mine", Color: "#aabbcc", Icon: "zap"}
		if err := CreateMood(ctx, d, mine, nil); err != nil || mine.HasDefaults {
			t.Fatalf("create %v %+v", err, mine)
		}
		if _, err := ResetMood(ctx, d, mine.ID); err == nil {
			t.Fatal("reset a mood without defaults")
		}
	})
}

// TestMoodDefaultsFirstRun: moods edited before edits were kept apart
// keep those edits when defaults first apply.
func TestMoodDefaultsFirstRun(t *testing.T) {
	dbtest.ForEachDialect(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		st := settings.NewStore(d)
		if _, err := Moods(ctx, d, st); err != nil {
			t.Fatal(err)
		}
		if _, err := d.NewUpdate().Model((*model.AudioMood)(nil)).Set("name = ?", "Chill").Where("slug = ?", "calm").Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := st.Set(ctx, settings.KeySoundtracks, settings.Soundtracks{MoodsSeeded: true}); err != nil {
			t.Fatal(err)
		}
		moods, err := Moods(ctx, d, st)
		if err != nil || moods[0].Name != "Chill" || !slices.Equal(moods[0].Edited, []string{FieldName}) || len(moods[1].Edited) != 0 {
			t.Fatalf("first run %v %+v", err, moods[0])
		}
	})
}
