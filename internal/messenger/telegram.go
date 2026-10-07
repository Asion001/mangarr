// Package messenger talks to the server's own Telegram and Discord bots:
// checking their credentials, linking people's accounts and sending them
// private messages.
package messenger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Identity is who a bot or a linked account is.
type Identity struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name,omitempty"`
}

// APIError is an error answer from Telegram or Discord.
type APIError struct {
	Status int
	Code   int // Telegram error_code or Discord's JSON error code
	Msg    string
}

func (e *APIError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// Telegram is a Bot API client.
type Telegram struct {
	APIURL string
	Token  string
	HTTP   *http.Client
}

func (t *Telegram) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return &http.Client{Timeout: 70 * time.Second}
}

// call posts params to method and decodes the result into out.
func (t *Telegram) call(ctx context.Context, method string, params url.Values, out any) error {
	if strings.TrimSpace(t.Token) == "" {
		return errors.New("no Telegram bot token")
	}
	endpoint := strings.TrimRight(t.APIURL, "/") + "/bot" + t.Token + "/" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(params.Encode()))
	if err != nil {
		return errors.New("invalid Telegram API server")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := t.client().Do(req)
	if err != nil {
		// the URL holds the token: never pass the url.Error on
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("Telegram can't be reached")
	}
	defer resp.Body.Close()
	var body struct {
		OK          bool            `json:"ok"`
		ErrorCode   int             `json:"error_code"`
		Description string          `json:"description"`
		Result      json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return &APIError{Status: resp.StatusCode, Msg: fmt.Sprintf("Telegram answered HTTP %d", resp.StatusCode)}
	}
	if !body.OK {
		msg := body.Description
		if msg == "" {
			msg = fmt.Sprintf("Telegram answered HTTP %d", resp.StatusCode)
		}
		return &APIError{Status: resp.StatusCode, Code: body.ErrorCode, Msg: "Telegram: " + msg}
	}
	if out != nil {
		return json.Unmarshal(body.Result, out)
	}
	return nil
}

type tgUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

func (u tgUser) identity() Identity {
	return Identity{ID: strconv.FormatInt(u.ID, 10), Username: u.Username, Name: strings.TrimSpace(u.FirstName + " " + u.LastName)}
}

// Me checks the token and returns the bot.
func (t *Telegram) Me(ctx context.Context) (Identity, error) {
	var u tgUser
	if err := t.call(ctx, "getMe", url.Values{}, &u); err != nil {
		return Identity{}, err
	}
	return u.identity(), nil
}
