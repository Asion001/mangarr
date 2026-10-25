package api

import (
	"testing"

	"github.com/Asion001/mangarr/internal/model"
)

func TestDisplayTitle(t *testing.T) {
	ser := model.Series{Title: "Атака титанов", Metadata: model.SeriesMetadata{
		Titles: map[string]string{"en": "Attack on Titan", "ru": "Атака титанов", model.TitleRomanized: "Shingeki no Kyojin"}}}
	for lang, want := range map[string]string{"en": "Attack on Titan", "": "Attack on Titan", "ru": "Атака титанов", "uk": "Атака титанов", "de": "Shingeki no Kyojin"} {
		if got, ok := displayTitle(ser, lang); got != want || !ok {
			t.Errorf("%q: %q, %v; want %q", lang, got, ok, want)
		}
	}
	ser.Metadata.Titles["uk"] = "Атака титанів"
	if got, _ := displayTitle(ser, "uk"); got != "Атака титанів" {
		t.Errorf("uk: %q", got)
	}
	// a name the user set, or none from metadata, is the series' own
	ser.Metadata.Locks = []string{"title"}
	if got, ok := displayTitle(ser, "en"); got != ser.Title || ok {
		t.Errorf("locked: %q, %v", got, ok)
	}
	if got, ok := displayTitle(model.Series{Title: "Own"}, "en"); got != "Own" || ok {
		t.Errorf("no titles: %q, %v", got, ok)
	}
}
