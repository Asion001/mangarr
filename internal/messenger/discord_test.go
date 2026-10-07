package messenger

import (
	"net/url"
	"testing"
)

func TestDiscordAuthorizeURL(t *testing.T) {
	u, err := url.Parse(DiscordAuthorizeURL("123", "https://manga.example.com/api/v1/me/messenger/discord/callback", "st_ate-1"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Host != "discord.com" || q.Get("scope") != "identify" || q.Get("client_id") != "123" || q.Get("state") != "st_ate-1" ||
		q.Get("response_type") != "code" || q.Get("redirect_uri") != "https://manga.example.com/api/v1/me/messenger/discord/callback" {
		t.Fatalf("authorize URL %s", u)
	}
}

func TestMarkdown(t *testing.T) {
	for in, want := range map[string]string{
		`<b>A &lt;B&gt;</b> <i>x</i> <code>1</code>`:   "**A <B>** *x* `1`",
		`<a href="https://e.x/s?a=1&amp;b=2">open</a>`: "[open](https://e.x/s?a=1&b=2)",
		`plain`: "plain",
	} {
		if got := Markdown(in); got != want {
			t.Errorf("Markdown(%q) = %q, want %q", in, got, want)
		}
	}
}
