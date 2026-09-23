package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// Tests for the secured `serve` TCP listener (#298).

const testServeToken = "test-serve-token-0123456789abcdef"

func testServeConnOptions() serveConnOptions {
	return serveConnOptions{AuthToken: testServeToken}
}

func authLineFor(token string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"auth","params":{"token":%q}}`, token) + "\n"
}

// authedInput prefixes input with a valid auth handshake line.
func authedInput(input string) string {
	return authLineFor(testServeToken) + input
}

// countingPool is a fake upstream: it counts every forwarded call and runs
// an optional hook before answering.
type countingPool struct {
	calls atomic.Int64
	hook  func(ctx context.Context, req *proxy.JSONRPCRequest) error
}

func (p *countingPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	p.calls.Add(1)
	if p.hook != nil {
		if err := p.hook(ctx, req); err != nil {
			return nil, err
		}
	}
	return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{"ok":true}`), ID: req.ID}, nil
}

// startTestServe runs a serveListener on a loopback port and returns its
// address. The listener is closed when the test ends.
func startTestServe(t *testing.T, p Pool, opts serveConnOptions, maxConns int) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &serveListener{router: &mockRouter{}, gateway: &mockGatewayTools{}, pool: p, conn: opts, maxConns: maxConns}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v, want nil after close", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("Serve did not return after the listener was closed")
		}
	})
	return ln.Addr().String()
}

func dialTestServe(t *testing.T, addr string) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn, bufio.NewReader(conn)
}

func toolCallLine(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"fs.write_file","arguments":{"path":"/tmp/pwned"}}}`, id) + "\n"
}

// expectClosedSilently asserts the server closes conn without sending a
// single byte.
func expectClosedSilently(t *testing.T, conn net.Conn, r *bufio.Reader) {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	data, err := io.ReadAll(r)
	if len(data) != 0 {
		t.Fatalf("server answered an unauthenticated connection: %q", data)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("server did not close the unauthenticated connection")
	}
}

// TestServe_HTTPCrossProtocolPOST_NoUpstreamCall is the PoC regression test
// from the audit: a browser-style text/plain POST whose body is a JSON-RPC
// line must not reach any upstream server, with or without auth.
func TestServe_HTTPCrossProtocolPOST_NoUpstreamCall(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts serveConnOptions
	}{
		{"auth on", testServeConnOptions()},
		{"no-auth", serveConnOptions{NoAuth: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &countingPool{}
			addr := startTestServe(t, upstream, tc.opts, 0)

			client := &http.Client{Timeout: 15 * time.Second}
			resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader(toolCallLine(7)))
			if err == nil {
				resp.Body.Close()
				t.Fatalf("serve answered an HTTP request: %s", resp.Status)
			}

			// Positive control on a fresh connection: the same request
			// does reach the upstream once authenticated, so a zero count
			// above is meaningful.
			conn, r := dialTestServe(t, addr)
			if _, err := io.WriteString(conn, authLineFor(testServeToken)+toolCallLine(8)); err != nil {
				t.Fatal(err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
			line, err := r.ReadBytes('\n')
			if err != nil {
				t.Fatalf("authenticated call got no answer: %v", err)
			}
			if !bytes.Contains(line, []byte(`"id":8`)) {
				t.Fatalf("unexpected answer %s", line)
			}
			if got := upstream.calls.Load(); got != 1 {
				t.Fatalf("upstream calls = %d, want exactly 1 (the authenticated one); the HTTP POST reached the upstream", got)
			}
		})
	}
}

func TestServe_InvalidAuth_ClosedWithoutResponses(t *testing.T) {
	cases := []struct {
		name  string
		opts  serveConnOptions
		first string
	}{
		{"request without auth", testServeConnOptions(), toolCallLine(1)},
		{"wrong token", testServeConnOptions(), authLineFor("wrong-token-0123456789abcdef")},
		{"token prefix", testServeConnOptions(), authLineFor(testServeToken[:len(testServeToken)-1])},
		{"empty token", testServeConnOptions(), authLineFor("")},
		{"not json-rpc 2.0", testServeConnOptions(), fmt.Sprintf(`{"jsonrpc":"1.0","method":"auth","params":{"token":%q}}`, testServeToken) + "\n"},
		{"blank line", testServeConnOptions(), "\n"},
		{"garbage", testServeConnOptions(), "hello\n"},
		{"server without a token fails closed", serveConnOptions{}, authLineFor("")},
		{"HTTP request line", testServeConnOptions(), "GET / HTTP/1.1\r\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &countingPool{}
			addr := startTestServe(t, upstream, tc.opts, 0)
			conn, r := dialTestServe(t, addr)
			// The valid request lines after the bad first line must not
			// run either.
			if _, err := io.WriteString(conn, tc.first+toolCallLine(2)+`{"jsonrpc":"2.0","id":3,"method":"list_servers"}`+"\n"); err != nil {
				t.Fatal(err)
			}
			expectClosedSilently(t, conn, r)
			if got := upstream.calls.Load(); got != 0 {
				t.Fatalf("upstream calls = %d, want 0", got)
			}
		})
	}
}

func TestServe_AuthLineWithIDIsAnswered(t *testing.T) {
	addr := startTestServe(t, &countingPool{}, testServeConnOptions(), 0)
	conn, r := dialTestServe(t, addr)
	if _, err := io.WriteString(conn, fmt.Sprintf(`{"jsonrpc":"2.0","id":"a","method":"auth","params":{"token":%q}}`, testServeToken)+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, err := r.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(line, []byte(`"authenticated":true`)) || !bytes.Contains(line, []byte(`"id":"a"`)) {
		t.Fatalf("unexpected auth answer %s", line)
	}
}

func TestServe_NoAuth_FirstLineRunsAndAuthLineTolerated(t *testing.T) {
	upstream := &countingPool{}
	addr := startTestServe(t, upstream, serveConnOptions{NoAuth: true}, 0)

	conn, r := dialTestServe(t, addr)
	if _, err := io.WriteString(conn, toolCallLine(1)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if line, err := r.ReadBytes('\n'); err != nil || !bytes.Contains(line, []byte(`"id":1`)) {
		t.Fatalf("first request under --no-auth: %s %v", line, err)
	}

	conn2, r2 := dialTestServe(t, addr)
	if _, err := io.WriteString(conn2, authLineFor("anything-at-all-0123")+toolCallLine(2)); err != nil {
		t.Fatal(err)
	}
	_ = conn2.SetReadDeadline(time.Now().Add(15 * time.Second))
	if line, err := r2.ReadBytes('\n'); err != nil || !bytes.Contains(line, []byte(`"id":2`)) {
		t.Fatalf("request after an auth line under --no-auth: %s %v", line, err)
	}
	if got := upstream.calls.Load(); got != 2 {
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

// infiniteReader yields an endless stream of 'a' (no newline) and counts
// the bytes handed out.
type infiniteReader struct{ n atomic.Int64 }

func (r *infiniteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	r.n.Add(int64(len(p)))
	return len(p), nil
}

// TestReadBoundedLine_StopsAtCap: a newline-free stream is abandoned after
// about the cap, so memory never grows past it.
func TestReadBoundedLine_StopsAtCap(t *testing.T) {
	const maxBytes = 1 << 20
	src := &infiniteReader{}
	_, err := readBoundedLine(bufio.NewReaderSize(src, serveReadBufferSize), maxBytes)
	if !errors.Is(err, errLineTooLong) {
		t.Fatalf("err = %v, want errLineTooLong", err)
	}
	if got := src.n.Load(); got > maxBytes+2*serveReadBufferSize {
		t.Fatalf("read %d bytes for a %d-byte cap", got, maxBytes)
	}

	line, err := readBoundedLine(bufio.NewReaderSize(strings.NewReader("abc\ndef"), 16), 4)
	if err != nil || string(line) != "abc\n" {
		t.Fatalf("line = %q, %v", line, err)
	}
	if _, err := readBoundedLine(bufio.NewReaderSize(strings.NewReader("abcd\n"), 16), 4); !errors.Is(err, errLineTooLong) {
		t.Fatalf("5-byte line with a 4-byte cap: err = %v", err)
	}
	line, err = readBoundedLine(bufio.NewReaderSize(strings.NewReader("tail"), 16), 64)
	if !errors.Is(err, io.EOF) || string(line) != "tail" {
		t.Fatalf("partial line at EOF = %q, %v", line, err)
	}
}

// TestServe_OversizedLine_ClosesConnection sends up to 100 MB without a
// newline: the server gives up after about max_line_bytes and closes.
func TestServe_OversizedLine_ClosesConnection(t *testing.T) {
	upstream := &countingPool{}
	opts := testServeConnOptions()
	opts.MaxLineBytes = 1 << 20
	addr := startTestServe(t, upstream, opts, 0)
	conn, r := dialTestServe(t, addr)
	if _, err := io.WriteString(conn, authLineFor(testServeToken)); err != nil {
		t.Fatal(err)
	}

	const total = 100 << 20
	chunk := bytes.Repeat([]byte("a"), 64<<10)
	written := 0
	writeErr := make(chan error, 1)
	go func() {
		_ = conn.SetWriteDeadline(time.Now().Add(60 * time.Second))
		for written < total {
			n, err := conn.Write(chunk)
			written += n
			if err != nil {
				writeErr <- err
				return
			}
		}
		writeErr <- nil
	}()

	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	data, _ := io.ReadAll(r)
	if len(data) > 0 && !bytes.Contains(data, []byte("maximum size")) {
		t.Fatalf("unexpected answer to an oversized line: %q", data)
	}
	err := <-writeErr
	if err == nil {
		t.Fatalf("server accepted all %d bytes of a newline-free line (cap %d)", total, opts.MaxLineBytes)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatal("write timed out: the server never closed the connection")
	}
	if written >= total {
		t.Fatalf("wrote %d bytes, want the connection closed earlier", written)
	}
	if got := upstream.calls.Load(); got != 0 {
		t.Fatalf("upstream calls = %d, want 0", got)
	}
}

// TestServe_ConcurrencyCap: 10,000 requests sent at once never run more
// than max_concurrent_requests (64) handlers on the connection.
func TestServe_ConcurrencyCap(t *testing.T) {
	const (
		n       = 10000
		maxConc = 64
	)
	var peak atomic.Int64
	gate := make(chan struct{})
	var openGate sync.Once
	opts := testServeConnOptions()
	opts.MaxConcurrent = maxConc
	opts.onHandlerStart = func(active int64) {
		for {
			p := peak.Load()
			if active <= p || peak.CompareAndSwap(p, active) {
				break
			}
		}
		if active >= maxConc {
			openGate.Do(func() { close(gate) })
		}
	}
	upstream := &countingPool{hook: func(ctx context.Context, _ *proxy.JSONRPCRequest) error {
		// Hold every handler until the cap has been reached once, so the
		// test proves the cap is both reached and never exceeded.
		select {
		case <-gate:
		case <-time.After(30 * time.Second):
		}
		return nil
	}}
	addr := startTestServe(t, upstream, opts, 0)
	conn, r := dialTestServe(t, addr)

	go func() {
		var sb strings.Builder
		sb.WriteString(authLineFor(testServeToken))
		for i := 1; i <= n; i++ {
			sb.WriteString(toolCallLine(i))
		}
		_, _ = io.WriteString(conn, sb.String())
	}()

	_ = conn.SetReadDeadline(time.Now().Add(120 * time.Second))
	for i := 0; i < n; i++ {
		if _, err := r.ReadBytes('\n'); err != nil {
			t.Fatalf("read response %d: %v", i, err)
		}
	}
	if got := peak.Load(); got != maxConc {
		t.Fatalf("peak concurrent handlers = %d, want exactly %d", got, maxConc)
	}
	if got := upstream.calls.Load(); got != n {
		t.Fatalf("upstream calls = %d, want %d", got, n)
	}
}

// TestServe_DisconnectCancelsUpstream: closing the connection mid-call
// cancels the in-flight upstream call's context.
func TestServe_DisconnectCancelsUpstream(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	upstream := &countingPool{hook: func(ctx context.Context, _ *proxy.JSONRPCRequest) error {
		close(entered)
		select {
		case <-ctx.Done():
			close(canceled)
			return ctx.Err()
		case <-time.After(60 * time.Second):
			return nil
		}
	}}
	addr := startTestServe(t, upstream, testServeConnOptions(), 0)
	conn, _ := dialTestServe(t, addr)
	if _, err := io.WriteString(conn, authLineFor(testServeToken)+toolCallLine(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("request never reached the upstream")
	}
	_ = conn.Close()
	select {
	case <-canceled:
	case <-time.After(15 * time.Second):
		t.Fatal("upstream context was not canceled after the client disconnected")
	}
}

func TestServe_MaxConnections(t *testing.T) {
	addr := startTestServe(t, &countingPool{}, testServeConnOptions(), 1)
	first, r1 := dialTestServe(t, addr)
	if _, err := io.WriteString(first, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"auth","params":{"token":%q}}`, testServeToken)+"\n"); err != nil {
		t.Fatal(err)
	}
	_ = first.SetReadDeadline(time.Now().Add(15 * time.Second))
	if _, err := r1.ReadBytes('\n'); err != nil {
		t.Fatalf("first connection: %v", err)
	}

	second, r2 := dialTestServe(t, addr)
	_, _ = io.WriteString(second, authLineFor(testServeToken)+toolCallLine(2))
	expectClosedSilently(t, second, r2)

	// Once the first connection goes away its slot is reused.
	_ = first.Close()
	deadline := time.Now().Add(15 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(c, fmt.Sprintf(`{"jsonrpc":"2.0","id":3,"method":"auth","params":{"token":%q}}`, testServeToken)+"\n")
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, err = bufio.NewReader(c).ReadBytes('\n')
		_ = c.Close()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("slot was not released after the first connection closed: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// flakyListener fails Accept a few times, then reports it is closed.
type flakyListener struct {
	net.Listener
	failures int
	calls    atomic.Int64
}

func (l *flakyListener) Accept() (net.Conn, error) {
	if int(l.calls.Add(1)) <= l.failures {
		return nil, errors.New("accept: too many open files")
	}
	return nil, net.ErrClosed
}

func TestServeListener_AcceptBackoffAndClose(t *testing.T) {
	ln := &flakyListener{failures: 3}
	s := &serveListener{router: &mockRouter{}, gateway: &mockGatewayTools{}, pool: &countingPool{}, conn: testServeConnOptions()}
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve = %v, want nil on net.ErrClosed", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return on net.ErrClosed")
	}
	if got := ln.calls.Load(); got != 4 {
		t.Fatalf("Accept calls = %d, want 4 (3 failures then closed)", got)
	}

	want := []time.Duration{5 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond}
	var b time.Duration
	for _, w := range want {
		b = nextAcceptBackoff(b)
		if b != w {
			t.Fatalf("backoff = %v, want %v", b, w)
		}
	}
	for i := 0; i < 20; i++ {
		b = nextAcceptBackoff(b)
	}
	if b != acceptBackoffMax {
		t.Fatalf("backoff capped at %v, want %v", b, acceptBackoffMax)
	}
}

func TestLooksLikeHTTP(t *testing.T) {
	for line, want := range map[string]bool{
		"POST / HTTP/1.1":      true,
		"GET /x HTTP/1.0":      true,
		"OPTIONS * HTTP/1.1":   true,
		"CONNECT a:1 HTTP/1.1": true,
		"PRI * HTTP/2.0":       false,
		"garbage HTTP/1.1":     true,
		`{"jsonrpc":"2.0","method":"auth","params":{"token":"x"}}`:              false,
		`{"jsonrpc":"2.0","id":1,"method":"t","params":{"q":"GET / HTTP/1.1"}}`: false,
		`[{"jsonrpc":"2.0","id":1,"method":"t"}]`:                               false,
	} {
		if got := looksLikeHTTP([]byte(line)); got != want {
			t.Errorf("looksLikeHTTP(%q) = %v, want %v", line, got, want)
		}
	}
}

func TestServeAuthSettings(t *testing.T) {
	home := t.TempDir()
	for _, addr := range []string{"0.0.0.0:9000", ":8080", "[::]:1", "192.168.1.10:8080", "example.com:80", "garbage"} {
		if _, _, err := serveAuthSettings(addr, "", true, "", home); err == nil {
			t.Errorf("--no-auth on %q accepted, want refusal", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080", "127.0.0.2:1"} {
		tok, _, err := serveAuthSettings(addr, "", true, "", home)
		if err != nil || tok != "" {
			t.Errorf("--no-auth on %q: token %q, err %v", addr, tok, err)
		}
	}
	if _, _, err := serveAuthSettings("127.0.0.1:1", testServeToken, true, "", home); err == nil {
		t.Error("--no-auth with --auth-token accepted")
	}
	// Auth is still available on a non-loopback address.
	if tok, _, err := serveAuthSettings("0.0.0.0:9000", testServeToken, false, "", home); err != nil || tok != testServeToken {
		t.Errorf("auth on 0.0.0.0: %q %v", tok, err)
	}
	if _, err := os.Stat(serveTokenPath(home)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("token file created although a token was given or auth was off: %v", err)
	}
}

func TestResolveServeToken(t *testing.T) {
	home := t.TempDir()

	tok, src, err := resolveServeToken("", "", home)
	if err != nil {
		t.Fatal(err)
	}
	path := serveTokenPath(home)
	if src != path {
		t.Errorf("source = %q, want %q", src, path)
	}
	if len(tok) != 2*serveTokenBytes || strings.Trim(tok, "0123456789abcdef") != "" {
		t.Errorf("generated token %q is not %d hex characters", tok, 2*serveTokenBytes)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("token dir mode = %v, want 0700", dirInfo.Mode().Perm())
	}

	again, _, err := resolveServeToken("", "", home)
	if err != nil || again != tok {
		t.Fatalf("second start: token %q (err %v), want the stored %q", again, err, tok)
	}

	if got, src, _ := resolveServeToken("", "env-token-0123456789abcdef", home); got != "env-token-0123456789abcdef" || src != serveTokenEnv {
		t.Errorf("env token: %q from %q", got, src)
	}
	if got, _, _ := resolveServeToken("flag-token-0123456789abcdef", "env-token-0123456789abcdef", home); got != "flag-token-0123456789abcdef" {
		t.Errorf("flag token not preferred: %q", got)
	}
	if _, _, err := resolveServeToken("short", "", home); err == nil {
		t.Error("short --auth-token accepted")
	}
	if _, _, err := resolveServeToken("", "has space in the token value", home); err == nil {
		t.Error("token with whitespace accepted")
	}

	// A group/world-readable token file is tightened, not rejected.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, _, err := resolveServeToken("", "", home); err != nil || got != tok {
		t.Fatalf("loose-mode token file: %q %v", got, err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("token file mode after load = %v, want 0600", info.Mode().Perm())
	}

	// An empty token file is an error, never an empty token.
	if err := os.WriteFile(path, []byte("\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := resolveServeToken("", "", home); err == nil {
		t.Error("empty token file accepted")
	}
}
