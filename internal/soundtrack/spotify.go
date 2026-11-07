package soundtrack

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/uptrace/bun"
	"golang.org/x/sync/singleflight"

	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
)

// Where Spotify answers (tests point these at a fake).
var (
	AccountsURL = "https://accounts.spotify.com"
	APIURL      = "https://api.spotify.com/v1"
)

// DevModeUsers is how many people a Spotify app in development mode may
// have (Spotify's February 2026 rules), each added by email in its
// dashboard.
const DevModeUsers = 5

// SearchLimit is the most results Spotify returns per search page.
const SearchLimit = 10

// Scopes are what mangarr asks each reader's Spotify account for: playing
// in the browser (Web Playback SDK) and controlling their other devices.
var Scopes = []string{"streaming", "user-read-playback-state", "user-modify-playback-state", "user-read-email", "user-read-private"}

var (
	ErrNotConfigured = errors.New("Spotify isn't set up on this server: an admin adds a Client ID in Settings › Soundtracks")
	ErrNotConnected  = errors.New("connect your Spotify account first")
	ErrBadState      = errors.New("the Spotify sign-in expired or was already used: try again")
	ErrPremium       = errors.New("playing music needs Spotify Premium")
	ErrNoDevice      = errors.New("no Spotify device is active: open Spotify on a phone, computer or speaker")
	// ErrDeviceGone: the device asked for isn't there any more (a closed
	// browser tab's player, a phone that went to sleep).
	ErrDeviceGone = errors.New("that Spotify device isn't there any more: pick another under Play music on…")
	ErrNoVolume   = errors.New("this Spotify device doesn't let others set its volume: use its own volume buttons")
)

// APIError is an error answer from Spotify.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("Spotify answered %d", e.Status)
	}
	return "Spotify: " + e.Message
}

// pendingAuth is a sign-in in progress, by its state.
type pendingAuth struct {
	userID             int64
	verifier, redirect string
	returnTo           string
	expires            time.Time
}

type accessToken struct {
	token    string
	expires  time.Time
	clientID string
}

// Spotify talks to Spotify for readers: sign-in (Authorization Code with
// PKCE), short-lived tokens, playback and search.
type Spotify struct {
	DB       bun.IDB
	Settings *settings.Store
	HTTP     *http.Client

	mu      sync.Mutex
	pending map[string]pendingAuth
	tokens  map[int64]accessToken
	// refreshing makes one refresh per reader at a time: Spotify rotates
	// refresh tokens, so a second refresh with the same token could be
	// turned away and sign the reader out
	refreshing singleflight.Group
}

// New returns the Spotify service.
func New(db bun.IDB, st *settings.Store) *Spotify {
	return &Spotify{DB: db, Settings: st, HTTP: &http.Client{Timeout: 20 * time.Second}, pending: map[string]pendingAuth{}, tokens: map[int64]accessToken{}}
}

// ClientID is the admin's Spotify app ("" when not set up).
func (s *Spotify) ClientID(ctx context.Context) string {
	c, err := s.Settings.Spotify(ctx)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(c.ClientID)
}

func randomString(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// challenge is the PKCE S256 challenge for a verifier.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// Start begins a reader's sign-in and returns the Spotify page to send them
// to. returnTo is the mangarr page to come back to.
func (s *Spotify) Start(ctx context.Context, userID int64, redirectURI, returnTo string) (string, error) {
	clientID := s.ClientID(ctx)
	if clientID == "" {
		return "", ErrNotConfigured
	}
	state, verifier := randomString(24), randomString(48)
	s.mu.Lock()
	now := time.Now()
	for k, p := range s.pending {
		if now.After(p.expires) {
			delete(s.pending, k)
		}
	}
	s.pending[state] = pendingAuth{userID: userID, verifier: verifier, redirect: redirectURI, returnTo: returnTo, expires: now.Add(15 * time.Minute)}
	s.mu.Unlock()
	q := url.Values{
		"client_id":             {clientID},
		"response_type":         {"code"},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"scope":                 {strings.Join(Scopes, " ")},
		"code_challenge_method": {"S256"},
		"code_challenge":        {challenge(verifier)},
	}
	return AccountsURL + "/authorize?" + q.Encode(), nil
}

// ReturnTo is where a sign-in started from ("" for an unknown state).
func (s *Spotify) ReturnTo(state string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[state].returnTo
}

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func (s *Spotify) tokenRequest(ctx context.Context, form url.Values) (*tokenAnswer, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, AccountsURL+"/api/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var a tokenAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&a); err != nil {
		return nil, &APIError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK || a.AccessToken == "" {
		msg := a.Description
		if msg == "" {
			msg = a.Error
		}
		return &a, &APIError{Status: resp.StatusCode, Message: msg}
	}
	return &a, nil
}

// Profile is a Spotify account.
type Profile struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Product     string `json:"product"`
}

// Finish completes a sign-in: it trades the code for tokens and keeps the
// reader's refresh token. It returns where to send them back to.
func (s *Spotify) Finish(ctx context.Context, state, code string) (string, error) {
	s.mu.Lock()
	p, ok := s.pending[state]
	delete(s.pending, state)
	s.mu.Unlock()
	if !ok || time.Now().After(p.expires) {
		return "", ErrBadState
	}
	clientID := s.ClientID(ctx)
	if clientID == "" {
		return p.returnTo, ErrNotConfigured
	}
	a, err := s.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.redirect},
		"client_id":     {clientID},
		"code_verifier": {p.verifier},
	})
	if err != nil {
		return p.returnTo, err
	}
	var prof Profile
	if err := s.call(ctx, a.AccessToken, http.MethodGet, "/me", nil, &prof); err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusForbidden {
			// a development-mode app only lets in people on its user list
			return p.returnTo, errors.New("Spotify turned the account away: an admin adds its email under User Management in the Spotify dashboard (5 people at most)")
		}
		return p.returnTo, err
	}
	enc, err := s.seal(ctx, a.RefreshToken)
	if err != nil {
		return p.returnTo, err
	}
	acc := model.SpotifyAccount{UserID: p.userID, SpotifyUserID: prof.ID, DisplayName: prof.DisplayName, Product: prof.Product, RefreshTokenEnc: enc, Scopes: a.Scope, ConnectedAt: time.Now().UTC()}
	if _, err := s.DB.NewInsert().Model(&acc).On("CONFLICT (user_id) DO UPDATE").
		Set("spotify_user_id = EXCLUDED.spotify_user_id, display_name = EXCLUDED.display_name, product = EXCLUDED.product, refresh_token_enc = EXCLUDED.refresh_token_enc, scopes = EXCLUDED.scopes, connected_at = EXCLUDED.connected_at").
		Exec(ctx); err != nil {
		return p.returnTo, err
	}
	s.mu.Lock()
	s.tokens[p.userID] = accessToken{token: a.AccessToken, expires: time.Now().Add(time.Duration(a.ExpiresIn) * time.Second), clientID: clientID}
	s.mu.Unlock()
	return p.returnTo, nil
}

// Account is a reader's connected account (nil when not connected).
func (s *Spotify) Account(ctx context.Context, userID int64) (*model.SpotifyAccount, error) {
	var acc model.SpotifyAccount
	if err := s.DB.NewSelect().Model(&acc).Where("user_id = ?", userID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &acc, nil
}

// Accounts lists the connected accounts.
func (s *Spotify) Accounts(ctx context.Context) ([]model.SpotifyAccount, error) {
	out := []model.SpotifyAccount{}
	err := s.DB.NewSelect().Model(&out).Order("connected_at").Scan(ctx)
	return out, err
}

// Disconnect forgets a reader's account.
func (s *Spotify) Disconnect(ctx context.Context, userID int64) error {
	s.mu.Lock()
	delete(s.tokens, userID)
	s.mu.Unlock()
	_, err := s.DB.NewDelete().Model((*model.SpotifyAccount)(nil)).Where("user_id = ?", userID).Exec(ctx)
	return err
}

// Token returns a reader's access token, refreshing it when it's about to
// run out.
func (s *Spotify) Token(ctx context.Context, userID int64) (string, time.Time, error) {
	clientID := s.ClientID(ctx)
	if clientID == "" {
		return "", time.Time{}, ErrNotConfigured
	}
	s.mu.Lock()
	t, ok := s.tokens[userID]
	s.mu.Unlock()
	if ok && t.clientID == clientID && time.Until(t.expires) > time.Minute {
		return t.token, t.expires, nil
	}
	v, err, _ := s.refreshing.Do(strconv.FormatInt(userID, 10), func() (any, error) {
		// shared by the callers waiting on it: one caller leaving doesn't
		// cancel the refresh for the others
		return s.refresh(context.WithoutCancel(ctx), userID, clientID)
	})
	if err != nil {
		return "", time.Time{}, err
	}
	t = v.(accessToken)
	return t.token, t.expires, nil
}

// refresh trades a reader's refresh token for a new access token.
func (s *Spotify) refresh(ctx context.Context, userID int64, clientID string) (accessToken, error) {
	s.mu.Lock()
	t, ok := s.tokens[userID]
	s.mu.Unlock()
	if ok && t.clientID == clientID && time.Until(t.expires) > time.Minute {
		return t, nil // refreshed while this caller waited
	}
	acc, err := s.Account(ctx, userID)
	if err != nil {
		return accessToken{}, err
	}
	if acc == nil {
		return accessToken{}, ErrNotConnected
	}
	refresh, err := s.open(ctx, acc.RefreshTokenEnc)
	if err != nil {
		// the instance secret changed: the reader signs in again
		_ = s.disconnectIf(ctx, userID, acc.RefreshTokenEnc)
		return accessToken{}, ErrNotConnected
	}
	a, err := s.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
	if err != nil {
		if a != nil && a.Error == "invalid_grant" {
			// revoked in Spotify, or made for another Client ID; a token
			// stored since (a new sign-in) is kept
			_ = s.disconnectIf(ctx, userID, acc.RefreshTokenEnc)
			return accessToken{}, ErrNotConnected
		}
		return accessToken{}, err
	}
	if a.RefreshToken != "" && a.RefreshToken != refresh {
		if enc, err := s.seal(ctx, a.RefreshToken); err == nil {
			_, _ = s.DB.NewUpdate().Model((*model.SpotifyAccount)(nil)).Set("refresh_token_enc = ?", enc).
				Where("user_id = ? AND refresh_token_enc = ?", userID, acc.RefreshTokenEnc).Exec(ctx)
		}
	}
	t = accessToken{token: a.AccessToken, expires: time.Now().Add(time.Duration(a.ExpiresIn) * time.Second), clientID: clientID}
	s.mu.Lock()
	s.tokens[userID] = t
	s.mu.Unlock()
	return t, nil
}

// disconnectIf forgets a reader's account when its stored refresh token is
// still the one that failed.
func (s *Spotify) disconnectIf(ctx context.Context, userID int64, sealed string) error {
	s.mu.Lock()
	delete(s.tokens, userID)
	s.mu.Unlock()
	_, err := s.DB.NewDelete().Model((*model.SpotifyAccount)(nil)).Where("user_id = ? AND refresh_token_enc = ?", userID, sealed).Exec(ctx)
	return err
}

// key is the token encryption key, from the instance secret.
func (s *Spotify) key(ctx context.Context) ([]byte, error) {
	g, err := s.Settings.General(ctx)
	if err != nil {
		return nil, err
	}
	if g.SessionSecret == "" {
		return nil, errors.New("the instance secret isn't set")
	}
	sum := sha256.Sum256([]byte("spotify-refresh-token:" + g.SessionSecret))
	return sum[:], nil
}

// seal encrypts a token (AES-GCM; base64 of nonce and ciphertext).
func (s *Spotify) seal(ctx context.Context, plain string) (string, error) {
	key, err := s.key(ctx)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(gcm.Seal(nonce, nonce, []byte(plain), nil)), nil
}

func (s *Spotify) open(ctx context.Context, sealed string) (string, error) {
	key, err := s.key(ctx)
	if err != nil {
		return "", err
	}
	raw, err := base64.RawStdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("sealed token too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}

// call makes a Web API request with a token; out may be nil.
func (s *Spotify) call(ctx context.Context, token, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, APIURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Reason  string `json:"reason"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		switch {
		case e.Error.Reason == "PREMIUM_REQUIRED":
			return ErrPremium
		case e.Error.Reason == "VOLUME_CONTROL_DISALLOW":
			return ErrNoVolume
		case e.Error.Reason == "NO_ACTIVE_DEVICE" || (resp.StatusCode == http.StatusNotFound && strings.Contains(path, "/me/player")):
			return ErrNoDevice
		}
		return &APIError{Status: resp.StatusCode, Message: e.Error.Message}
	}
	if out == nil || len(data) == 0 || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.Unmarshal(data, out)
}

// as makes a Web API request as a reader.
func (s *Spotify) as(ctx context.Context, userID int64, method, path string, body, out any) error {
	token, _, err := s.Token(ctx, userID)
	if err != nil {
		return err
	}
	return s.call(ctx, token, method, path, body, out)
}

// Device is a place Spotify can play.
type Device struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type" doc:"Computer, Smartphone, Speaker…"`
	IsActive      bool   `json:"isActive"`
	VolumePercent *int   `json:"volumePercent,omitempty"`
}

type spotifyDevice struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Type          string `json:"type"`
	IsActive      bool   `json:"is_active"`
	IsRestricted  bool   `json:"is_restricted"`
	VolumePercent *int   `json:"volume_percent"`
}

func (d spotifyDevice) device() Device {
	return Device{ID: d.ID, Name: d.Name, Type: d.Type, IsActive: d.IsActive, VolumePercent: d.VolumePercent}
}

// Devices lists a reader's devices Spotify can play on.
func (s *Spotify) Devices(ctx context.Context, userID int64) ([]Device, error) {
	var a struct {
		Devices []spotifyDevice `json:"devices"`
	}
	if err := s.as(ctx, userID, http.MethodGet, "/me/player/devices", nil, &a); err != nil {
		return nil, err
	}
	out := []Device{}
	for _, d := range a.Devices {
		if d.ID != "" && !d.IsRestricted {
			out = append(out, d.device())
		}
	}
	return out, nil
}

func deviceQuery(path, deviceID string, q url.Values) string {
	if q == nil {
		q = url.Values{}
	}
	if deviceID != "" {
		q.Set("device_id", deviceID)
	}
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// Play is music to start.
type Play struct {
	DeviceID string `json:"deviceId,omitempty" doc:"Empty: the active device"`
	URI      string `json:"uri" doc:"A playlist or album (played as a context) or a track"`
	Shuffle  *bool  `json:"shuffle,omitempty"`
	Volume   *int   `json:"volume,omitempty" minimum:"0" maximum:"100"`
}

// Play starts music on a reader's device: playlists and albums by context
// URI, a track on its own.
func (s *Spotify) Play(ctx context.Context, userID int64, p Play) error {
	if p.URI == "" || !ValidURI(p.URI) {
		return invalid("play a Spotify playlist, album or track")
	}
	if p.Volume != nil {
		// best effort: phones' Spotify apps don't let others set their
		// volume, and the music should start anyway
		_ = s.Volume(ctx, userID, *p.Volume, p.DeviceID)
	}
	body := map[string]any{"context_uri": p.URI}
	if strings.HasPrefix(p.URI, "spotify:track:") {
		body = map[string]any{"uris": []string{p.URI}}
	}
	play := func() error {
		return s.as(ctx, userID, http.MethodPut, deviceQuery("/me/player/play", p.DeviceID, nil), body, nil)
	}
	err := play()
	if errors.Is(err, ErrNoDevice) && p.DeviceID != "" {
		// a device Spotify doesn't count as active yet (a browser's player
		// that just started) takes the playback over first
		if err = s.Transfer(ctx, userID, p.DeviceID); err == nil {
			err = play()
		}
		if errors.Is(err, ErrNoDevice) {
			err = ErrDeviceGone
		}
	}
	if err != nil {
		return err
	}
	if p.Shuffle != nil {
		// after play: shuffle needs the device to be playing already
		_ = s.as(ctx, userID, http.MethodPut, deviceQuery("/me/player/shuffle", p.DeviceID, url.Values{"state": {strconv.FormatBool(*p.Shuffle)}}), nil, nil)
	}
	return nil
}

// Transfer moves a reader's playback to a device, without starting it.
func (s *Spotify) Transfer(ctx context.Context, userID int64, deviceID string) error {
	return s.as(ctx, userID, http.MethodPut, "/me/player", map[string]any{"device_ids": []string{deviceID}, "play": false}, nil)
}

// Pause pauses a reader's playback.
func (s *Spotify) Pause(ctx context.Context, userID int64, deviceID string) error {
	return s.as(ctx, userID, http.MethodPut, deviceQuery("/me/player/pause", deviceID, nil), nil, nil)
}

// Next skips to the next track of what plays.
func (s *Spotify) Next(ctx context.Context, userID int64, deviceID string) error {
	return s.as(ctx, userID, http.MethodPost, deviceQuery("/me/player/next", deviceID, nil), nil, nil)
}

// Resume resumes a reader's playback where it was.
func (s *Spotify) Resume(ctx context.Context, userID int64, deviceID string) error {
	return s.as(ctx, userID, http.MethodPut, deviceQuery("/me/player/play", deviceID, nil), nil, nil)
}

// Volume sets the music volume (0–100) on a reader's device.
func (s *Spotify) Volume(ctx context.Context, userID int64, volume int, deviceID string) error {
	volume = max(0, min(100, volume))
	return s.as(ctx, userID, http.MethodPut, deviceQuery("/me/player/volume", deviceID, url.Values{"volume_percent": {strconv.Itoa(volume)}}), nil, nil)
}

type image struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// cover picks the smallest image at least 160 pixels wide, else the
// largest.
func cover(images []image) string {
	best, bestW := "", -1
	for _, im := range images {
		big, bestBig := im.Width >= 160, bestW >= 160
		switch {
		case bestW < 0, big && (!bestBig || im.Width < bestW), !big && !bestBig && im.Width > bestW:
			best, bestW = im.URL, im.Width
		}
	}
	return best
}

type named struct {
	Name string `json:"name"`
}

func names(list []named) string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.Name)
	}
	return strings.Join(out, ", ")
}

// NowPlaying is what plays on a reader's Spotify.
type NowPlaying struct {
	IsPlaying  bool    `json:"isPlaying"`
	Device     *Device `json:"device,omitempty"`
	ContextURI string  `json:"contextUri,omitempty"`
	Track      string  `json:"track,omitempty"`
	Artists    string  `json:"artists,omitempty"`
	ImageURL   string  `json:"imageUrl,omitempty"`
	Shuffle    bool    `json:"shuffle"`
}

// Player returns what plays on a reader's Spotify (empty when nothing).
func (s *Spotify) Player(ctx context.Context, userID int64) (*NowPlaying, error) {
	var a struct {
		IsPlaying    bool           `json:"is_playing"`
		ShuffleState bool           `json:"shuffle_state"`
		Device       *spotifyDevice `json:"device"`
		Context      *struct {
			URI string `json:"uri"`
		} `json:"context"`
		Item *struct {
			Name    string  `json:"name"`
			Artists []named `json:"artists"`
			Album   struct {
				Images []image `json:"images"`
			} `json:"album"`
		} `json:"item"`
	}
	if err := s.as(ctx, userID, http.MethodGet, "/me/player", nil, &a); err != nil {
		if errors.Is(err, ErrNoDevice) {
			return &NowPlaying{}, nil
		}
		return nil, err
	}
	out := &NowPlaying{IsPlaying: a.IsPlaying, Shuffle: a.ShuffleState}
	if a.Device != nil {
		d := a.Device.device()
		out.Device = &d
	}
	if a.Context != nil {
		out.ContextURI = a.Context.URI
	}
	if a.Item != nil {
		out.Track, out.Artists, out.ImageURL = a.Item.Name, names(a.Item.Artists), cover(a.Item.Album.Images)
	}
	return out, nil
}

// SearchItem is one search result.
type SearchItem struct {
	URI      string `json:"uri"`
	Type     string `json:"type" enum:"playlist,album,track"`
	Name     string `json:"name"`
	Subtitle string `json:"subtitle,omitempty" doc:"The playlist's owner, or the album's or track's artists"`
	ImageURL string `json:"imageUrl,omitempty"`
	Tracks   int    `json:"tracks,omitempty"`
}

// SearchTypes are what search can find.
var SearchTypes = []string{"playlist", "album", "track"}

// Search finds playlists, albums or tracks (SearchLimit of each per page).
func (s *Spotify) Search(ctx context.Context, userID int64, q string, types []string, offset int) ([]SearchItem, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return []SearchItem{}, nil
	}
	if len(types) == 0 {
		types = SearchTypes
	}
	v := url.Values{"q": {q}, "type": {strings.Join(types, ",")}, "limit": {strconv.Itoa(SearchLimit)}, "offset": {strconv.Itoa(max(0, offset))}}
	type total struct {
		Total int `json:"total"`
	}
	var a struct {
		Playlists struct {
			Items []*struct {
				URI    string  `json:"uri"`
				Name   string  `json:"name"`
				Images []image `json:"images"`
				Owner  struct {
					DisplayName string `json:"display_name"`
				} `json:"owner"`
				Tracks *total `json:"tracks"`
				Items  *total `json:"items"`
			} `json:"items"`
		} `json:"playlists"`
		Albums struct {
			Items []*struct {
				URI     string  `json:"uri"`
				Name    string  `json:"name"`
				Images  []image `json:"images"`
				Artists []named `json:"artists"`
				Total   int     `json:"total_tracks"`
			} `json:"items"`
		} `json:"albums"`
		Tracks struct {
			Items []*struct {
				URI     string  `json:"uri"`
				Name    string  `json:"name"`
				Artists []named `json:"artists"`
				Album   struct {
					Images []image `json:"images"`
				} `json:"album"`
			} `json:"items"`
		} `json:"tracks"`
	}
	if err := s.as(ctx, userID, http.MethodGet, "/search?"+v.Encode(), nil, &a); err != nil {
		return nil, err
	}
	out := []SearchItem{}
	for _, p := range a.Playlists.Items {
		if p == nil || p.URI == "" {
			continue // Spotify leaves holes for playlists it hides
		}
		it := SearchItem{URI: p.URI, Type: "playlist", Name: p.Name, Subtitle: p.Owner.DisplayName, ImageURL: cover(p.Images)}
		if p.Items != nil {
			it.Tracks = p.Items.Total
		} else if p.Tracks != nil {
			it.Tracks = p.Tracks.Total
		}
		out = append(out, it)
	}
	for _, al := range a.Albums.Items {
		if al != nil && al.URI != "" {
			out = append(out, SearchItem{URI: al.URI, Type: "album", Name: al.Name, Subtitle: names(al.Artists), ImageURL: cover(al.Images), Tracks: al.Total})
		}
	}
	for _, t := range a.Tracks.Items {
		if t != nil && t.URI != "" {
			out = append(out, SearchItem{URI: t.URI, Type: "track", Name: t.Name, Subtitle: names(t.Artists), ImageURL: cover(t.Album.Images)})
		}
	}
	return out, nil
}
