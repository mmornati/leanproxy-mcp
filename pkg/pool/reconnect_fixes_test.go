package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/stretchr/testify/require"
)

// echoServerConfig builds a StdioServerConfig that runs the fake MCP helper
// (TestHelperProcess) as the child process.
func echoServerConfig(name string, extraEnv ...string) StdioServerConfig {
	return StdioServerConfig{
		Name:           name,
		Command:        os.Args[0],
		Args:           []string{"-test.run=TestHelperProcess"},
		Env:            append([]string{"GO_WANT_HELPER_PROCESS=1"}, extraEnv...),
		RequestTimeout: 5 * time.Second,
	}
}

func TestIsTransportError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// Server-side JSON-RPC errors arrive as plain fmt.Errorf values
		// carrying the server's message; they must NOT be retried as
		// transport failures.
		{"server-side session error", fmt.Errorf("session expired"), false},
		{"server-side timeout message", fmt.Errorf("upstream timeout while streaming response"), false},
		{"server-side stream message", fmt.Errorf("stream not found"), false},
		{"generic application error", fmt.Errorf("tool execution failed: invalid argument"), false},
		// Genuine transport failures.
		{"plain EOF", io.EOF, true},
		{"wrapped unexpected EOF", fmt.Errorf("read body: %w", io.ErrUnexpectedEOF), true},
		{"net op error conn refused", &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}, true},
		{"wrapped errno conn reset", fmt.Errorf("write: %w", syscall.ECONNRESET), true},
		{"wrapped broken pipe", fmt.Errorf("write: %w", syscall.EPIPE), true},
		{"literal connection refused text", fmt.Errorf("dial tcp 127.0.0.1:8080: connection refused"), true},
		{"literal broken pipe text", fmt.Errorf("write: broken pipe"), true},
		{"context deadline", context.DeadlineExceeded, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isTransportError(tt.err))
		})
	}
}

func TestJSONIDEqual(t *testing.T) {
	require.True(t, jsonIDEqual(float64(1), int64(1)), "decoded float64 ID must match int64 wire ID")
	require.True(t, jsonIDEqual("a", "a"))
	require.False(t, jsonIDEqual("1", int64(1)), "string and numeric IDs are distinct")
	require.False(t, jsonIDEqual(nil, int64(1)))
}

func TestReconnectSettingsValidateClampsBackoff(t *testing.T) {
	s := ReconnectSettings{RestartBackoff: time.Nanosecond}.validate()
	require.Equal(t, minRestartBackoff, s.RestartBackoff,
		"tiny backoff must be floored (jitter math panics below 4ns)")

	s = ReconnectSettings{RestartBackoff: time.Hour}.validate()
	require.Equal(t, maxRestartBackoff, s.RestartBackoff,
		"backoff must be capped so the first wait honors the documented 1m maximum")

	s = ReconnectSettings{}.validate()
	require.Equal(t, time.Second, s.RestartBackoff)
}

// TestIdleTimeoutStopDoesNotDeadlock is the regression test for the
// idle-timeout self-deadlock: the idle check runs on the request-loop
// goroutine, which stopLocked waits on via the WaitGroup — a synchronous stop
// would wait on itself and wedge the server (and every later restart)
// permanently.
func TestIdleTimeoutStopDoesNotDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	ctx := context.Background()

	config := echoServerConfig("test-idle")
	config.IdleTimeout = 300 * time.Millisecond
	server := newServerV2("test-idle", config, slog.Default())
	// Speed up the fixed 30s idle-check ticker before spawning so the swap
	// happens-before the request loop starts.
	server.healthTicker.Stop()
	server.healthTicker = time.NewTicker(100 * time.Millisecond)

	require.NoError(t, server.spawn(ctx))
	defer server.stop()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if server.getState() == StateStopped {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.Equal(t, StateStopped, server.getState(),
		"idle timeout must stop the server without deadlocking")

	// The lifecycle must not be wedged: a later stop returns promptly and an
	// on-demand restart (the documented "revives on next use" path) works.
	done := make(chan error, 1)
	go func() { done <- server.stop() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop wedged after idle-timeout stop")
	}

	require.NoError(t, server.restart(ctx))
	require.Equal(t, StateIdle, server.getState(), "idle-stopped server must be restartable on demand")
}

// TestCrashAutoRestartDisabledByMasterSwitch verifies reconnect.enabled=false
// gates the crash-restart path: a crashed server stays in the error state
// instead of being respawned, while a deliberate restart still revives it.
func TestCrashAutoRestartDisabledByMasterSwitch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	ctx := context.Background()

	server := newServerV2("test-disabled", echoServerConfig("test-disabled"), slog.Default())
	server.applyReconnect(ReconnectSettings{
		Disabled:           true,
		MaxRestartAttempts: 5,
		RestartBackoff:     100 * time.Millisecond,
		StableWindow:       time.Minute,
	})
	require.NoError(t, server.spawn(ctx))
	defer server.stop()

	server.mu.Lock()
	originalPid := server.process.Process.Pid
	server.mu.Unlock()
	require.NoError(t, server.process.Process.Kill())

	// Give the (disabled) crash recovery ample time to misfire.
	time.Sleep(2 * time.Second)

	require.Equal(t, StateError, server.getState(),
		"crashed server must stay in error state when auto-reconnect is disabled")
	server.mu.Lock()
	currentPid := server.process.Process.Pid
	server.mu.Unlock()
	require.Equal(t, originalPid, currentPid, "server must not be respawned when auto-reconnect is disabled")

	// A deliberate restart still works (idle-timeout revive path).
	require.NoError(t, server.restart(ctx))
	require.Equal(t, StateIdle, server.getState())
}

// TestStopEscalatesSIGKILLForSIGTERMIgnoringProcess verifies that a process
// ignoring SIGTERM (the wedged case the health checker exists to recover
// from) cannot wedge the stop path forever.
func TestStopEscalatesSIGKILLForSIGTERMIgnoringProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	server := newServerV2("test-stubborn", echoServerConfig("test-stubborn", "GO_HELPER_IGNORE_SIGTERM=1"), slog.Default())
	require.NoError(t, server.spawn(context.Background()))

	// Give the child time to install its SIGTERM handler; a SIGTERM delivered
	// during process boot would hit the default disposition and kill it,
	// defeating the purpose of the test.
	time.Sleep(500 * time.Millisecond)

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- server.stop() }()
	select {
	case <-done:
	case <-time.After(stopGracePeriod + 10*time.Second):
		t.Fatal("stop wedged: SIGTERM-ignoring process was not escalated to SIGKILL")
	}
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, stopGracePeriod-time.Second,
		"stop should have waited out the SIGTERM grace period before escalating, took %v", elapsed)
	require.Equal(t, StateStopped, server.getState())
}

// TestSendRequestDropsStaleResponses verifies that a stale or cross-generation
// response buffered in the shared response channel is never delivered to an
// unrelated in-flight request.
func TestSendRequestDropsStaleResponses(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	ctx := context.Background()
	server := newServerV2("test-stale", echoServerConfig("test-stale"), slog.Default())
	require.NoError(t, server.spawn(ctx))
	defer server.stop()

	// Pre-buffer a stale response whose ID cannot match the internal wire ID
	// of the next request.
	server.responseCh <- Response{ID: "stale-from-old-generation", Result: json.RawMessage(`{"stale":true}`)}

	result, err := server.sendRequest(ctx, Request{Method: "tools/list", ID: 1, Timeout: 5 * time.Second}, make(chan struct{}))
	require.NoError(t, err)
	require.NotContains(t, string(result), "stale", "stale response must be discarded, not delivered")
}

// TestDeliberateRestartResetsCrashBudget verifies that a request- or
// operator-triggered restart grants the server a fresh crash-restart budget
// instead of carrying stale credit from an earlier crash loop.
func TestDeliberateRestartResetsCrashBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	ctx := context.Background()
	server := newServerV2("test-budget", echoServerConfig("test-budget"), slog.Default())
	require.NoError(t, server.spawn(ctx))
	defer server.stop()

	server.mu.Lock()
	server.restartCount = 4
	server.mu.Unlock()
	server.autoRestartExhausted.Store(true)

	require.NoError(t, server.restart(ctx))

	server.mu.Lock()
	restartCount := server.restartCount
	server.mu.Unlock()
	require.Equal(t, 0, restartCount, "deliberate restart must reset the crash-restart budget")
	require.False(t, server.autoRestartExhausted.Load())
}

// TestHealthCheckerLeavesStoppedServerStopped is the regression test for the
// idle_timeout churn loop: the health checker must neither count failures
// against nor restart a deliberately stopped server — it revives lazily on
// its next request.
func TestHealthCheckerLeavesStoppedServerStopped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping process-based test in short mode")
	}
	ctx := context.Background()
	p := NewStdioPool(5, 5*time.Minute, nil)
	defer p.Close()

	require.NoError(t, p.StartServer(ctx, fakeMCPConfig("test-stopped")))
	require.NoError(t, p.StopServer("test-stopped"))

	hc := NewHealthChecker(p, nil)
	hc.SetMaxFailures(1)
	hc.checkAllServers(ctx)
	// Give a (wrongly) triggered restart a chance to run.
	time.Sleep(300 * time.Millisecond)

	state, err := p.GetServerState("test-stopped")
	require.NoError(t, err)
	require.Equal(t, StateStopped, state, "health checker must not restart an idle-stopped server")

	hc.mu.RLock()
	check := hc.checks["test-stopped"]
	hc.mu.RUnlock()
	require.NotNil(t, check)
	check.mu.Lock()
	failures := check.consecutiveFailures
	check.mu.Unlock()
	require.Equal(t, 0, failures, "stopped server must not accrue health-check failures")
}

// TestClosedRemoteServerIsNotResurrected verifies that an in-flight request
// racing a pool shutdown cannot resurrect a deliberately closed SSE/HTTP
// server.
func TestClosedRemoteServerIsNotResurrected(t *testing.T) {
	sseServer := NewSSEServer("test-sse-closed", &migrate.ServerConfig{
		Name: "test-sse-closed",
		HTTP: &migrate.HTTPConfig{URL: "http://127.0.0.1:1/sse"},
	}, slog.Default())
	require.NoError(t, sseServer.Close())
	_, err := sseServer.ensureConnected(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed", "closed SSE server must refuse to reconnect")

	httpServer := NewHTTPClientServer("test-http-closed", &migrate.ServerConfig{
		Name: "test-http-closed",
		HTTP: &migrate.HTTPConfig{URL: "http://127.0.0.1:1/mcp"},
	}, slog.Default())
	require.NoError(t, httpServer.Close())
	_, err = httpServer.ensureConnected(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "closed", "closed HTTP server must refuse to reconnect")
}
