package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/stretchr/testify/require"
)

var (
	concurrentMCPOnce sync.Once
	concurrentMCPDir  string
	concurrentMCPPath string
	concurrentMCPErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if concurrentMCPDir != "" {
		_ = os.RemoveAll(concurrentMCPDir)
	}
	os.Exit(code)
}

// concurrentMCPBinary builds testdata/concurrentmcp once per test binary and
// returns its path. Only the multiplexing tests pay the (cached) build cost.
func concurrentMCPBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and spawns a fake MCP server")
	}
	concurrentMCPOnce.Do(func() {
		dir, err := os.MkdirTemp("", "concurrentmcp-")
		if err != nil {
			concurrentMCPErr = err
			return
		}
		concurrentMCPDir = dir
		path := filepath.Join(dir, "concurrentmcp")
		out, err := exec.Command("go", "build", "-o", path, "./testdata/concurrentmcp").CombinedOutput()
		if err != nil {
			concurrentMCPErr = fmt.Errorf("go build testdata/concurrentmcp: %v\n%s", err, out)
			return
		}
		concurrentMCPPath = path
	})
	require.NoError(t, concurrentMCPErr)
	return concurrentMCPPath
}

// startConcurrentMCP starts a pool with one concurrentmcp server.
func startConcurrentMCP(t *testing.T, name string, maxInFlight int) *StdioPool {
	t.Helper()
	bin := concurrentMCPBinary(t)
	p := NewStdioPool(5, 0, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = p.Close() })
	require.NoError(t, p.StartServer(context.Background(), &migrate.ServerConfig{
		Name:         name,
		Transport:    registry.TransportStdio,
		Stdio:        &migrate.StdioConfig{Command: bin},
		TimeoutValue: 30 * time.Second,
		MaxInFlight:  maxInFlight,
	}))
	return p
}

func callTool(ctx context.Context, p *StdioPool, server, tool string, args map[string]interface{}, timeout time.Duration) (*Response, error) {
	params, err := json.Marshal(map[string]interface{}{"name": tool, "arguments": args})
	if err != nil {
		return nil, err
	}
	return p.SendRequestToServer(ctx, server, "tools/call", params, timeout)
}

func resultTag(t *testing.T, resp *Response) string {
	t.Helper()
	require.NotNil(t, resp)
	require.Nil(t, resp.Error, "unexpected upstream error")
	var r struct {
		Tag string `json:"tag"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &r))
	return r.Tag
}

// tagOf extracts the fake server's echoed tag without failing the test, so
// it is safe to call from worker goroutines.
func tagOf(resp *Response) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("nil response")
	}
	if resp.Error != nil {
		return "", resp.Error
	}
	var r struct {
		Tag string `json:"tag"`
	}
	if err := json.Unmarshal(resp.Result, &r); err != nil {
		return "", err
	}
	return r.Tag, nil
}

type fakeStats struct {
	Received      int64 `json:"received"`
	MaxActive     int64 `json:"maxActive"`
	Cancellations []struct {
		RequestID json.RawMessage `json:"requestId"`
		Reason    string          `json:"reason"`
	} `json:"cancellations"`
}

func getFakeStats(t *testing.T, p *StdioPool, server string) fakeStats {
	t.Helper()
	resp, err := callTool(context.Background(), p, server, "stats", nil, 5*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)
	var st fakeStats
	require.NoError(t, json.Unmarshal(resp.Result, &st))
	return st
}

// TestStdioMultiplex_ParallelCallsRunConcurrently: 50 parallel calls to a
// 100 ms tool on ONE stdio server complete in well under 50×100 ms.
func TestStdioMultiplex_ParallelCallsRunConcurrently(t *testing.T) {
	p := startConcurrentMCP(t, "mux-par", 0)
	ctx := context.Background()

	// Warm up so process start-up is not measured.
	_, err := callTool(ctx, p, "mux-par", "echo", map[string]interface{}{"tag": "warm"}, 5*time.Second)
	require.NoError(t, err)

	const n = 50
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tag := fmt.Sprintf("call-%d", i)
			resp, err := callTool(ctx, p, "mux-par", "sleep", map[string]interface{}{"ms": 100, "tag": tag}, 5*time.Second)
			if err != nil {
				errCh <- err
				return
			}
			if got, err := tagOf(resp); err != nil || got != tag {
				errCh <- fmt.Errorf("call %d got payload %q (%v)", i, got, err)
			}
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
	t.Logf("50 parallel 100ms calls on one stdio server took %v", elapsed)
	require.Less(t, elapsed, 300*time.Millisecond, "calls must be multiplexed, not serialized")
}

// TestStdioMultiplex_OutOfOrderResponses: the server answers in reverse
// order; every caller still receives its own payload.
func TestStdioMultiplex_OutOfOrderResponses(t *testing.T) {
	p := startConcurrentMCP(t, "mux-ooo", 0)
	ctx := context.Background()

	const n = 20
	var wg sync.WaitGroup
	got := make([]string, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Earlier callers sleep longer, so responses arrive reversed.
			resp, err := callTool(ctx, p, "mux-ooo", "sleep", map[string]interface{}{"ms": (n - i) * 10, "tag": fmt.Sprintf("caller-%d", i)}, 5*time.Second)
			if err == nil {
				got[i], err = tagOf(resp)
			}
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		require.NoError(t, errs[i])
		require.Equal(t, fmt.Sprintf("caller-%d", i), got[i], "caller %d received another caller's response", i)
	}
}

// TestStdioMultiplex_TimeoutSendsCancelledAndDropsLateResponse: a caller
// that times out makes the proxy send the MCP cancellation notification; its late
// response is discarded without affecting a concurrent caller.
func TestStdioMultiplex_TimeoutSendsCancelledAndDropsLateResponse(t *testing.T) {
	p := startConcurrentMCP(t, "mux-cancel", 0)
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)
	var otherResp *Response
	var otherErr error
	go func() {
		defer wg.Done()
		otherResp, otherErr = callTool(ctx, p, "mux-cancel", "sleep", map[string]interface{}{"ms": 300, "tag": "other"}, 5*time.Second)
	}()

	start := time.Now()
	_, err := callTool(ctx, p, "mux-cancel", "sleep", map[string]interface{}{"ms": 200, "tag": "late"}, 50*time.Millisecond)
	require.Error(t, err, "caller must time out")
	require.Contains(t, err.Error(), "request timeout")
	require.Less(t, time.Since(start), 150*time.Millisecond, "timed-out caller must return at its deadline")

	wg.Wait()
	require.NoError(t, otherErr)
	require.Equal(t, "other", resultTag(t, otherResp), "late response must not reach another caller")

	st := getFakeStats(t, p, "mux-cancel")
	require.Len(t, st.Cancellations, 1, "exactly one cancellation notification expected")
	require.Equal(t, "timeout", st.Cancellations[0].Reason)
	require.NotEmpty(t, st.Cancellations[0].RequestID)

	// The server is still healthy and serving after the late response.
	resp, err := callTool(ctx, p, "mux-cancel", "echo", map[string]interface{}{"tag": "after"}, 5*time.Second)
	require.NoError(t, err)
	require.Equal(t, "after", resultTag(t, resp))
}

// TestStdioMultiplex_DoneContextIsNeverWritten: a request whose context is
// already done before it is written never reaches the server.
func TestStdioMultiplex_DoneContextIsNeverWritten(t *testing.T) {
	p := startConcurrentMCP(t, "mux-done", 0)
	before := getFakeStats(t, p, "mux-done")

	server, err := p.GetServer("mux-done")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = server.sendRequest(ctx, Request{Method: "tools/call", Params: json.RawMessage(`{"name":"echo","arguments":{"tag":"x"}}`), ID: 1})
	require.ErrorIs(t, err, context.Canceled)

	after := getFakeStats(t, p, "mux-done")
	require.Equal(t, before.Received+1, after.Received, "only the second stats call may have reached the server")
}

// TestStdioMultiplex_ProcessDeathFailsAllPending: killing the child fails
// every pending call within 100 ms, not at its timeout.
func TestStdioMultiplex_ProcessDeathFailsAllPending(t *testing.T) {
	p := startConcurrentMCP(t, "mux-kill", 0)
	ctx := context.Background()
	server, err := p.GetServer("mux-kill")
	require.NoError(t, err)

	const n = 10
	type result struct {
		err error
		at  time.Time
	}
	results := make(chan result, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := callTool(ctx, p, "mux-kill", "hang", nil, 20*time.Second)
			results <- result{err: err, at: time.Now()}
		}()
	}

	require.Eventually(t, func() bool {
		server.mu.Lock()
		conn := server.conn
		server.mu.Unlock()
		return conn.inFlight() == n
	}, 5*time.Second, 5*time.Millisecond, "all calls must be pending")

	killedAt := time.Now()
	require.NoError(t, server.process.Process.Kill())

	for i := 0; i < n; i++ {
		select {
		case r := <-results:
			require.Error(t, r.err)
			require.Contains(t, r.err.Error(), "exited")
			require.Less(t, r.at.Sub(killedAt), 100*time.Millisecond, "pending call must fail right after the process dies")
		case <-time.After(2 * time.Second):
			t.Fatal("pending call did not fail after the process was killed")
		}
	}
	require.Equal(t, 0, server.inFlightCount())
}

// TestStdioMultiplex_ServerExitMidRequestFailsPending: a server that exits
// on its own (not killed by us) also fails every pending call immediately.
func TestStdioMultiplex_ServerExitMidRequestFailsPending(t *testing.T) {
	p := startConcurrentMCP(t, "mux-exit", 0)
	ctx := context.Background()

	pending := make(chan error, 1)
	go func() {
		_, err := callTool(ctx, p, "mux-exit", "hang", nil, 20*time.Second)
		pending <- err
	}()
	server, err := p.GetServer("mux-exit")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return server.inFlightCount() == 1 }, 5*time.Second, 5*time.Millisecond)

	start := time.Now()
	_, err = callTool(ctx, p, "mux-exit", "exit", map[string]interface{}{"code": 3}, 20*time.Second)
	require.Error(t, err)
	select {
	case err := <-pending:
		require.Error(t, err)
		require.Contains(t, err.Error(), "exited")
	case <-time.After(2 * time.Second):
		t.Fatal("pending call not failed after the server exited")
	}
	require.Less(t, time.Since(start), time.Second)
}

// TestStdioMultiplex_ServerToClientRequestGetsMethodNotFound: a request from
// the server (roots/list) is answered with -32601 so the server never hangs.
func TestStdioMultiplex_ServerToClientRequestGetsMethodNotFound(t *testing.T) {
	p := startConcurrentMCP(t, "mux-s2c", 0)
	resp, err := callTool(context.Background(), p, "mux-s2c", "ask_client", nil, 3*time.Second)
	require.NoError(t, err)
	require.Nil(t, resp.Error)

	var r struct {
		Reply struct {
			ID    string `json:"id"`
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		} `json:"reply"`
	}
	require.NoError(t, json.Unmarshal(resp.Result, &r), string(resp.Result))
	require.Equal(t, "srv-1", r.Reply.ID, "reply must carry the server's own request id")
	require.Equal(t, -32601, r.Reply.Error.Code)
}

// TestStdioMultiplex_MaxInFlightWaitsInsteadOfRejecting: at the cap,
// callers wait for a slot (respecting their context) rather than failing.
func TestStdioMultiplex_MaxInFlightWaitsInsteadOfRejecting(t *testing.T) {
	p := startConcurrentMCP(t, "mux-cap", 2)
	ctx := context.Background()

	const n = 6
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := callTool(ctx, p, "mux-cap", "sleep", map[string]interface{}{"ms": 50, "tag": fmt.Sprint(i)}, 5*time.Second)
			errCh <- err
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)
	close(errCh)
	for err := range errCh {
		require.NoError(t, err, "callers over the cap must wait, not be rejected")
	}
	require.GreaterOrEqual(t, elapsed, 150*time.Millisecond, "6 calls at cap 2 need 3 rounds of 50ms")

	st := getFakeStats(t, p, "mux-cap")
	require.LessOrEqual(t, st.MaxActive, int64(2), "server must never see more than max_in_flight concurrent calls")

	// A caller whose deadline expires while waiting for a slot fails with a
	// timeout and is never written to the server.
	server, err := p.GetServer("mux-cap")
	require.NoError(t, err)
	var hold sync.WaitGroup
	for i := 0; i < 2; i++ {
		hold.Add(1)
		go func() {
			defer hold.Done()
			_, _ = callTool(ctx, p, "mux-cap", "sleep", map[string]interface{}{"ms": 200}, 5*time.Second)
		}()
	}
	require.Eventually(t, func() bool { return server.inFlightCount() == 2 }, 2*time.Second, 2*time.Millisecond)

	_, err = callTool(ctx, p, "mux-cap", "echo", map[string]interface{}{"tag": "starved"}, 50*time.Millisecond)
	require.Error(t, err, "a caller waiting for a slot must honor its own timeout")
	require.Contains(t, err.Error(), "request timeout")

	hold.Wait()
	after := getFakeStats(t, p, "mux-cap")
	require.Equal(t, st.Received+3, after.Received, "the starved call must never have been written (2 held calls + 1 stats)")
}

// TestStdioMultiplex_Stress1000: 1,000 concurrent calls on one server under
// the race detector; every caller gets its own payload and the server never
// sees more than max_in_flight at once.
func TestStdioMultiplex_Stress1000(t *testing.T) {
	p := startConcurrentMCP(t, "mux-stress", 0)
	ctx := context.Background()

	const n = 1000
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tag := fmt.Sprintf("s-%d", i)
			resp, err := callTool(ctx, p, "mux-stress", "sleep", map[string]interface{}{"ms": i % 5, "tag": tag}, 20*time.Second)
			if err != nil {
				errCh <- fmt.Errorf("call %d: %w", i, err)
				return
			}
			if got, err := tagOf(resp); err != nil || got != tag {
				errCh <- fmt.Errorf("call %d got %q (%v)", i, got, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	failures := make([]string, 0, n)
	for err := range errCh {
		failures = append(failures, err.Error())
	}
	require.Empty(t, failures, strings.Join(failures, "\n"))

	st := getFakeStats(t, p, "mux-stress")
	require.LessOrEqual(t, st.MaxActive, int64(DefaultMaxInFlight))
	require.Equal(t, int64(n+1), st.Received)

	server, err := p.GetServer("mux-stress")
	require.NoError(t, err)
	require.Equal(t, 0, server.inFlightCount())
	require.Equal(t, StateIdle, server.getState())
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) Close() error { return nil }

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestStdioConn_HandleLine covers the reader's message classification
// without a process: responses reach exactly their waiter, notifications
// and unknown IDs are discarded, server requests get -32601, and fail()
// releases every waiter.
func TestStdioConn_HandleLine(t *testing.T) {
	stdin := &lockedBuffer{}
	c := newStdioConn("unit", stdin, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ch1, err := c.register(1)
	require.NoError(t, err)
	ch2, err := c.register(2)
	require.NoError(t, err)

	c.handleLine([]byte(`not json`))
	c.handleLine([]byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{}}`))
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":99,"result":{}}`))
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":"1","result":{}}`))
	require.Equal(t, 2, c.inFlight(), "noise must not consume pending entries")

	c.handleLine([]byte(`{"jsonrpc":"2.0","id":2,"error":{"code":-32602,"message":"bad"}}`))
	c.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`))
	r1 := <-ch1
	require.NoError(t, r1.err)
	require.JSONEq(t, `{"ok":true}`, string(r1.result))
	r2 := <-ch2
	require.Error(t, r2.err)
	require.Contains(t, r2.err.Error(), "bad")
	require.Equal(t, 0, c.inFlight())

	c.handleLine([]byte(`{"jsonrpc":"2.0","id":"srv-7","method":"sampling/createMessage","params":{}}`))
	require.Eventually(t, func() bool { return strings.Contains(stdin.String(), `"srv-7"`) }, time.Second, time.Millisecond)
	require.Contains(t, stdin.String(), `-32601`)

	ch3, err := c.register(3)
	require.NoError(t, err)
	c.fail(fmt.Errorf("boom"))
	require.EqualError(t, (<-ch3).err, "boom")
	_, err = c.register(4)
	require.EqualError(t, err, "boom", "a dead connection must refuse new requests")
}
