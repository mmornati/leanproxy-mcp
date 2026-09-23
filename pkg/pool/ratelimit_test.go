package pool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

// TestRateLimitersDefaultUnlimited verifies that a server with no rate_limit
// configured has no limiter installed and never waits.
func TestRateLimitersDefaultUnlimited(t *testing.T) {
	rl := newServerRateLimiters()
	rl.set("svc", nil)

	if l := rl.get("svc"); l != nil {
		t.Fatalf("expected no limiter for unconfigured server, got %+v", l)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := rl.wait(ctx, "svc", "tools/call"); err != nil {
		t.Fatalf("wait() on an unlimited server should never fail: %v", err)
	}
}

// TestRateLimitersZeroRPSIsUnlimited verifies that an explicit
// requests_per_second of 0 is treated the same as "unlimited".
func TestRateLimitersZeroRPSIsUnlimited(t *testing.T) {
	rl := newServerRateLimiters()
	rl.set("svc", &migrate.RateLimitConfig{RequestsPerSecond: 0, Burst: 5})

	if l := rl.get("svc"); l != nil {
		t.Fatalf("expected no limiter for requests_per_second=0, got %+v", l)
	}
}

// TestRateLimitersInternalMethodsBypass verifies that health pings, the
// initialize handshake, and tools/list cache refreshes never wait, even
// when a server's limiter is fully saturated.
func TestRateLimitersInternalMethodsBypass(t *testing.T) {
	rl := newServerRateLimiters()
	// burst=1 so the very next non-internal call would have to wait.
	rl.set("svc", &migrate.RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1})

	// Drain the single token with a real (non-internal) call.
	if err := rl.wait(context.Background(), "svc", "tools/call"); err != nil {
		t.Fatalf("first call should succeed immediately: %v", err)
	}

	for _, method := range []string{"ping", "initialize", "tools/list"} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := rl.wait(ctx, "svc", method)
		cancel()
		if err != nil {
			t.Errorf("internal method %q should bypass the limiter, got error: %v", method, err)
		}
	}
}

// TestRateLimitersWaitsThenSucceeds verifies that once the burst is spent,
// further requests wait for a token instead of being rejected, and succeed
// once one becomes available.
func TestRateLimitersWaitsThenSucceeds(t *testing.T) {
	rl := newServerRateLimiters()
	// 100 req/s with a burst of 1: the 2nd call must wait ~10ms for the
	// next token rather than fail outright.
	rl.set("svc", &migrate.RateLimitConfig{RequestsPerSecond: 100, Burst: 1})

	if err := rl.wait(context.Background(), "svc", "tools/call"); err != nil {
		t.Fatalf("first call should succeed immediately: %v", err)
	}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rl.wait(ctx, "svc", "tools/call"); err != nil {
		t.Fatalf("second call should wait and then succeed, got error: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 5*time.Millisecond {
		t.Errorf("expected the second call to wait for a token, only took %v", elapsed)
	}
}

// TestRateLimitersDeadlineExceeded verifies that a request whose context
// deadline is shorter than the wait fails with the documented error.
func TestRateLimitersDeadlineExceeded(t *testing.T) {
	rl := newServerRateLimiters()
	// 1 req/s with burst 1: the 2nd call needs ~1s for a token, far longer
	// than the 10ms deadline below.
	rl.set("svc", &migrate.RateLimitConfig{RequestsPerSecond: 1, Burst: 1})

	if err := rl.wait(context.Background(), "svc", "tools/call"); err != nil {
		t.Fatalf("first call should succeed immediately: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := rl.wait(ctx, "svc", "tools/call")
	if err == nil {
		t.Fatal("expected the second call to fail once its deadline elapses")
	}
	if !strings.Contains(err.Error(), "rate limit wait exceeded deadline for svc") {
		t.Errorf("error = %v, want it to mention the documented deadline message", err)
	}
}

// TestRateLimitersRemove verifies that remove() clears a previously
// configured limiter.
func TestRateLimitersRemove(t *testing.T) {
	rl := newServerRateLimiters()
	rl.set("svc", &migrate.RateLimitConfig{RequestsPerSecond: 1, Burst: 1})
	if l := rl.get("svc"); l == nil {
		t.Fatal("expected a limiter to be installed")
	}
	rl.remove("svc")
	if l := rl.get("svc"); l != nil {
		t.Fatalf("expected no limiter after remove(), got %+v", l)
	}
}

// TestPutRequestDefaultNoRateLimit verifies the pool-level integration: a
// server started without a rate_limit config never has PutRequest wait or
// fail because of a limiter.
func TestPutRequestDefaultNoRateLimit(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(50, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name:         "no-limit",
		Transport:    registry.TransportStdio,
		Stdio:        &migrate.StdioConfig{Command: "cat"},
		TimeoutValue: 30 * time.Second,
	}
	if err := pool.StartServer(ctx, config); err != nil {
		t.Fatalf("StartServer() failed: %v", err)
	}

	for i := 0; i < 20; i++ {
		req := Request{Method: "tools/call", ID: i, Timeout: 200 * time.Millisecond}
		if err := pool.PutRequest("no-limit", req); err != nil {
			t.Fatalf("PutRequest() %d failed with default (unlimited) config: %v", i, err)
		}
	}
}

// TestPutRequestConfiguredLimitWaits verifies that a configured limit makes
// PutRequest wait for a token rather than reject the request outright, as
// long as the request's own timeout allows it.
func TestPutRequestConfiguredLimitWaits(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(50, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name:         "limited",
		Transport:    registry.TransportStdio,
		Stdio:        &migrate.StdioConfig{Command: "cat"},
		TimeoutValue: 30 * time.Second,
		RateLimit:    &migrate.RateLimitConfig{RequestsPerSecond: 200, Burst: 2},
	}
	if err := pool.StartServer(ctx, config); err != nil {
		t.Fatalf("StartServer() failed: %v", err)
	}

	start := time.Now()
	for i := 0; i < 10; i++ {
		req := Request{Method: "tools/call", ID: i, Timeout: time.Second}
		if err := pool.PutRequest("limited", req); err != nil {
			t.Fatalf("PutRequest() %d should wait rather than fail, got error: %v", i, err)
		}
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Errorf("expected the burst to be exhausted and later calls to wait, only took %v", elapsed)
	}
}

// TestPutRequestDeadlineExceededError verifies that a short request timeout
// combined with a saturated limiter produces the documented error, not a
// silent success or a generic timeout.
func TestPutRequestDeadlineExceededError(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(50, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name:         "tight",
		Transport:    registry.TransportStdio,
		Stdio:        &migrate.StdioConfig{Command: "cat"},
		TimeoutValue: 30 * time.Second,
		RateLimit:    &migrate.RateLimitConfig{RequestsPerSecond: 1, Burst: 1},
	}
	if err := pool.StartServer(ctx, config); err != nil {
		t.Fatalf("StartServer() failed: %v", err)
	}

	// Spend the single burst token.
	first := Request{Method: "tools/call", ID: 1, Timeout: time.Second}
	if err := pool.PutRequest("tight", first); err != nil {
		t.Fatalf("first PutRequest() should succeed immediately: %v", err)
	}

	// The next call needs ~1s for a token; a 20ms timeout must fail fast
	// with the documented message rather than waiting the full second.
	second := Request{Method: "tools/call", ID: 2, Timeout: 20 * time.Millisecond}
	err := pool.PutRequest("tight", second)
	if err == nil {
		t.Fatal("expected PutRequest() to fail once its short timeout elapses")
	}
	if !strings.Contains(err.Error(), "rate limit wait exceeded deadline for tight") {
		t.Errorf("error = %v, want it to mention the documented deadline message", err)
	}
}

// TestPutRequestHealthPingBypassesLimit verifies that a ping never fails
// because of a saturated rate limiter — health checks must not report
// false failures due to rate limiting.
func TestPutRequestHealthPingBypassesLimit(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(50, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name:         "pinged",
		Transport:    registry.TransportStdio,
		Stdio:        &migrate.StdioConfig{Command: "cat"},
		TimeoutValue: 30 * time.Second,
		RateLimit:    &migrate.RateLimitConfig{RequestsPerSecond: 0.001, Burst: 1},
	}
	if err := pool.StartServer(ctx, config); err != nil {
		t.Fatalf("StartServer() failed: %v", err)
	}

	// Spend the single burst token with a real call.
	if err := pool.PutRequest("pinged", Request{Method: "tools/call", ID: 1, Timeout: time.Second}); err != nil {
		t.Fatalf("first PutRequest() should succeed immediately: %v", err)
	}

	// A ping right afterwards, with a short timeout, must still succeed
	// immediately: it is exempt from the limiter entirely.
	ping := Request{Method: "ping", ID: 2, Timeout: 50 * time.Millisecond}
	start := time.Now()
	if err := pool.PutRequest("pinged", ping); err != nil {
		t.Fatalf("ping should bypass the rate limiter, got error: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 40*time.Millisecond {
		t.Errorf("ping took %v, expected it to bypass the limiter and return immediately", elapsed)
	}
}

// TestNewLimiterFromConfigBurstDefault verifies that an unset burst
// defaults to the per-second rate, rounded up, with a floor of 1.
func TestNewLimiterFromConfigBurstDefault(t *testing.T) {
	l := newLimiterFromConfig(&migrate.RateLimitConfig{RequestsPerSecond: 0.5})
	if l == nil {
		t.Fatal("expected a limiter for a positive requests_per_second")
	}
	if b := l.Burst(); b != 1 {
		t.Errorf("Burst() = %d, want 1 for requests_per_second=0.5", b)
	}

	l = newLimiterFromConfig(&migrate.RateLimitConfig{RequestsPerSecond: 20})
	if b := l.Burst(); b != 20 {
		t.Errorf("Burst() = %d, want 20 for requests_per_second=20 with no explicit burst", b)
	}
}
