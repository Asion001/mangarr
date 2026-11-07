// Package komgaapi serves a Komga-compatible REST API so reading apps that
// speak Komga (Mihon's Komga extension and tracker, KMReader, Paperback) can
// browse and read mangarr's whole library, including chapters that aren't
// downloaded yet, with read progress synced back.
//
// It runs on its own port with routes at the root, like a real Komga (and
// like silo's compatibility APIs), and only while enabled in the settings.
package komgaapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"github.com/Asion001/mangarr/internal/auth"
	"github.com/Asion001/mangarr/internal/db"
	"github.com/Asion001/mangarr/internal/events"
	"github.com/Asion001/mangarr/internal/reading"
	"github.com/Asion001/mangarr/internal/settings"
)

// DefaultListen is Komga's own port.
const DefaultListen = ":25600"

// Deps are the services the API reads from.
type Deps struct {
	DB       *db.DB
	Settings *settings.Store
	Auth     *auth.Service
	Reading  *reading.Service
	Bus      *events.Bus
	Log      *slog.Logger
	// WebListen and URLBase locate the main web UI when General.PublicURL is unset.
	WebListen string
	URLBase   string
	// Maintenance (optional) reports that the database is moving: writes
	// answer 503 meanwhile.
	Maintenance func() bool
	// TrustedProxies are believed about the client's address (nil: the
	// clientip defaults).
	TrustedProxies []netip.Prefix
}

// Service owns the listener: it runs while the API is enabled.
type Service struct {
	deps    Deps
	addr    string
	handler http.Handler

	keys  keys
	basic basicCache
	users userCache

	mu      sync.Mutex
	srv     *http.Server
	boundTo string
	lastErr string
	// cancel ends the listener's requests (SSE streams) on stop
	cancel context.CancelFunc
}

// NewService builds the API; it starts listening once enabled (Start, Reconcile).
func NewService(deps Deps, addr string) *Service {
	if addr == "" {
		addr = DefaultListen
	}
	s := &Service{deps: deps, addr: addr}
	s.handler = s.router()
	return s
}

// Handler serves the API (for tests and in-process use).
func (s *Service) Handler() http.Handler { return s.handler }

// Status describes the listener.
type Status struct {
	Enabled   bool   `json:"enabled"`
	Listening bool   `json:"listening"`
	Address   string `json:"address"`
	Error     string `json:"error,omitempty"`
}

// Status reports whether the API is listening and where.
func (s *Service) Status(ctx context.Context) Status {
	r, _ := s.deps.Settings.Reading(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Enabled: r.Enabled, Listening: s.srv != nil, Address: s.addr, Error: s.lastErr}
	if s.boundTo != "" {
		st.Address = s.boundTo
	}
	return st
}

// Start begins serving if enabled; it stops when ctx ends.
func (s *Service) Start(ctx context.Context) error {
	s.Reconcile(ctx)
	go func() {
		<-ctx.Done()
		s.stop()
	}()
	return nil
}

// Reconcile starts or stops the listener to match the settings.
func (s *Service) Reconcile(ctx context.Context) {
	r, err := s.deps.Settings.Reading(ctx)
	if err != nil {
		return
	}
	if !r.Enabled {
		s.stop()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.srv != nil {
		return
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		s.lastErr = err.Error()
		s.deps.Log.Error("Komga-compatible API can't listen", "addr", s.addr, "err", err)
		return
	}
	base, cancel := context.WithCancel(context.Background())
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true) // reverse proxies multiplex page requests
	srv := &http.Server{Handler: s.handler, Protocols: &protocols, ReadHeaderTimeout: 15 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	s.srv, s.boundTo, s.lastErr, s.cancel = srv, ln.Addr().String(), "", cancel
	s.deps.Log.Info("Komga-compatible API listening", "addr", s.boundTo)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.mu.Lock()
			s.lastErr = err.Error()
			if s.srv == srv {
				s.srv, s.boundTo = nil, ""
			}
			s.mu.Unlock()
		}
	}()
}

func (s *Service) stop() {
	s.mu.Lock()
	srv, cancel := s.srv, s.cancel
	s.srv, s.boundTo, s.cancel = nil, "", nil
	s.mu.Unlock()
	if srv == nil {
		return
	}
	cancel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	s.deps.Log.Info("Komga-compatible API stopped")
}
