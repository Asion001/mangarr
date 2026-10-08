package api_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// TestOfferedLanguages: anyone signed in gets the languages set up on the
// server (search defaults first), not every language a catalog has.
func TestOfferedLanguages(t *testing.T) {
	srv, a := newServer(t, false)
	ctx := context.Background()
	now := time.Now().UTC()
	for _, lang := range []string{"en", "*", "ru"} {
		if _, err := a.DB.NewInsert().Model(&model.RootFolder{Path: t.TempDir(), Language: lang, CreatedAt: now}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	src, err := a.Settings.Sources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	src.DefaultLanguages = []string{"uk", "en"}
	src.LanguageDefaults = []settings.LanguageDefault{{Language: "ja"}, {Language: "EN"}}
	if err := a.Settings.Set(ctx, settings.KeySources, src); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "boss", Password: "boss-pass-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Auth.CreateUser(ctx, auth.NewUser{Username: "ann", Password: "ann-pass-1"}); err != nil {
		t.Fatal(err)
	}
	ann := caller{t, login(t, srv.URL, "ann", "ann-pass-1"), srv.URL}
	var got struct {
		Defaults  []string `json:"defaults"`
		Languages []string `json:"languages"`
	}
	if code := ann.do("GET", "/api/v1/languages", "", &got); code != 200 {
		t.Fatalf("languages: %d", code)
	}
	if !slices.Equal(got.Defaults, []string{"uk", "en"}) || !slices.Equal(got.Languages, []string{"uk", "en", "ru", "ja"}) {
		t.Fatalf("offered %+v", got)
	}
}
