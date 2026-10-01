package api_test

import (
	"context"
	"testing"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/model"
)

func TestUIPreferencesIsolation(t *testing.T) {
	srv, a := newServer(t, false)
	for _, name := range []string{"boss", "friend"} {
		if _, err := a.Auth.CreateUser(context.Background(), auth.NewUser{Username: name, Password: "test-password"}); err != nil {
			t.Fatal(err)
		}
	}
	boss := caller{t, login(t, srv.URL, "boss", "test-password"), srv.URL}
	friend := caller{t, login(t, srv.URL, "friend", "test-password"), srv.URL}
	const path = "/api/v1/me/ui-preferences"
	var v model.UIPreferences
	if code := friend.do("GET", path, "", &v); code != 200 || v.Locale != "auto" || v.Mode != "reading" {
		t.Fatalf("defaults: %d %+v", code, v)
	}
	if code := boss.do("PUT", path, `{"locale":"ru","mode":"editing"}`, &v); code != 200 || v.Mode != "editing" {
		t.Fatalf("boss: %d %+v", code, v)
	}
	if code := friend.do("PUT", path, `{"locale":"ua","mode":"editing"}`, &v); code != 200 || v.Locale != "uk" || v.Mode != "reading" {
		t.Fatalf("friend: %d %+v", code, v)
	}
	if code := boss.do("GET", path, "", &v); code != 200 || v.Locale != "ru" || v.Mode != "editing" {
		t.Fatalf("isolation: %d %+v", code, v)
	}
	if code := boss.do("PUT", path, `{"locale":"ru","mode":"editing","options":{"otherLanguageChapters":false}}`, &v); code != 200 || v.Options.OtherLanguageChapters == nil || *v.Options.OtherLanguageChapters {
		t.Fatalf("options: %d %+v", code, v)
	}
	// a save without options keeps them
	if code := boss.do("PUT", path, `{"locale":"en","mode":"editing"}`, &v); code != 200 || v.Options.OtherLanguageChapters == nil {
		t.Fatalf("options kept: %d %+v", code, v)
	}
	if code := boss.do("PUT", path, `{"locale":"xx","mode":"reading"}`, nil); code != 422 {
		t.Fatalf("invalid locale: %d", code)
	}
}
