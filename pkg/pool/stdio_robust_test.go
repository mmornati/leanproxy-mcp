package pool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/stretchr/testify/require"
)

// Tests for #295: large responses, stderr drain, reader death, blocked
// stdin writes, process groups and parallel Close.

// testStopGrace keeps SIGTERM→SIGKILL escalation short so these tests stay
// well inside the CI timeout.
const testStopGrace = time.Second

type fakeOpts struct {
	command          string
	args             []string
	maxResponseBytes int
	timeout          time.Duration
}

// startFake starts one concurrentmcp-based server (or opts.command) in p.
func startFake(t *testing.T, p *StdioPool, name string, opts fakeOpts) {
	t.Helper()
	bin := concurrentMCPBinary(t)
	cmd := opts.command
	if cmd == "" {
		cmd = bin
	}
	timeout := opts.timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	require.NoError(t, p.StartServer(context.Background(), &migrate.ServerConfig{
		Name:             name,
		Transport:        registry.TransportStdio,
		Stdio:            &migrate.StdioConfig{Command: cmd, Args: opts.args},
		TimeoutValue:     timeout,
		MaxResponseBytes: opts.maxResponseBytes,
	}))
}

func newTestPool(t *testing.T) *StdioPool {
	t.Helper()
	p := NewStdioPool(5, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.stopGrace = testStopGrace
	p.SetReconnect(ReconnectSettings{RestartBackoff: 50 * time.Millisecond})
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func bigText(t *testing.T, resp *Response) int {
	t.Helper()
	return len(resultTag(t, resp))
}

// TestStdioRobust_LargeResponseRelayedIntact: a 5 MB response (well over
// the old hard 1 MB limit) arrives intact with the default limit.
func TestStdioRobust_LargeResponseRelayedIntact(t *testing.T) {
	p := newTestPool(t)
	startFake(t, p, "big", fakeOpts{})

	const size = 5 << 20
	resp, err := callTool(context.Background(), p, "big", "big", map[string]interface{}{"bytes": size}, 30*time.Second)
	require.NoError(t, err)
	require.Equal(t, size, bigText(t, resp))
}

// TestStdioRobust_OversizedResponseFailsOnlyThatCall: with
// max_response_bytes 1 MiB a 1.5 MB response fails that call only, with a
// clear error, while a concurrent call and the next call to the same
// server succeed (the server used to wedge permanently).
func TestStdioRobust_OversizedResponseFailsOnlyThatCall(t *testing.T) {
	for _, idLast := range []bool{false, true} {
		t.Run(fmt.Sprintf("id_last=%v", idLast), func(t *testing.T) {
			p := newTestPool(t)
			startFake(t, p, "capped", fakeOpts{maxResponseBytes: 1 << 20})
			ctx := context.Background()

			// A concurrent, older request still pending while the
			// oversized line arrives: it must not be the one failed.
			var wg sync.WaitGroup
			var slowResp *Response
			var slowErr error
			wg.Add(1)
			go func() {
				defer wg.Done()
				slowResp, slowErr = callTool(ctx, p, "capped", "sleep", map[string]interface{}{"ms": 500, "tag": "slow"}, 10*time.Second)
			}()
			time.Sleep(100 * time.Millisecond)

			start := time.Now()
			_, err := callTool(ctx, p, "capped", "big", map[string]interface{}{"bytes": 1500000, "id_last": idLast}, 10*time.Second)
			require.Error(t, err)
			require.Contains(t, err.Error(), "response from capped exceeded max_response_bytes (1048576)")
			require.Less(t, time.Since(start), 5*time.Second, "the oversized call must fail fast, not time out")

			wg.Wait()
			require.NoError(t, slowErr)
			require.Equal(t, "slow", resultTag(t, slowResp))

			resp, err := callTool(ctx, p, "capped", "echo", map[string]interface{}{"tag": "after"}, 5*time.Second)
			require.NoError(t, err)
			require.Equal(t, "after", resultTag(t, resp))

			// A response under the limit (the fake repeats the text twice) still
			// goes through.
			resp, err = callTool(ctx, p, "capped", "big", map[string]interface{}{"bytes": 400000}, 10*time.Second)
			require.NoError(t, err)
			require.Equal(t, 400000, bigText(t, resp))
		})
	}
}

// TestStdioRobust_HugeStderrLineDoesNotWedge: a child that writes a 200 KB
// stderr line (at startup and during a call) keeps working; the kept line
// is truncated.
func TestStdioRobust_HugeStderrLineDoesNotWedge(t *testing.T) {
	p := newTestPool(t)
	startFake(t, p, "noisy", fakeOpts{args: []string{"--stderr-bytes", "200000"}})
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		resp, err := callTool(ctx, p, "noisy", "stderr", map[string]interface{}{"bytes": 200000}, 5*time.Second)
		require.NoError(t, err, "call %d", i)
		require.Equal(t, "ok", resultTag(t, resp))
	}
	resp, err := callTool(ctx, p, "noisy", "echo", map[string]interface{}{"tag": "alive"}, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, "alive", resultTag(t, resp))

	server, err := p.GetServer("noisy")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return strings.Contains(server.stderrLines.String(), "[truncated 191808 bytes]")
	}, 5*time.Second, 10*time.Millisecond)
	for _, line := range strings.Split(server.stderrLines.String(), "\n") {
		require.LessOrEqual(t, len(line), maxStderrLineBytes+64)
	}
}

// TestStdioRobust_StderrIsRedacted: stderr lines are redacted before they
// are kept (and surfaced in error messages) or logged.
func TestStdioRobust_StderrIsRedacted(t *testing.T) {
	var logs lockedBuffer
	p := NewStdioPool(5, 0, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	p.stopGrace = testStopGrace
	t.Cleanup(func() { _ = p.Close() })
	startFake(t, p, "leaky", fakeOpts{})

	secret := "ghp_" + strings.Repeat("A", 36)
	_, err := callTool(context.Background(), p, "leaky", "stderr", map[string]interface{}{"text": "token=" + secret}, 5*time.Second)
	require.NoError(t, err)

	server, err := p.GetServer("leaky")
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return strings.Contains(server.stderrLines.String(), "token=")
	}, 5*time.Second, 10*time.Millisecond)
	require.NotContains(t, server.stderrLines.String(), secret)
	require.Contains(t, logs.String(), "server stderr")
	require.NotContains(t, logs.String(), secret)
}

// TestStdioRobust_ReaderDeathRestartsServer: a child that closes stdout but
// keeps running is never left "alive but unread": pending calls fail fast,
// the server is killed and restarted, and later calls succeed.
func TestStdioRobust_ReaderDeathRestartsServer(t *testing.T) {
	p := newTestPool(t)
	startFake(t, p, "mute", fakeOpts{})
	ctx := context.Background()

	server, err := p.GetServer("mute")
	require.NoError(t, err)
	gen := server.generation.Load()

	start := time.Now()
	_, err = callTool(ctx, p, "mute", "close_stdout", nil, 20*time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exited")
	require.Less(t, time.Since(start), 5*time.Second, "pending call must fail when the reader dies, not time out")

	require.Eventually(t, func() bool {
		return server.generation.Load() > gen && server.isHealthy()
	}, 15*time.Second, 20*time.Millisecond, "server must be killed and restarted")

	resp, err := callTool(ctx, p, "mute", "echo", map[string]interface{}{"tag": "back"}, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, "back", resultTag(t, resp))
}

// TestStdioRobust_BlockedStdinWriteHonorsDeadline: a child that stopped
// reading stdin cannot block callers past their deadline, neither the one
// stuck in Write nor those waiting for the writer.
func TestStdioRobust_BlockedStdinWriteHonorsDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pipe write deadlines are not supported on Windows")
	}
	p := newTestPool(t)
	startFake(t, p, "deaf", fakeOpts{})
	ctx := context.Background()

	resp, err := callTool(ctx, p, "deaf", "stop_reading", nil, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, "stopped", resultTag(t, resp))

	// Far bigger than any pipe buffer, so the write blocks mid-line.
	payload := strings.Repeat("p", 4<<20)
	const timeout = 500 * time.Millisecond
	var wg sync.WaitGroup
	errsCh := make(chan error, 3)
	durations := make(chan time.Duration, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start := time.Now()
			_, err := callTool(ctx, p, "deaf", "echo", map[string]interface{}{"tag": payload}, timeout)
			durations <- time.Since(start)
			errsCh <- err
		}()
	}
	wg.Wait()
	close(errsCh)
	close(durations)
	for err := range errsCh {
		require.Error(t, err)
	}
	for d := range durations {
		require.Less(t, d, 3*timeout+time.Second, "a blocked stdin write held a caller past its deadline")
	}

	// Stopping the wedged server is still prompt (the background write
	// finishing the interrupted line is released when the pipe closes).
	start := time.Now()
	require.NoError(t, p.StopServer("deaf"))
	require.Less(t, time.Since(start), testStopGrace+5*time.Second)
}

// TestStdioRobust_ParallelClose: Close with 5 servers that ignore SIGTERM
// takes about one grace period, not five.
func TestStdioRobust_ParallelClose(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGTERM is not delivered on Windows")
	}
	p := NewStdioPool(5, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.stopGrace = testStopGrace
	for i := 0; i < 5; i++ {
		startFake(t, p, fmt.Sprintf("stubborn-%d", i), fakeOpts{args: []string{"--ignore-sigterm"}})
	}
	// Make sure every child is up (and has installed its SIGTERM handler).
	for i := 0; i < 5; i++ {
		_, err := callTool(context.Background(), p, fmt.Sprintf("stubborn-%d", i), "echo", map[string]interface{}{"tag": "x"}, 5*time.Second)
		require.NoError(t, err)
	}

	start := time.Now()
	require.NoError(t, p.Close())
	elapsed := time.Since(start)
	require.GreaterOrEqual(t, elapsed, testStopGrace-100*time.Millisecond, "servers ignoring SIGTERM should get the grace period")
	require.Less(t, elapsed, 3*testStopGrace, "Close must stop servers in parallel (serial would take ~%v)", 5*testStopGrace)
	require.Equal(t, 0, p.ServerCount())
}

func TestLineReader(t *testing.T) {
	big := strings.Repeat("z", 300)
	input := "a\n\n" + big + "\n" + `{"id":7,"result":"` + big + `"}` + "\nlast"
	lr := newLineReader(strings.NewReader(input), 100)
	lr.r = bufio.NewReaderSize(strings.NewReader(input), 16)

	line, over, err := lr.next()
	require.NoError(t, err)
	require.Nil(t, over)
	require.Equal(t, "a", string(line))

	line, _, err = lr.next()
	require.NoError(t, err)
	require.Empty(t, line)

	_, over, err = lr.next()
	require.NoError(t, err)
	require.NotNil(t, over)
	require.Equal(t, int64(300), over.size)
	_, ok := oversizedResponseID(over)
	require.False(t, ok)

	_, over, err = lr.next()
	require.NoError(t, err)
	require.NotNil(t, over)
	id, ok := oversizedResponseID(over)
	require.True(t, ok)
	require.Equal(t, int64(7), id)

	line, over, err = lr.next()
	require.NoError(t, err)
	require.Nil(t, over)
	require.Equal(t, "last", string(line), "a final unterminated line is still delivered")

	_, _, err = lr.next()
	require.ErrorIs(t, err, io.EOF)
}

func TestOversizedResponseID(t *testing.T) {
	cases := []struct {
		head, tail string
		id         int64
		ok         bool
	}{
		{`{"jsonrpc":"2.0","id":12,"result":{"content":[{"text":"xx`, ``, 12, true},
		// A nested "id" in the result is not the response ID.
		{`{"jsonrpc":"2.0","result":{"id":3,"content":[{"text":"xx`, `xx"}]},"id":44}`, 44, true},
		{`{"jsonrpc":"2.0","result":{"id":3,"content":[{"text":"xx`, `xx","id":5}]}}`, 0, false},
		{`{"jsonrpc":"2.0","id":"abc","result":`, ``, 0, false},
		{`not json at all`, ``, 0, false},
	}
	for _, c := range cases {
		id, ok := oversizedResponseID(&oversizedLine{head: []byte(c.head), tail: []byte(c.tail)})
		require.Equal(t, c.ok, ok, c.head)
		require.Equal(t, c.id, id, c.head)
	}
}

func TestDrainStderrTruncatesAndNeverStops(t *testing.T) {
	var lines []string
	input := "short\n" + strings.Repeat("x", 3*maxStderrLineBytes) + "\r\nafter\nno-newline"
	drainStderr(strings.NewReader(input), func(line []byte, truncated int) {
		lines = append(lines, formatStderrLine(line, truncated))
	})
	require.Len(t, lines, 4)
	require.Equal(t, "short", lines[0])
	require.Equal(t, strings.Repeat("x", maxStderrLineBytes)+" ...[truncated 16385 bytes]", lines[1])
	require.Equal(t, "after", lines[2])
	require.Equal(t, "no-newline", lines[3])
}

func TestStdioConn_FailRequest(t *testing.T) {
	c := newStdioConn("unit", &lockedBuffer{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ch5, _ := c.register(5)
	ch9, _ := c.register(9)
	ch7, _ := c.register(7)

	id, ok := c.failRequest(9, true, fmt.Errorf("too big"))
	require.True(t, ok)
	require.Equal(t, int64(9), id)
	require.EqualError(t, (<-ch9).err, "too big")

	// Unknown ID: the oldest pending request is failed.
	id, ok = c.failRequest(100, true, fmt.Errorf("too big"))
	require.True(t, ok)
	require.Equal(t, int64(5), id)
	require.Error(t, (<-ch5).err)

	id, ok = c.failRequest(0, false, fmt.Errorf("too big"))
	require.True(t, ok)
	require.Equal(t, int64(7), id)
	require.Error(t, (<-ch7).err)

	_, ok = c.failRequest(0, false, fmt.Errorf("too big"))
	require.False(t, ok)
}
