package messenger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
