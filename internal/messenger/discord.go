package messenger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DiscordAPI is Discord's REST API; tests point it at a fake.
var DiscordAPI = "https://discord.com/api/v10"

// Discord is a bot client.
type Discord struct {
	Token string
	// APIURL defaults to DiscordAPI (tests point it elsewhere).
	APIURL string
	HTTP   *http.Client
}

func (d *Discord) base() string {
	if d.APIURL != "" {
		return strings.TrimRight(d.APIURL, "/")
	}
	return DiscordAPI
}

func (d *Discord) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do sends a request with the bot token and decodes the answer into out.
func (d *Discord) do(ctx context.Context, method, path string, in, out any) error {
	if strings.TrimSpace(d.Token) == "" {
		return errors.New("no Discord bot token")
	}
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bot "+d.Token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := d.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Discord can't be reached")
	}
	defer resp.Body.Close()
	return decodeDiscord(resp, out)
}

func decodeDiscord(resp *http.Response, out any) error {
	r := io.LimitReader(resp.Body, 4<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(r).Decode(&e)
		msg := e.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Code: e.Code, Msg: "Discord: " + msg}
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(r).Decode(out)
}

type discordUser struct {
	ID         string `json:"id"`
	Username   string `json:"username"`
	GlobalName string `json:"global_name"`
}

func (u discordUser) identity() Identity {
	return Identity{ID: u.ID, Username: u.Username, Name: u.GlobalName}
}

// Me checks the token and returns the bot.
func (d *Discord) Me(ctx context.Context) (Identity, error) {
	var u discordUser
	if err := d.do(ctx, http.MethodGet, "/users/@me", nil, &u); err != nil {
		return Identity{}, err
	}
	if u.ID == "" {
		return Identity{}, errors.New("Discord: unexpected answer")
	}
	return u.identity(), nil
}

// DiscordAuthorize is where people sign in to link their account.
var DiscordAuthorize = "https://discord.com/oauth2/authorize"

// DiscordAuthorizeURL sends a person to Discord to sign in with the identify
// scope; state comes back on the redirect.
func DiscordAuthorizeURL(clientID, redirectURI, state string) string {
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {clientID},
		"scope":         {"identify"},
		"redirect_uri":  {redirectURI},
		"state":         {state},
		"prompt":        {"none"},
	}
	return DiscordAuthorize + "?" + q.Encode()
}

// DiscordUser trades an authorization code for the person who signed in.
func DiscordUser(ctx context.Context, client *http.Client, clientID, secret, redirectURI, code string) (Identity, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(DiscordAPI, "/")+"/oauth2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Identity{}, err
	}
	req.SetBasicAuth(clientID, secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return Identity{}, errors.New("Discord can't be reached")
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	err = decodeDiscord(resp, &tok)
	resp.Body.Close()
	if err != nil {
		return Identity{}, err
	}
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(DiscordAPI, "/")+"/users/@me", nil)
	if err != nil {
		return Identity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	resp, err = client.Do(req)
	if err != nil {
		return Identity{}, errors.New("Discord can't be reached")
	}
	defer resp.Body.Close()
	var u discordUser
	if err := decodeDiscord(resp, &u); err != nil {
		return Identity{}, err
	}
	if u.ID == "" {
		return Identity{}, errors.New("Discord: unexpected answer")
	}
	return u.identity(), nil
}

// DM opens (or reuses) the direct-message channel with a user and returns its id.
func (d *Discord) DM(ctx context.Context, userID string) (string, error) {
	var ch struct {
		ID string `json:"id"`
	}
	if err := d.do(ctx, http.MethodPost, "/users/@me/channels", map[string]string{"recipient_id": userID}, &ch); err != nil {
		return "", err
	}
	return ch.ID, nil
}

// Send posts a message (Discord markdown) to a channel.
func (d *Discord) Send(ctx context.Context, channelID, content string) error {
	return d.do(ctx, http.MethodPost, "/channels/"+url.PathEscape(channelID)+"/messages",
		map[string]any{"content": content, "allowed_mentions": map[string]any{"parse": []string{}}}, nil)
}

var (
	tagLink = regexp.MustCompile(`<a href="([^"]*)">(.*?)</a>`)
	tagAny  = regexp.MustCompile(`</?[a-z]+[^>]*>`)
)

// Markdown turns the Telegram HTML of a message (b, i, code, a) into
// Discord markdown.
func Markdown(h string) string {
	h = tagLink.ReplaceAllString(h, "[$2]($1)")
	r := strings.NewReplacer("<b>", "**", "</b>", "**", "<i>", "*", "</i>", "*", "<code>", "`", "</code>", "`")
	h = r.Replace(h)
	h = tagAny.ReplaceAllString(h, "")
	return html.UnescapeString(h)
}
