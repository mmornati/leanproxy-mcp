package pool

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// internalMethods lists JSON-RPC methods that only ever carry leanproxy's
// own housekeeping traffic to a downstream server: health pings, the
// startup handshake, and periodic tool-list cache refreshes. None of these
// are requests a client ever issues through the gateway (see
// pkg/mcp/handlers.go: MethodInitialize and MethodToolsList are answered
// locally and never forwarded), so they are always exempt from a
// per-server rate limit — a health check must never fail because the
// limiter is saturated by real traffic.
var internalMethods = map[string]bool{
	"ping":       true,
	"initialize": true,
	"tools/list": true,
}

// serverRateLimiters holds one optional golang.org/x/time/rate.Limiter per
// server name. A server with no entry is unlimited, which is the default
// for every server unless a rate_limit block is configured for it.
type serverRateLimiters struct {
	mu       sync.RWMutex
	limiters map[string]*rate.Limiter
}

func newServerRateLimiters() *serverRateLimiters {
	return &serverRateLimiters{limiters: make(map[string]*rate.Limiter)}
}

// newLimiterFromConfig builds a rate.Limiter from a ServerConfig's optional
// rate_limit block, or returns nil for "unlimited" (nil config, or
// requests_per_second <= 0).
func newLimiterFromConfig(cfg *migrate.RateLimitConfig) *rate.Limiter {
	if cfg == nil || cfg.RequestsPerSecond <= 0 {
		return nil
	}
	burst := cfg.Burst
	if burst <= 0 {
		// Default the burst to the per-second rate (rounded up), so a
		// server configured only with requests_per_second still allows a
		// small burst instead of admitting exactly one request per tick.
		burst = int(cfg.RequestsPerSecond + 0.999999)
		if burst < 1 {
			burst = 1
		}
	}
	return rate.NewLimiter(rate.Limit(cfg.RequestsPerSecond), burst)
}

// set installs (or clears, for a nil/unlimited cfg) the limiter for name.
func (s *serverRateLimiters) set(name string, cfg *migrate.RateLimitConfig) {
	limiter := newLimiterFromConfig(cfg)

	s.mu.Lock()
	defer s.mu.Unlock()
	if limiter == nil {
		delete(s.limiters, name)
		return
	}
	s.limiters[name] = limiter
}

// remove drops any limiter configured for name.
func (s *serverRateLimiters) remove(name string) {
	s.mu.Lock()
	delete(s.limiters, name)
	s.mu.Unlock()
}

func (s *serverRateLimiters) get(name string) *rate.Limiter {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limiters[name]
}

// boundedContext returns a context derived from ctx with an additional
// timeout, when timeout is positive. A zero or negative timeout leaves ctx
// as-is instead of expiring the derived context immediately.
func boundedContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// wait blocks until a token is available for method on server name. It
// returns immediately (nil) when method is internal housekeeping traffic or
// when no limit is configured for that server. Otherwise it waits up to
// ctx's deadline for a token, returning a clear error if the deadline
// elapses first.
func (s *serverRateLimiters) wait(ctx context.Context, name, method string) error {
	if internalMethods[method] {
		return nil
	}
	limiter := s.get(name)
	if limiter == nil {
		return nil
	}
	if err := limiter.Wait(ctx); err != nil {
		return fmt.Errorf("rate limit wait exceeded deadline for %s", name)
	}
	return nil
}
