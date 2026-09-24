// Package streamhttp is LeanProxy's own MCP Streamable HTTP front end
// (issue #309): one shared local gateway that any number of MCP clients
// reach by URL, instead of every IDE spawning its own `server run --stdio`
// process with its own child servers.
//
// It speaks the Streamable HTTP transport of the MCP revisions LeanProxy
// negotiates (2025-03-26, 2025-06-18, 2025-11-25) on a single endpoint:
//
//   - POST carries one client message (or, for 2025-03-26 sessions, a
//     batch). Requests are answered with application/json, or with a
//     text/event-stream when server-to-client messages (progress,
//     elicitation, ...) must go out before the answer; notifications and
//     responses get 202 Accepted.
//   - GET opens the session's server-initiated SSE stream (list changes,
//     resource updates, and server-to-client requests with no request of
//     their own).
//   - DELETE ends the session.
//
// Every message goes through the same *mcp.Handler — and so the same
// middleware chain (redaction, injection guard, response cache, tool
// pinning, per-tool policy, telemetry) — as the stdio front end. Each
// Mcp-Session-Id maps to one mcp.ClientSession: its negotiated protocol
// version, declared capabilities and server-to-client requests.
//
// Security (see Options and docs/security.md): loopback bind by default
// and a refusal to bind elsewhere without a token; Host and Origin
// validation against DNS rebinding and cross-site requests (pkg/httpsec);
// bearer-token auth compared in constant time; unguessable session ids
// bound to the credential that created them, capped in number and expired
// when idle; request body, header, read and write limits; and a cap on the
// requests handled at once.
//
// Resumability (Last-Event-ID) is not supported: events carry no id and a
// reconnecting GET stream starts afresh.
package streamhttp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

// Transport headers.
const (
	HeaderSessionID       = "Mcp-Session-Id"
	HeaderProtocolVersion = "Mcp-Protocol-Version"
	headerLastEventID     = "Last-Event-Id"
)

// Defaults for the zero values of Options.
const (
	DefaultEndpoint           = "/mcp"
	DefaultMaxBodyBytes       = 64 << 20
	DefaultMaxSessions        = 64
	DefaultSessionIdleTimeout = 30 * time.Minute
	DefaultMaxConcurrent      = 64
	DefaultReadHeaderTimeout  = 10 * time.Second
	DefaultReadTimeout        = 60 * time.Second
	DefaultWriteTimeout       = 30 * time.Second
	DefaultIdleTimeout        = 120 * time.Second
	DefaultHeartbeat          = 25 * time.Second
	// maxHeaderBytes bounds the request headers (net/http's default is
	// 1 MiB; MCP requests carry a handful of short headers).
	maxHeaderBytes = 64 << 10
)

// Handler is what the front end needs from *mcp.Handler.
type Handler interface {
	HandleRequest(ctx context.Context, req *mcp.Request) (*mcp.Response, error)
	OpenSession(notify mcp.NotifyFunc) (*mcp.ClientSession, func())
}

// Options configures a Server. Zero values mean the defaults above.
type Options struct {
	// Addr is the "host:port" to listen on. Required.
	Addr string
	// Endpoint is the MCP endpoint path (DefaultEndpoint).
	Endpoint string
	// Token is the bearer token every request must present. Empty
	// disables authentication, which New only accepts on a loopback Addr.
	Token string
	// AllowedHosts are Host header values accepted beyond the bind host
	// and the loopback names (see httpsec.AllowedHosts).
	AllowedHosts []string
	// AllowedOrigins are the browser origins ("https://app.example")
	// allowed beyond the server's own. Requests without an Origin header
	// (every non-browser client) are not affected.
	AllowedOrigins []string
	// MaxBodyBytes caps one POST body.
	MaxBodyBytes int64
	// MaxSessions caps the sessions open at once; initialize beyond it
	// gets 503 until one ends or expires.
	MaxSessions int
	// SessionIdleTimeout ends a session with no request in flight and no
	// GET stream open for this long.
	SessionIdleTimeout time.Duration
	// MaxConcurrent caps the client requests handled at once, across
	// sessions; more wait for a slot.
	MaxConcurrent int
	// ReadHeaderTimeout, ReadTimeout (the whole POST body), WriteTimeout
	// (each write) and IdleTimeout (keep-alive) bound slow clients.
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	// Heartbeat is the interval of the SSE keep-alive comments on a GET
	// stream.
	Heartbeat time.Duration
	Logger    *slog.Logger
}

func (o *Options) setDefaults() {
	if o.Endpoint == "" {
		o.Endpoint = DefaultEndpoint
	}
	if o.MaxBodyBytes <= 0 {
		o.MaxBodyBytes = DefaultMaxBodyBytes
	}
	if o.MaxSessions <= 0 {
		o.MaxSessions = DefaultMaxSessions
	}
	if o.SessionIdleTimeout <= 0 {
		o.SessionIdleTimeout = DefaultSessionIdleTimeout
	}
	if o.MaxConcurrent <= 0 {
		o.MaxConcurrent = DefaultMaxConcurrent
	}
	if o.ReadHeaderTimeout <= 0 {
		o.ReadHeaderTimeout = DefaultReadHeaderTimeout
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = DefaultReadTimeout
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = DefaultWriteTimeout
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.Heartbeat <= 0 {
		o.Heartbeat = DefaultHeartbeat
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
}

// Server is a running Streamable HTTP front end.
type Server struct {
	h              Handler
	opts           Options
	logger         *slog.Logger
	allowedOrigins map[string]struct{}
	sem            chan struct{}

	ln      net.Listener
	httpSrv *http.Server

	mu       sync.Mutex
	sessions map[string]*session
	closing  bool

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// ErrUnauthenticatedExposure is returned by New for a non-loopback Addr
// without a Token.
var ErrUnauthenticatedExposure = errors.New("refusing to serve MCP over HTTP on a non-loopback address without a bearer token")

// CheckOptions validates the exposure settings of opts: a listen address,
// and a token unless it binds a loopback interface only.
func CheckOptions(opts Options) error {
	if opts.Addr == "" {
		return errors.New("streamhttp: listen address is required")
	}
	if _, _, err := net.SplitHostPort(opts.Addr); err != nil {
		return fmt.Errorf("streamhttp: invalid listen address %q: %w", opts.Addr, err)
	}
	if opts.Token == "" && !httpsec.IsLoopbackBindAddr(opts.Addr) {
		return fmt.Errorf("%w (%s)", ErrUnauthenticatedExposure, opts.Addr)
	}
	return nil
}

// New validates opts and returns a Server that is not listening yet.
func New(h Handler, opts Options) (*Server, error) {
	if h == nil {
		return nil, errors.New("streamhttp: nil handler")
	}
	if err := CheckOptions(opts); err != nil {
		return nil, err
	}
	opts.setDefaults()
	return &Server{
		h:              h,
		opts:           opts,
		logger:         opts.Logger,
		allowedOrigins: httpsec.OriginSet(opts.AllowedOrigins),
		sem:            make(chan struct{}, opts.MaxConcurrent),
		sessions:       make(map[string]*session),
		stop:           make(chan struct{}),
	}, nil
}

// Start listens on Addr and serves in the background.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.opts.Addr)
	if err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(s.opts.Addr)
	if err != nil {
		_ = ln.Close()
		return err
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		_ = ln.Close()
		return err
	}
	s.ln = ln
	s.httpSrv = &http.Server{
		Handler:           s.routes(httpsec.AllowedHosts(host, port, s.opts.AllowedHosts)),
		ReadHeaderTimeout: s.opts.ReadHeaderTimeout,
		IdleTimeout:       s.opts.IdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
		// ReadTimeout and WriteTimeout are per request here (the POST
		// body, each SSE write): server-wide values would cut long
		// calls and GET streams.
		ErrorLog: slog.NewLogLogger(s.logger.Handler(), slog.LevelDebug),
	}
	s.wg.Add(2)
	go func() {
		defer s.wg.Done()
		if err := s.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("streamable HTTP front end stopped", "error", err)
		}
	}()
	go func() {
		defer s.wg.Done()
		s.sweep()
	}()
	return nil
}

// Addr returns the address the server listens on ("host:port").
func (s *Server) Addr() string {
	if s.ln == nil {
		return s.opts.Addr
	}
	return s.ln.Addr().String()
}

// URL returns the MCP endpoint URL.
func (s *Server) URL() string {
	return "http://" + s.Addr() + s.opts.Endpoint
}

// Authenticated reports whether the server requires a bearer token.
func (s *Server) Authenticated() bool { return s.opts.Token != "" }

// SessionCount returns the number of open sessions.
func (s *Server) SessionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

// Shutdown ends every session (canceling their requests and closing their
// streams), then stops the HTTP server, waiting at most until ctx ends.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closing = true
	all := make([]*session, 0, len(s.sessions))
	for id, sess := range s.sessions {
		all = append(all, sess)
		delete(s.sessions, id)
	}
	s.mu.Unlock()
	for _, sess := range all {
		sess.shutdown()
	}
	s.stopOnce.Do(func() { close(s.stop) })
	var err error
	if s.httpSrv != nil {
		if err = s.httpSrv.Shutdown(ctx); err != nil {
			_ = s.httpSrv.Close()
		}
	}
	s.wg.Wait()
	return err
}

// sweep expires idle sessions until the server stops.
func (s *Server) sweep() {
	interval := min(s.opts.SessionIdleTimeout/4, time.Minute)
	interval = max(interval, 10*time.Millisecond)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case now := <-t.C:
			s.expireIdle(now)
		}
	}
}

// expireIdle ends the sessions idle for longer than SessionIdleTimeout.
func (s *Server) expireIdle(now time.Time) int {
	s.mu.Lock()
	var expired []*session
	for id, sess := range s.sessions {
		if sess.expired(now, s.opts.SessionIdleTimeout) {
			expired = append(expired, sess)
			delete(s.sessions, id)
		}
	}
	s.mu.Unlock()
	for _, sess := range expired {
		s.logger.Info("streamable HTTP session expired", "idle_timeout", s.opts.SessionIdleTimeout)
		sess.shutdown()
	}
	return len(expired)
}

// errTooManySessions is returned when MaxSessions sessions are open.
var errTooManySessions = errors.New("too many open sessions")

// newSession opens a session for principal.
func (s *Server) newSession(principal [32]byte) (*session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	if !s.hasRoom() && s.expireIdle(time.Now()) == 0 {
		// Room is made from idle sessions only before refusing.
		return nil, errTooManySessions
	}
	sess := &session{
		id:         id,
		principal:  principal,
		srv:        s,
		done:       make(chan struct{}),
		lastActive: time.Now(),
		progress:   make(map[string]*stream),
		sentOn:     make(map[string]*stream),
		inflight:   make(map[string]*inflight),
	}
	sess.cs, sess.closeCS = s.h.OpenSession(sess.notify)
	sess.cs.EnableContextRequests(sess.sendRequest)

	s.mu.Lock()
	switch {
	case s.closing:
		err = errors.New("server is shutting down")
	case len(s.sessions) >= s.opts.MaxSessions:
		err = errTooManySessions
	default:
		s.sessions[id] = sess
	}
	s.mu.Unlock()
	if err != nil {
		sess.shutdown()
		return nil, err
	}
	return sess, nil
}

func (s *Server) hasRoom() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions) < s.opts.MaxSessions
}

// lookup returns the open session id created by principal, or nil. A
// session of another principal is reported as unknown.
func (s *Server) lookup(id string, principal [32]byte) *session {
	if id == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[id]
	if sess == nil || sess.principal != principal {
		return nil
	}
	return sess
}

// remove ends a session.
func (s *Server) remove(sess *session) {
	s.mu.Lock()
	if s.sessions[sess.id] == sess {
		delete(s.sessions, sess.id)
	}
	s.mu.Unlock()
	sess.shutdown()
}

// principalOf fingerprints the credential of an authenticated request.
// Every client of a single-token server shares a principal; the binding
// still keeps a session from outliving a token change.
func (s *Server) principalOf(r *http.Request) [32]byte {
	if s.opts.Token == "" {
		return [32]byte{}
	}
	tok, _ := httpsec.BearerToken(r)
	return sha256.Sum256([]byte(tok))
}
