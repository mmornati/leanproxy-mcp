package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/streamhttp"
)

// httpShutdownGrace bounds how long the HTTP front end waits for requests
// in flight when it stops.
const httpShutdownGrace = 5 * time.Second

// checkRunModeFlags validates the front-end flags of `server run`: exactly
// one of --stdio and --http, and the HTTP-only flags only with --http.
func checkRunModeFlags(stdio bool, httpAddr, httpToken string, noAuth, httpLists bool) error {
	switch {
	case stdio && httpAddr != "":
		return errors.New("--stdio and --http are mutually exclusive: run one front end per process")
	case !stdio && httpAddr == "":
		return errors.New("either --stdio or --http <host:port> is required")
	case stdio && (httpToken != "" || noAuth || httpLists):
		return errors.New("--http-token, --no-auth, --http-allowed-hosts and --http-allowed-origins only apply to --http")
	}
	return nil
}

// checkIdleExit validates --idle-exit: never negative, and only with --http
// (a stdio proxy already ends with its client).
func checkIdleExit(idle time.Duration, httpAddr string) error {
	switch {
	case idle < 0:
		return fmt.Errorf("--idle-exit must not be negative, got %s", idle)
	case idle > 0 && httpAddr == "":
		return errors.New("--idle-exit only applies to --http")
	}
	return nil
}

// idleCheckInterval is how often the gateway looks at its session count
// when --idle-exit is set.
func idleCheckInterval(idle time.Duration) time.Duration {
	return max(min(idle/10, 5*time.Second), 10*time.Millisecond)
}

// waitIdle returns true once count has reported no client for a whole idle
// period, and false when ctx ends first. A client that holds its session
// open (the stream `connect` keeps for server-initiated messages) keeps
// the gateway up for as long as it runs.
func waitIdle(ctx context.Context, count func() int, idle, interval time.Duration) bool {
	t := time.NewTicker(interval)
	defer t.Stop()
	since := time.Now()
	for {
		select {
		case <-ctx.Done():
			return false
		case now := <-t.C:
			if count() > 0 {
				since = now
				continue
			}
			if now.Sub(since) >= idle {
				return true
			}
		}
	}
}

// httpAuthSettings resolves the bearer token of the HTTP front end, the
// same way `serve` resolves its own (#298): --http-token, else
// $LEANPROXY_SERVE_TOKEN, else the serve token file under home (created
// on first start). --no-auth returns no token and is refused on a
// non-loopback address and together with --http-token. source says where
// the token came from, for the startup log; it never holds the token.
func httpAuthSettings(listenAddr, flagToken string, noAuth bool, envToken, home string) (token, source string, err error) {
	if noAuth {
		if flagToken != "" {
			return "", "", errors.New("--no-auth and --http-token are mutually exclusive")
		}
		if !httpsec.IsLoopbackBindAddr(listenAddr) {
			return "", "", fmt.Errorf("refusing to start: --no-auth is only allowed on a loopback --http address, not %q", listenAddr)
		}
		return "", "", nil
	}
	if flagToken != "" {
		if err := checkServeToken(flagToken); err != nil {
			return "", "", fmt.Errorf("--http-token: %w", err)
		}
		return flagToken, "--http-token flag", nil
	}
	return resolveServeToken("", envToken, home)
}

// httpFrontendOptions builds the front end's options from the server.http
// config block and the command-line flags (whose host and origin lists
// add to the config's).
func httpFrontendOptions(cfg *migrate.Config, addr, token string, extraHosts, extraOrigins []string) streamhttp.Options {
	h := cfg.EffectiveHTTPFrontend()
	return streamhttp.Options{
		Addr:               addr,
		Token:              token,
		AllowedHosts:       append(h.AllowedHosts, extraHosts...),
		AllowedOrigins:     append(h.AllowedOrigins, extraOrigins...),
		MaxBodyBytes:       h.MaxBodyBytes,
		MaxSessions:        h.MaxSessions,
		SessionIdleTimeout: h.SessionIdleTimeout,
		MaxConcurrent:      cfg.EffectiveMaxConcurrentRequests(),
		Logger:             slog.Default(),
	}
}

// checkHTTPOrigins validates the --http-allowed-origins values like the
// config's.
func checkHTTPOrigins(origins []string) error {
	for _, o := range origins {
		if err := migrate.ValidateHTTPOrigin(o); err != nil {
			return fmt.Errorf("--http-allowed-origins: %w", err)
		}
	}
	return nil
}

// handleHTTP runs the Streamable HTTP front end until sig (SIGINT/SIGTERM)
// or, when idleExit is set, until no client has been connected for that
// long. It then ends every session, stops the listener and runs cleanup (which stops
// the health checker and closes the pools, so no child process is
// orphaned).
func handleHTTP(handler *mcp.Handler, opts streamhttp.Options, sig <-chan os.Signal, cleanup func(), statusStore *statusfile.FileStatusStore, idleExit time.Duration) error {
	defer cleanup()
	srv, err := streamhttp.New(handler, opts)
	if err != nil {
		return err
	}
	if err := srv.Start(); err != nil {
		return fmt.Errorf("HTTP front end: %w", err)
	}
	loopback := httpsec.IsLoopbackBindAddr(opts.Addr)
	if !loopback {
		slog.Warn("HTTP front end listening on a non-loopback interface; traffic is not encrypted (put a TLS proxy in front)", "listen", srv.Addr())
	}
	slog.Info("leanproxy-mcp Streamable HTTP front end started", "url", srv.URL(),
		"auth", srv.Authenticated(), "max_sessions", opts.MaxSessions,
		"max_concurrent_requests", opts.MaxConcurrent, "allowed_origins", len(opts.AllowedOrigins))
	if statusStore != nil {
		statusStore.SetHTTPFrontend(&statusfile.HTTPFrontendStatus{
			URL:            srv.URL(),
			Loopback:       loopback,
			Auth:           srv.Authenticated(),
			AllowedHosts:   opts.AllowedHosts,
			AllowedOrigins: opts.AllowedOrigins,
			MaxSessions:    opts.MaxSessions,
		})
		defer statusStore.RemoveFile()
	}

	idle := make(chan struct{})
	if idleExit > 0 {
		idleCtx, stopIdle := context.WithCancel(context.Background())
		defer stopIdle()
		go func() {
			if waitIdle(idleCtx, srv.SessionCount, idleExit, idleCheckInterval(idleExit)) {
				close(idle)
			}
		}()
	}
	select {
	case <-sig:
	case <-idle:
		slog.Info("no client connected, stopping (--idle-exit)", "idle", idleExit)
	}
	slog.Info("shutting down the HTTP front end", "sessions", srv.SessionCount())
	flushUsageSnapshot()

	ctx, cancel := context.WithTimeout(context.Background(), httpShutdownGrace)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warn("HTTP front end did not stop cleanly", "error", err)
	}
	return nil
}
