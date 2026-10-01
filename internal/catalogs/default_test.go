package catalogs

import (
	"testing"

	"github.com/Asion001/mangarr/internal/settings"
)

func TestDefaultOn(t *testing.T) {
	none := settings.Sources{}
	if !DefaultOn(none, 1, "a", "ar") {
		t.Fatal("without search languages every catalog is on")
	}
	st := settings.Sources{
		DefaultLanguages: []string{"en"},
		LanguageDefaults: []settings.LanguageDefault{{Language: "RU", Sources: []string{"1:jp-extra"}}},
	}
	for _, c := range []struct {
		id, lang string
		want     bool
	}{
		{"a", "en", true},
		{"b", "ru", true}, // a language default's language
		{"c", "multi", true},
		{"d", "ar", false},
		{"jp-extra", "ja", true}, // named by a language default
	} {
		if got := DefaultOn(st, 1, c.id, c.lang); got != c.want {
			t.Errorf("%s (%s): got %v, want %v", c.id, c.lang, got, c.want)
		}
	}
}
