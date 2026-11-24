package pool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/stretchr/testify/require"
)

// TestNoPingHelperProcess is a fake stdio MCP server that does not implement
// ping (it answers "Method not found") and fails the "fail" method; every
// other request gets an empty result.
func TestNoPingHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_NOPING_HELPER") != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var msg map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if _, ok := msg["id"]; !ok {
			continue
		}
		resp := map[string]interface{}{"jsonrpc": "2.0", "id": msg["id"]}
		switch msg["method"] {
		case "initialize":
			resp["result"] = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "fake-mcp", "version": "1.0.0"},
			}
		case "ping":
			resp["error"] = map[string]interface{}{"code": -32601, "message": "Method not found"}
		case "fail":
			resp["error"] = map[string]interface{}{"code": -32000, "message": "tool failed"}
		default:
			resp["result"] = map[string]interface{}{}
		}
		data, _ := json.Marshal(resp)
		os.Stdout.Write(append(data, '\n'))
	}
	os.Exit(0)
}

func noPingMCPConfig(name string) *migrate.ServerConfig {
	return &migrate.ServerConfig{
		Name:      name,
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: os.Args[0],
			Args:    []string{"-test.run=TestNoPingHelperProcess"},
			Env:     []string{"GO_WANT_NOPING_HELPER=1"},
		},
		TimeoutValue: 30 * time.Second,
	}
}

// TestHealthPingErrorReplyIsHealthy: a JSON-RPC error reply to the health
// ping proves the server is alive; it must not count as a liveness failure.
func TestHealthPingErrorReplyIsHealthy(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()
	require.NoError(t, pool.StartServer(ctx, noPingMCPConfig("noping")))

	// Run the handshake: the probe only pings initialized servers.
	_, err := pool.SendRequestToServer(ctx, "noping", "tools/list", nil, 5*time.Second)
	require.NoError(t, err)

	server, err := pool.GetServer("noping")
	require.NoError(t, err)
	hc := NewHealthChecker(pool, nil)

	ok, _ := hc.performPingCheck(ctx, server)
	require.True(t, ok, "an error reply to ping is still a reply")

	result := hc.CheckServer(ctx, "noping")
	require.Equal(t, HealthHealthy, result.Status, result.Error)
}

// TestHealthPingInvisibleToIdleAndStats: health pings must not reset the
// idle clock (idle_timeout would never fire) nor be counted in ServerStats.
func TestHealthPingInvisibleToIdleAndStats(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()
	config := noPingMCPConfig("idle")
	config.IdleTimeoutValue = 300 * time.Millisecond
	require.NoError(t, pool.StartServer(ctx, config))

	_, err := pool.SendRequestToServer(ctx, "idle", "tools/list", nil, 5*time.Second)
	require.NoError(t, err)

	server, err := pool.GetServer("idle")
	require.NoError(t, err)
	before := server.getStats()
	require.Equal(t, int64(1), before.RequestCount)
	require.False(t, before.LastRequestAt.IsZero())

	// Ping (the server answers with an error, the worst case for the
	// counters) well past the idle timeout.
	hc := NewHealthChecker(pool, nil)
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		ok, _ := hc.performPingCheck(ctx, server)
		require.True(t, ok)
		time.Sleep(50 * time.Millisecond)
	}

	after := server.getStats()
	require.Equal(t, before.RequestCount, after.RequestCount)
	require.Equal(t, before.ErrorCount, after.ErrorCount)
	require.Equal(t, before.AvgLatencyMs, after.AvgLatencyMs)
	require.Equal(t, before.LastError, after.LastError)
	require.True(t, before.LastRequestAt.Equal(after.LastRequestAt))

	// The pings did not keep the server alive: the idle check stops it.
	server.checkIdleTimeout(ctx)
	require.Eventually(t, func() bool {
		return server.getState() == StateStopped
	}, 10*time.Second, 20*time.Millisecond, "idle server pinged by the health checker must still idle out")
}

// TestRecentErrorRate: the rate is errors/requests (RequestCount already
// includes the errors) over the requests since the previous check.
func TestRecentErrorRate(t *testing.T) {
	hc := NewHealthChecker(NewStdioPool(5, 5*time.Minute, nil), nil)

	// Every request failed: 100%, not the 50% of errors/(requests+errors).
	require.InDelta(t, 1.0, hc.recentErrorRate("s", ServerStats{RequestCount: 2, ErrorCount: 2}), 1e-9)
	// 8 more requests, none failed: the earlier failures are out of the window.
	require.Zero(t, hc.recentErrorRate("s", ServerStats{RequestCount: 10, ErrorCount: 2}))
	// 10 more requests, 1 failed.
	require.InDelta(t, 0.1, hc.recentErrorRate("s", ServerStats{RequestCount: 20, ErrorCount: 3}), 1e-9)
	// No request since: no rate, even if a crash bumped ErrorCount alone.
	require.Zero(t, hc.recentErrorRate("s", ServerStats{RequestCount: 20, ErrorCount: 4}))
}

// TestHealthDegradedIsRecentAndStillPinged: failing requests make the
// server degraded for the window they fall in only, and a degraded server
// is still pinged.
func TestHealthDegradedIsRecentAndStillPinged(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()
	require.NoError(t, pool.StartServer(ctx, noPingMCPConfig("flaky")))

	resp, err := pool.SendRequestToServer(ctx, "flaky", "fail", nil, 5*time.Second)
	require.NoError(t, err)
	require.NotNil(t, resp.Error)

	hc := NewHealthChecker(pool, nil)
	result := hc.CheckServer(ctx, "flaky")
	require.Equal(t, HealthDegraded, result.Status)
	// The ping ran: its latency replaced the (stats) average.
	stats, _ := pool.GetServerStats("flaky")
	require.NotEqual(t, stats.AvgLatencyMs, result.LatencyMs)

	// No failure since the previous check: healthy again.
	result = hc.CheckServer(ctx, "flaky")
	require.Equal(t, HealthHealthy, result.Status, result.Error)
}

// TestHealthDegradedWedgedServerIsUnhealthy: a server with a high error
// rate that wedges must fail its ping (and so be restarted) instead of
// sitting in "degraded" forever.
func TestHealthDegradedWedgedServerIsUnhealthy(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()
	config := fakeMCPConfig("wedged")
	config.Stdio.Env = append(config.Stdio.Env, "GO_HELPER_HANG=1")
	require.NoError(t, pool.StartServer(ctx, config))
	pool.MarkServerMCPInitialized("wedged")

	server, err := pool.GetServer("wedged")
	require.NoError(t, err)
	server.mu.Lock()
	server.stats.RequestCount = 10
	server.stats.ErrorCount = 5
	server.mu.Unlock()

	result := NewHealthChecker(pool, nil).CheckServer(ctx, "wedged")
	require.Equal(t, HealthUnhealthy, result.Status)
	require.Equal(t, "MCP ping failed", result.Error)
}

// TestServerStatsLiveFields covers the fields the admin UI reads:
// LastRequestAt, InFlight, PID and StartedAt.
func TestServerStatsLiveFields(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()
	spawnedAfter := time.Now()
	require.NoError(t, pool.StartServer(ctx, fakeMCPConfig("stats")))

	server, err := pool.GetServer("stats")
	require.NoError(t, err)
	server.mu.Lock()
	pid := server.process.Process.Pid
	server.mu.Unlock()

	stats, err := pool.GetServerStats("stats")
	require.NoError(t, err)
	require.True(t, stats.LastRequestAt.IsZero(), "no request yet")
	require.Equal(t, pid, stats.PID)
	require.NotZero(t, stats.PID)
	require.False(t, stats.StartedAt.Before(spawnedAfter))
	require.Zero(t, stats.InFlight)

	requestedAfter := time.Now()
	_, err = pool.SendRequestToServer(ctx, "stats", "tools/list", nil, 5*time.Second)
	require.NoError(t, err)
	stats, _ = pool.GetServerStats("stats")
	require.False(t, stats.LastRequestAt.Before(requestedAfter))

	release, err := server.acquireSlot(ctx)
	require.NoError(t, err)
	stats, _ = pool.GetServerStats("stats")
	require.Equal(t, 1, stats.InFlight)
	release()

	require.NoError(t, pool.StopServer("stats"))
	stats, _ = pool.GetServerStats("stats")
	require.Zero(t, stats.PID)
	require.True(t, stats.StartedAt.IsZero())
	require.Zero(t, stats.InFlight)
	require.False(t, stats.LastRequestAt.IsZero(), "the last request time survives a stop")
}

// TestIsTransportErrorStreamableHTTPStatuses pins the classification to the
// errors mcp-go's Streamable HTTP transport really returns: a 404 (session
// terminated/expired) must trigger a reconnect, a 401 must not.
func TestIsTransportErrorStreamableHTTPStatuses(t *testing.T) {
	send := func(t *testing.T, status int) error {
		t.Helper()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		t.Cleanup(srv.Close)
		tr, err := transport.NewStreamableHTTP(srv.URL)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tr.Close() })
		_, err = tr.SendRequest(context.Background(), transport.JSONRPCRequest{JSONRPC: "2.0", Method: "tools/list"})
		require.Error(t, err)
		// client.Client wraps what the transport returns this way.
		return fmt.Errorf("http_pool: list tools: %w", transport.NewError(err))
	}

	expired := send(t, http.StatusNotFound)
	require.ErrorIs(t, expired, transport.ErrSessionTerminated)
	require.True(t, isTransportError(expired), "an expired session must be re-initialized by a reconnect")

	unauthorized := send(t, http.StatusUnauthorized)
	require.ErrorIs(t, unauthorized, transport.ErrAuthorizationRequired)
	require.False(t, isTransportError(unauthorized), "reconnecting cannot fix missing credentials")
}

// unexportedField reads the named unexported field of the struct v points
// to (or wraps in an interface).
func unexportedField(t *testing.T, v reflect.Value, name string) reflect.Value {
	t.Helper()
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	f := v.FieldByName(name)
	require.True(t, f.IsValid(), "field %s not found in %s", name, v.Type())
	return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
}

// TestSSEClientHonorsProxyEnvironment: the SSE client's custom
// http.Transport must keep the standard proxy environment support
// (HTTP_PROXY/HTTPS_PROXY/NO_PROXY) the default transport has.
func TestSSEClientHonorsProxyEnvironment(t *testing.T) {
	s := NewSSEServer("sse", &migrate.ServerConfig{
		Name:      "sse",
		Transport: registry.TransportSSE,
		HTTP:      &migrate.HTTPConfig{URL: "http://127.0.0.1:1/sse"},
	}, nil)
	c, err := s.buildClient()
	require.NoError(t, err)
	defer c.Close()

	sse, ok := c.GetTransport().(*transport.SSE)
	require.True(t, ok)
	httpClient := unexportedField(t, reflect.ValueOf(sse), "httpClient").Interface().(*http.Client)
	base := unexportedField(t, reflect.ValueOf(httpClient.Transport), "base").Interface().(http.RoundTripper)
	tr, ok := base.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, tr.Proxy)
	require.Equal(t,
		reflect.ValueOf(http.ProxyFromEnvironment).Pointer(),
		reflect.ValueOf(tr.Proxy).Pointer())
}
