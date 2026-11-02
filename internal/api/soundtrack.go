package api

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/Asion001/mangarr/internal/access"
	"github.com/Asion001/mangarr/internal/model"
	"github.com/Asion001/mangarr/internal/settings"
	"github.com/Asion001/mangarr/internal/soundtrack"
)

func init() { register((*Server).registerSoundtracks) }

// SpotifySettings is the admin's Spotify app and who connected to it.
type SpotifySettings struct {
	ClientID string `json:"clientId"`
	// RedirectURI goes in the Spotify app's settings.
	RedirectURI string `json:"redirectUri" readOnly:"true"`
	// Secure: the redirect is HTTPS (or a loopback address), which Spotify
	// requires.
	Secure bool `json:"secure" readOnly:"true"`
	// MaxUsers is how many people a development-mode Spotify app allows.
	MaxUsers int                    `json:"maxUsers" readOnly:"true"`
	Users    []SpotifyConnectedUser `json:"users" readOnly:"true"`
}

// SpotifyConnectedUser is someone who connected their Spotify account.
type SpotifyConnectedUser struct {
	UserID      int64     `json:"userId"`
	Username    string    `json:"username"`
	SpotifyName string    `json:"spotifyName"`
	Premium     bool      `json:"premium"`
	ConnectedAt time.Time `json:"connectedAt"`
}

// MySpotify is whether you can and did connect Spotify.
type MySpotify struct {
	// Available: an admin set up Spotify on this server.
	Available   bool       `json:"available"`
	Connected   bool       `json:"connected"`
	DisplayName string     `json:"displayName,omitempty"`
	Premium     bool       `json:"premium"`
	ConnectedAt *time.Time `json:"connectedAt,omitempty"`
}

// SpotifyToken is a short-lived token for the Web Playback SDK.
type SpotifyToken struct {
	AccessToken string    `json:"accessToken"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// SeriesSoundtrack is a series' shared soundtrack and your own version.
type SeriesSoundtrack struct {
	Shared   *soundtrack.Doc `json:"shared,omitempty" doc:"Set by admins; none: no music"`
	Personal *soundtrack.Doc `json:"personal,omitempty" doc:"Your own cues and mute; where they sit, they win over the shared ones"`
	// Credits are the pack sounds the soundtracks use, with their licences.
	Credits []soundtrack.Credit `json:"credits"`
	// Chapters are the series' chapter numbers with their page counts, for
	// placing cues.
	Chapters []SoundtrackChapter `json:"chapters"`
}

// SoundtrackChapter is a chapter cues can start in.
type SoundtrackChapter struct {
	ID     int64   `json:"id"`
	Number float64 `json:"number"`
	Label  string  `json:"label"`
	Pages  int     `json:"pages,omitempty"`
}

type moodBody struct {
	Name            string   `json:"name" minLength:"1" maxLength:"60"`
	Color           string   `json:"color" pattern:"^#[0-9a-fA-F]{6}$"`
	Icon            string   `json:"icon"`
	Genres          []string `json:"genres,omitempty"`
	SpotifyURI      string   `json:"spotifyUri,omitempty"`
	SpotifyName     string   `json:"spotifyName,omitempty"`
	SpotifyImageURL string   `json:"spotifyImageUrl,omitempty"`
	Shuffle         bool     `json:"shuffle" required:"false"`
}

func (b moodBody) mood() *model.AudioMood {
	return &model.AudioMood{Name: b.Name, Color: b.Color, Icon: b.Icon, Genres: b.Genres, SpotifyURI: b.SpotifyURI, SpotifyName: b.SpotifyName, SpotifyImageURL: b.SpotifyImageURL, Shuffle: b.Shuffle}
}

type deviceBody struct {
	Body struct {
		DeviceID string `json:"deviceId,omitempty"`
	}
}

func soundtrackError(err error) error {
	var ae *soundtrack.APIError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, soundtrack.ErrMoodNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, soundtrack.ErrInvalid):
		return huma.Error400BadRequest(err.Error())
	case errors.Is(err, soundtrack.ErrNotConfigured), errors.Is(err, soundtrack.ErrNotConnected), errors.Is(err, soundtrack.ErrNoDevice), errors.Is(err, soundtrack.ErrNoVolume):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, soundtrack.ErrPremium):
		return huma.Error403Forbidden(err.Error())
	case errors.As(err, &ae):
		return huma.Error502BadGateway(err.Error())
	}
	return toHTTPError(err)
}

// spotifyRedirectURL is where Spotify sends readers back: the public URL
// when it's set, else the address the request came to.
func (s *Server) spotifyRedirectURL(ctx context.Context) string {
	base := ""
	if g, err := s.app.Settings.General(ctx); err == nil {
		base = strings.TrimRight(g.PublicURL, "/")
	}
	if base == "" {
		c := access.ClientFrom(ctx)
		if c.Host == "" {
			return ""
		}
		scheme := "http"
		if c.Secure {
			scheme = "https"
		}
		base = scheme + "://" + c.Host
	}
	return base + s.app.Cfg.URLBase + "/api/v1/me/spotify/callback"
}

// secureRedirect reports whether Spotify accepts a redirect: HTTPS, or
// plain HTTP to a loopback address.
func secureRedirect(redirect string) bool {
	u, err := url.Parse(redirect)
	if err != nil || u.Host == "" {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && ip != nil && ip.IsLoopback()
}

// safeReturn keeps a sign-in's way back inside mangarr.
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\\r\n") {
		return "/account"
	}
	return p
}

func (s *Server) spotifySettings(ctx context.Context) (*SpotifySettings, error) {
	c, err := s.app.Settings.Spotify(ctx)
	if err != nil {
		return nil, err
	}
	redirect := s.spotifyRedirectURL(ctx)
	out := &SpotifySettings{ClientID: c.ClientID, RedirectURI: redirect, Secure: secureRedirect(redirect), MaxUsers: soundtrack.DevModeUsers, Users: []SpotifyConnectedUser{}}
	accounts, err := s.app.Spotify.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		var u model.User
		_ = s.app.DB.NewSelect().Model(&u).Column("username").Where("id = ?", a.UserID).Scan(ctx)
		out.Users = append(out.Users, SpotifyConnectedUser{UserID: a.UserID, Username: u.Username, SpotifyName: a.DisplayName, Premium: a.Product == "premium", ConnectedAt: a.ConnectedAt})
	}
	return out, nil
}

func (s *Server) mySpotify(ctx context.Context, userID int64) (*MySpotify, error) {
	out := &MySpotify{Available: s.app.Spotify.ClientID(ctx) != ""}
	acc, err := s.app.Spotify.Account(ctx, userID)
	if err != nil || acc == nil {
		return out, err
	}
	at := acc.ConnectedAt
	out.Connected, out.DisplayName, out.Premium, out.ConnectedAt = true, acc.DisplayName, acc.Product == "premium", &at
	return out, nil
}

// spotifyBack returns to the page the sign-in started from.
func (s *Server) spotifyBack(returnTo string, err error) *redirectOutput {
	u, _ := url.Parse(safeReturn(returnTo))
	q := u.Query()
	if err != nil {
		q.Set("spotify_error", err.Error())
	} else {
		q.Set("spotify", "connected")
	}
	u.RawQuery = q.Encode()
	return &redirectOutput{Status: http.StatusSeeOther, Location: s.app.Cfg.URLBase + u.String()}
}

// chapterSeries is a chapter the caller may see and its series.
func (s *Server) chapterSeries(ctx context.Context, chapterID int64) (*model.Chapter, *model.Series, error) {
	var ch model.Chapter
	if err := s.app.DB.NewSelect().Model(&ch).Where("id = ?", chapterID).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, huma.Error404NotFound("chapter not found")
		}
		return nil, nil, toHTTPError(err)
	}
	ser, err := s.visibleSeries(ctx, ch.SeriesID)
	if err != nil {
		return nil, nil, huma.Error404NotFound("chapter not found")
	}
	return &ch, ser, nil
}

func (s *Server) seriesSoundtrack(ctx context.Context, seriesID int64) (*SeriesSoundtrack, error) {
	if _, err := s.visibleSeries(ctx, seriesID); err != nil {
		return nil, err
	}
	out := &SeriesSoundtrack{Chapters: []SoundtrackChapter{}}
	var err error
	if out.Shared, err = soundtrack.Get(ctx, s.app.DB, seriesID, nil); err != nil {
		return nil, toHTTPError(err)
	}
	if p := access.From(ctx); p != nil && p.Kind == access.KindUser {
		if out.Personal, err = soundtrack.Get(ctx, s.app.DB, seriesID, &p.UserID); err != nil {
			return nil, toHTTPError(err)
		}
	}
	counts, err := soundtrack.PageCounts(ctx, s.app.DB, seriesID)
	if err != nil {
		return nil, toHTTPError(err)
	}
	for _, d := range []*soundtrack.Doc{out.Shared, out.Personal} {
		if d != nil {
			d.Flag(counts)
		}
	}
	if out.Credits, err = soundtrack.Credits(ctx, s.app.DB, out.Shared, out.Personal); err != nil {
		return nil, toHTTPError(err)
	}
	var chapters []model.Chapter
	if err := s.app.DB.NewSelect().Model(&chapters).Column("id", "number_key", "number_sort").Where("series_id = ?", seriesID).Order("number_sort").Scan(ctx); err != nil {
		return nil, toHTTPError(err)
	}
	for _, c := range chapters {
		out.Chapters = append(out.Chapters, SoundtrackChapter{ID: c.ID, Number: c.NumberSort, Label: c.NumberKey, Pages: counts[c.NumberSort]})
	}
	return out, nil
}

// genresOf are a series' genres and tags, and its title's.
func (s *Server) genresOf(ctx context.Context, ser *model.Series) []string {
	out := append(append([]string{}, ser.Metadata.Genres...), ser.Metadata.Tags...)
	if ser.WorkID > 0 {
		var w model.Work
		if err := s.app.DB.NewSelect().Model(&w).Where("id = ?", ser.WorkID).Scan(ctx); err == nil {
			out = append(append(out, w.Metadata.Genres...), w.Metadata.Tags...)
		}
	}
	return out
}

func (s *Server) registerSoundtracks() {
	tags := []string{"Soundtracks"}

	huma.Register(s.api, huma.Operation{OperationID: "settings-get-spotify", Method: http.MethodGet, Path: "/api/v1/settings/spotify", Tags: []string{"Settings"},
		Summary: "The Spotify app soundtracks play through, and who connected"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *SpotifySettings }, error) {
			out, err := s.spotifySettings(ctx)
			return &struct{ Body *SpotifySettings }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "settings-put-spotify", Method: http.MethodPut, Path: "/api/v1/settings/spotify", Tags: []string{"Settings"},
		Summary: "Set the Spotify app's Client ID"},
		func(ctx context.Context, in *struct {
			Body struct {
				ClientID string `json:"clientId" maxLength:"64"`
			}
		}) (*struct{ Body *SpotifySettings }, error) {
			id := strings.TrimSpace(in.Body.ClientID)
			for _, r := range id {
				if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
					return nil, huma.Error400BadRequest("a Spotify Client ID is letters and digits only")
				}
			}
			if err := s.app.Settings.Set(ctx, settings.KeySpotify, settings.Spotify{ClientID: id}); err != nil {
				return nil, toHTTPError(err)
			}
			s.app.Bus.Changed("settings", "updated", 0)
			out, err := s.spotifySettings(ctx)
			return &struct{ Body *SpotifySettings }{out}, toHTTPError(err)
		})

	// one's own Spotify account
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify", Method: http.MethodGet, Path: "/api/v1/me/spotify", Tags: tags,
		Summary: "Whether Spotify is set up here and your account connected"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *MySpotify }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			out, err := s.mySpotify(ctx, p.UserID)
			return &struct{ Body *MySpotify }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-connect", Method: http.MethodPost, Path: "/api/v1/me/spotify/connect", Tags: tags,
		Summary: "Start connecting your Spotify account: open the returned link"},
		func(ctx context.Context, in *struct {
			Body struct {
				ReturnTo string `json:"returnTo,omitempty" doc:"The mangarr page to come back to"`
			}
		}) (*struct {
			Body struct {
				URL string `json:"url"`
			}
		}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			redirect := s.spotifyRedirectURL(ctx)
			if !secureRedirect(redirect) {
				return nil, huma.Error409Conflict("Spotify sign-in needs HTTPS: open mangarr over HTTPS (or set its public URL to an https address)")
			}
			u, err := s.app.Spotify.Start(ctx, p.UserID, redirect, safeReturn(in.Body.ReturnTo))
			if err != nil {
				return nil, soundtrackError(err)
			}
			out := &struct {
				Body struct {
					URL string `json:"url"`
				}
			}{}
			out.Body.URL = u
			return out, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-callback", Method: http.MethodGet, Path: "/api/v1/me/spotify/callback", Tags: tags,
		Security: []map[string][]string{}, Summary: "Where Spotify sends people back after they sign in"},
		func(ctx context.Context, in *struct {
			Code  string `query:"code"`
			State string `query:"state"`
			Error string `query:"error"`
		}) (*redirectOutput, error) {
			back := s.app.Spotify.ReturnTo(in.State)
			if in.Error != "" || in.Code == "" || in.State == "" {
				msg := "Spotify didn't connect the account"
				if in.Error == "access_denied" {
					msg = "you didn't allow mangarr on Spotify"
				}
				return s.spotifyBack(back, errors.New(msg)), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			if _, err := s.app.Spotify.Finish(ctx, in.State, in.Code); err != nil {
				s.app.Log.Info("spotify connect failed", "err", err)
				return s.spotifyBack(back, err), nil
			}
			return s.spotifyBack(back, nil), nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-disconnect", Method: http.MethodDelete, Path: "/api/v1/me/spotify", Tags: tags,
		Summary: "Disconnect your Spotify account", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, _ *struct{}) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, toHTTPError(s.app.Spotify.Disconnect(ctx, p.UserID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-token", Method: http.MethodGet, Path: "/api/v1/me/spotify/token", Tags: tags,
		Summary: "A short-lived Spotify token for playing in this browser"},
		func(ctx context.Context, _ *struct{}) (*struct {
			CacheControl string `header:"Cache-Control"`
			Body         SpotifyToken
		}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			tok, exp, err := s.app.Spotify.Token(ctx, p.UserID)
			if err != nil {
				return nil, soundtrackError(err)
			}
			return &struct {
				CacheControl string `header:"Cache-Control"`
				Body         SpotifyToken
			}{"no-store", SpotifyToken{AccessToken: tok, ExpiresAt: exp}}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-devices", Method: http.MethodGet, Path: "/api/v1/me/spotify/devices", Tags: tags,
		Summary: "Your devices Spotify can play on"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []soundtrack.Device }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			out, err := s.app.Spotify.Devices(ctx, p.UserID)
			return &struct{ Body []soundtrack.Device }{out}, soundtrackError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-player", Method: http.MethodGet, Path: "/api/v1/me/spotify/player", Tags: tags,
		Summary: "What plays on your Spotify"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body *soundtrack.NowPlaying }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			out, err := s.app.Spotify.Player(ctx, p.UserID)
			return &struct{ Body *soundtrack.NowPlaying }{out}, soundtrackError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-play", Method: http.MethodPut, Path: "/api/v1/me/spotify/play", Tags: tags,
		Summary: "Play a playlist, album or track on one of your devices"},
		func(ctx context.Context, in *struct{ Body soundtrack.Play }) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, soundtrackError(s.app.Spotify.Play(ctx, p.UserID, in.Body))
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-pause", Method: http.MethodPut, Path: "/api/v1/me/spotify/pause", Tags: tags,
		Summary: "Pause your Spotify"},
		func(ctx context.Context, in *deviceBody) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, soundtrackError(s.app.Spotify.Pause(ctx, p.UserID, in.Body.DeviceID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-next", Method: http.MethodPost, Path: "/api/v1/me/spotify/next", Tags: tags,
		Summary: "Skip to the next track"},
		func(ctx context.Context, in *deviceBody) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, soundtrackError(s.app.Spotify.Next(ctx, p.UserID, in.Body.DeviceID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-resume", Method: http.MethodPut, Path: "/api/v1/me/spotify/resume", Tags: tags,
		Summary: "Resume your Spotify where it was"},
		func(ctx context.Context, in *deviceBody) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, soundtrackError(s.app.Spotify.Resume(ctx, p.UserID, in.Body.DeviceID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-spotify-volume", Method: http.MethodPut, Path: "/api/v1/me/spotify/volume", Tags: tags,
		Summary: "Set your Spotify's volume"},
		func(ctx context.Context, in *struct {
			Body struct {
				Volume   int    `json:"volume" minimum:"0" maximum:"100"`
				DeviceID string `json:"deviceId,omitempty"`
			}
		}) (*struct{}, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			return nil, soundtrackError(s.app.Spotify.Volume(ctx, p.UserID, in.Body.Volume, in.Body.DeviceID))
		})
	huma.Register(s.api, huma.Operation{OperationID: "spotify-search", Method: http.MethodGet, Path: "/api/v1/spotify/search", Tags: tags,
		Summary: "Search Spotify for playlists, albums and tracks (with your account)"},
		func(ctx context.Context, in *struct {
			Q      string   `query:"q" maxLength:"200"`
			Type   []string `query:"type" doc:"playlist, album, track (default all)"`
			Offset int      `query:"offset" minimum:"0" maximum:"1000"`
		}) (*struct{ Body []soundtrack.SearchItem }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			types := splitList(in.Type)
			for _, t := range types {
				if t != "playlist" && t != "album" && t != "track" {
					return nil, huma.Error400BadRequest("search type is playlist, album or track")
				}
			}
			out, err := s.app.Spotify.Search(ctx, p.UserID, in.Q, types, in.Offset)
			return &struct{ Body []soundtrack.SearchItem }{out}, soundtrackError(err)
		})

	// moods
	huma.Register(s.api, huma.Operation{OperationID: "audio-moods", Method: http.MethodGet, Path: "/api/v1/audio/moods", Tags: tags,
		Summary: "The mood presets"},
		func(ctx context.Context, _ *struct{}) (*struct{ Body []model.AudioMood }, error) {
			out, err := soundtrack.Moods(ctx, s.app.DB, s.app.Settings)
			return &struct{ Body []model.AudioMood }{out}, toHTTPError(err)
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-mood-create", Method: http.MethodPost, Path: "/api/v1/audio/moods", Tags: tags,
		Summary: "Add a mood"},
		func(ctx context.Context, in *struct{ Body moodBody }) (*struct{ Body *model.AudioMood }, error) {
			if err := soundtrack.EnsureMoods(ctx, s.app.DB, s.app.Settings); err != nil {
				return nil, toHTTPError(err)
			}
			m := in.Body.mood()
			if err := soundtrack.CreateMood(ctx, s.app.DB, m); err != nil {
				return nil, soundtrackError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", 0)
			return &struct{ Body *model.AudioMood }{m}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-mood-update", Method: http.MethodPut, Path: "/api/v1/audio/moods/{id}", Tags: tags,
		Summary: "Change a mood"},
		func(ctx context.Context, in *struct {
			IDPath
			Body moodBody
		}) (*struct{ Body *model.AudioMood }, error) {
			m := in.Body.mood()
			if err := soundtrack.UpdateMood(ctx, s.app.DB, in.ID, m); err != nil {
				return nil, soundtrackError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", 0)
			return &struct{ Body *model.AudioMood }{m}, nil
		})
	huma.Register(s.api, huma.Operation{OperationID: "audio-mood-delete", Method: http.MethodDelete, Path: "/api/v1/audio/moods/{id}", Tags: tags,
		Summary: "Delete a mood (soundtracks that used it play nothing there)", DefaultStatus: http.StatusNoContent},
		func(ctx context.Context, in *IDPath) (*struct{}, error) {
			if err := soundtrack.DeleteMood(ctx, s.app.DB, in.ID); err != nil {
				return nil, soundtrackError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", 0)
			return nil, nil
		})

	// a series' soundtrack
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack", Method: http.MethodGet, Path: "/api/v1/series/{id}/soundtrack", Tags: tags,
		Summary: "A series' shared soundtrack and your own version of it"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *SeriesSoundtrack }, error) {
			out, err := s.seriesSoundtrack(ctx, in.ID)
			return &struct{ Body *SeriesSoundtrack }{out}, err
		})
	huma.Register(s.api, huma.Operation{OperationID: "series-soundtrack-save", Method: http.MethodPut, Path: "/api/v1/series/{id}/soundtrack", Tags: tags,
		Summary: "Set a series' shared soundtrack (what everyone hears unless they made their own)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body soundtrack.Doc
		}) (*struct{ Body *SeriesSoundtrack }, error) {
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			if err := soundtrack.Save(ctx, s.app.DB, in.ID, nil, in.Body); err != nil {
				return nil, soundtrackError(err)
			}
			s.app.Bus.Changed("soundtrack", "updated", in.ID)
			out, err := s.seriesSoundtrack(ctx, in.ID)
			return &struct{ Body *SeriesSoundtrack }{out}, err
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-series-soundtrack-save", Method: http.MethodPut, Path: "/api/v1/me/series/{id}/soundtrack", Tags: tags,
		Summary: "Set your own version of a series' soundtrack (or mute it)"},
		func(ctx context.Context, in *struct {
			IDPath
			Body soundtrack.Doc
		}) (*struct{ Body *SeriesSoundtrack }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			if err := soundtrack.Save(ctx, s.app.DB, in.ID, &p.UserID, in.Body); err != nil {
				return nil, soundtrackError(err)
			}
			out, err := s.seriesSoundtrack(ctx, in.ID)
			return &struct{ Body *SeriesSoundtrack }{out}, err
		})
	huma.Register(s.api, huma.Operation{OperationID: "me-series-soundtrack-reset", Method: http.MethodDelete, Path: "/api/v1/me/series/{id}/soundtrack", Tags: tags,
		Summary: "Drop your own version: hear the shared soundtrack again"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *SeriesSoundtrack }, error) {
			p, err := me(ctx)
			if err != nil {
				return nil, err
			}
			if _, err := s.visibleSeries(ctx, in.ID); err != nil {
				return nil, err
			}
			if err := soundtrack.Reset(ctx, s.app.DB, in.ID, p.UserID); err != nil {
				return nil, toHTTPError(err)
			}
			out, err := s.seriesSoundtrack(ctx, in.ID)
			return &struct{ Body *SeriesSoundtrack }{out}, err
		})
	huma.Register(s.api, huma.Operation{OperationID: "read-chapter-soundtrack", Method: http.MethodGet, Path: "/api/v1/read/chapters/{id}/soundtrack", Tags: []string{"Reader"},
		Summary: "What plays in a chapter for you (your version, else the shared one)"},
		func(ctx context.Context, in *IDPath) (*struct{ Body *soundtrack.Resolved }, error) {
			rid, err := s.userOf(ctx)
			if err != nil {
				return nil, toHTTPError(err)
			}
			ch, ser, err := s.chapterSeries(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			out, err := soundtrack.Resolve(ctx, s.app.DB, s.app.Settings, ser.ID, rid, s.genresOf(ctx, ser), ch.NumberSort)
			return &struct{ Body *soundtrack.Resolved }{out}, toHTTPError(err)
		})
}
