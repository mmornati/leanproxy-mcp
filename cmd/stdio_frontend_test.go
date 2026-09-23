package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

// Generous bounds: CI runners are slow; these only need to separate
// "answered while another request is blocked" from "blocked behind it".
const (
	frontendWait = 5 * time.Second
)

// fakeFrontendHandler is a requestHandler whose methods block on channels:
//
//	block   wait for release (or ctx end; the id is then sent to canceled)
//	sleep   {"ms":N,"pad":P} sleep N ms, answer with P bytes of padding
//	*       answer {"method": <method>} immediately
type fakeFrontendHandler struct {
	release  chan struct{}
	canceled chan interface{}

	mu            sync.Mutex
	active        int
	maxActive     int
	calls         []string
	shutdownAfter int // active requests when shutdown was handled
}

func newFakeFrontendHandler() *fakeFrontendHandler {
	return &fakeFrontendHandler{
		release:       make(chan struct{}),
		canceled:      make(chan interface{}, 256),
		shutdownAfter: -1,
	}
}

func (h *fakeFrontendHandler) enter(method string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, method)
	if method == "block" || method == "sleep" {
		h.active++
		if h.active > h.maxActive {
			h.maxActive = h.active
		}
	}
}

func (h *fakeFrontendHandler) leave(method string) {
	if method == "block" || method == "sleep" {
		h.mu.Lock()
		h.active--
		h.mu.Unlock()
	}
}

func (h *fakeFrontendHandler) HandleRequest(ctx context.Context, req *mcp.Request) (*mcp.Response, error) {
	h.enter(req.Method)
	defer h.leave(req.Method)

	ok := func(v interface{}) (*mcp.Response, error) {
		b, _ := json.Marshal(v)
		return &mcp.Response{JSONRPC: mcp.JSONRPCVersion, Result: b, ID: req.ID}, nil
	}
	switch req.Method {
	case "block":
		select {
		case <-h.release:
			return ok(map[string]string{"method": "block"})
		case <-ctx.Done():
			h.canceled <- req.ID
			return &mcp.Response{JSONRPC: mcp.JSONRPCVersion, Error: mcp.NewError(mcp.ErrCodeInternalError, ctx.Err().Error()), ID: req.ID}, nil
		}
	case "sleep":
		var p struct {
			MS  int `json:"ms"`
			Pad int `json:"pad"`
		}
		_ = json.Unmarshal(req.Params, &p)
		time.Sleep(time.Duration(p.MS) * time.Millisecond)
		return ok(map[string]interface{}{"id": req.ID, "pad": strings.Repeat("x", p.Pad)})
	case mcp.MethodShutdown:
		h.mu.Lock()
		h.shutdownAfter = h.active
		h.mu.Unlock()
		return ok(map[string]string{"status": "shutdown"})
	default:
		return ok(map[string]string{"method": req.Method})
	}
}

func (h *fakeFrontendHandler) called(method string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c == method {
			return true
		}
	}
	return false
}

// frontendHarness drives serveStdio through pipes.
type frontendHarness struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan []byte
	done  chan error
}

func startFrontend(t *testing.T, h requestHandler, opts stdioFrontendOptions) *frontendHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	fh := &frontendHarness{t: t, in: inW, lines: make(chan []byte, 1024), done: make(chan error, 1)}

	go func() {
		err := serveStdio(context.Background(), inR, outW, h, opts)
		_ = outW.Close()
		fh.done <- err
	}()
	go func() {
		defer close(fh.lines)
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 16<<20)
		for sc.Scan() {
			fh.lines <- append([]byte(nil), sc.Bytes()...)
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-fh.done:
		case <-time.After(3 * frontendWait):
			t.Error("serveStdio did not return")
		}
	})
	return fh
}

func (fh *frontendHarness) send(msg string) {
	fh.t.Helper()
	if _, err := io.WriteString(fh.in, msg+"\n"); err != nil {
		fh.t.Fatalf("write request: %v", err)
	}
}

type frontendResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *mcp.Error      `json:"error"`
}

// next returns the next response line, parsed strictly.
func (fh *frontendHarness) next() frontendResp {
	fh.t.Helper()
	select {
	case line, ok := <-fh.lines:
		if !ok {
			fh.t.Fatal("output closed while waiting for a response")
		}
		var r frontendResp
		if err := json.Unmarshal(line, &r); err != nil {
			fh.t.Fatalf("output line is not valid JSON (%v): %q", err, line)
		}
		return r
	case <-time.After(frontendWait):
		fh.t.Fatal("timed out waiting for a response")
	}
	return frontendResp{}
}

// finish closes stdin, waits for serveStdio and returns any remaining lines.
func (fh *frontendHarness) finish() []frontendResp {
	fh.t.Helper()
	_ = fh.in.Close()
	select {
	case err := <-fh.done:
		if err != nil {
			fh.t.Fatalf("serveStdio() error = %v", err)
		}
		fh.done <- nil // let Cleanup observe completion
	case <-time.After(3 * frontendWait):
		fh.t.Fatal("serveStdio did not return after EOF")
	}
	rest := make([]frontendResp, 0, len(fh.lines))
	for line := range fh.lines {
		var r frontendResp
		if err := json.Unmarshal(line, &r); err != nil {
			fh.t.Fatalf("output line is not valid JSON (%v): %q", err, line)
		}
		rest = append(rest, r)
	}
	return rest
}

func TestStdioFrontend_SlowRequestDoesNotBlockOthers(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{})

	fh.send(`{"jsonrpc":"2.0","id":1,"method":"block"}`)
	fh.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)

	if r := fh.next(); string(r.ID) != "2" {
		t.Fatalf("first response id = %s, want 2 (ping must not wait for the blocked request)", r.ID)
	}
	close(h.release)
	if r := fh.next(); string(r.ID) != "1" || r.Error != nil {
		t.Fatalf("second response = %+v, want id 1 success", r)
	}
	if rest := fh.finish(); len(rest) != 0 {
		t.Fatalf("unexpected extra responses: %+v", rest)
	}
}

func TestStdioFrontend_CancelledNotification(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		requestID string
	}{
		{"numeric", `7`, `7`},
		{"numeric normalized", `7`, `7.0`},
		{"string", `"req-1"`, `"req-1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newFakeFrontendHandler()
			fh := startFrontend(t, h, stdioFrontendOptions{})

			fh.send(`{"jsonrpc":"2.0","id":` + tt.id + `,"method":"block"}`)
			// A request canceled before it starts is dropped without ever
			// reaching the handler; wait until it runs to exercise the
			// in-flight cancellation.
			waitCalled(t, h, "block")
			fh.send(`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":` + tt.requestID + `,"reason":"user"}}`)

			select {
			case <-h.canceled:
			case <-time.After(frontendWait):
				t.Fatal("request context was not canceled")
			}
			// The front end keeps serving after a cancellation.
			fh.send(`{"jsonrpc":"2.0","id":99,"method":"ping"}`)
			if r := fh.next(); string(r.ID) != "99" {
				t.Fatalf("response id = %s, want 99 (no response to the canceled request)", r.ID)
			}
			if rest := fh.finish(); len(rest) != 0 {
				t.Fatalf("canceled request must not be answered, got %+v", rest)
			}
		})
	}
}

func TestStdioFrontend_CancelDoesNotConfuseStringAndNumberIDs(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{})

	fh.send(`{"jsonrpc":"2.0","id":7,"method":"block"}`)
	fh.send(`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":"7"}}`)
	fh.send(`{"jsonrpc":"2.0","id":8,"method":"ping"}`)
	if r := fh.next(); string(r.ID) != "8" {
		t.Fatalf("response id = %s, want 8", r.ID)
	}
	select {
	case id := <-h.canceled:
		t.Fatalf("request %v was canceled by a notification naming a different id", id)
	default:
	}
	close(h.release)
	if r := fh.next(); string(r.ID) != "7" || r.Error != nil {
		t.Fatalf("response = %+v, want id 7 success", r)
	}
	fh.finish()
}

func TestStdioFrontend_NotificationsGetNoResponse(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{})

	fh.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fh.send(`{"jsonrpc":"2.0","method":"notifications/unknown"}`)
	fh.send(`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":12345}}`)
	fh.send(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)

	if r := fh.next(); string(r.ID) != "1" {
		t.Fatalf("first response id = %s, want 1 (notifications get no response)", r.ID)
	}
	if rest := fh.finish(); len(rest) != 0 {
		t.Fatalf("unexpected responses: %+v", rest)
	}
	if !h.called(mcp.MethodInitialized) {
		t.Error("notifications/initialized was not passed to the handler")
	}
}

func TestStdioFrontend_ParseErrorAndNullID(t *testing.T) {
	var logs bytes.Buffer
	var logsMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: &logsMu}, &slog.HandlerOptions{Level: slog.LevelDebug}))

	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{Logger: logger})

	const secret = "ghp_SUPERSECRETTOKEN0123456789"
	fh.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"token":"` + secret + `"`) // truncated JSON

	r := fh.next()
	if string(r.ID) != "null" {
		t.Fatalf("parse error id = %s, want null", r.ID)
	}
	if r.Error == nil || r.Error.Code != mcp.ErrCodeParseError {
		t.Fatalf("parse error response = %+v, want code %d", r, mcp.ErrCodeParseError)
	}

	fh.send(`{"jsonrpc":"2.0","id":null,"method":"ping"}`)
	r = fh.next()
	if string(r.ID) != "null" || r.Error != nil || r.Result == nil {
		t.Fatalf("id:null request response = %+v, want a result with id null", r)
	}
	fh.finish()

	logsMu.Lock()
	defer logsMu.Unlock()
	if strings.Contains(logs.String(), secret) || strings.Contains(logs.String(), "tools/call") {
		t.Fatalf("raw request line leaked into logs:\n%s", logs.String())
	}
	if !strings.Contains(logs.String(), "failed to parse JSON-RPC request") {
		t.Fatalf("parse failure was not logged:\n%s", logs.String())
	}
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// TestStdioFrontend_ConcurrentResponsesNeverInterleave sends 200 requests
// that finish in random order with large bodies and checks that every output
// line is one complete JSON-RPC response and every id is answered once.
func TestStdioFrontend_ConcurrentResponsesNeverInterleave(t *testing.T) {
	const n = 200
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{})

	go func() {
		for i := 0; i < n; i++ {
			msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"sleep","params":{"ms":%d,"pad":%d}}`, i, (i*7)%20, 4096+(i*131)%8192)
			if _, err := io.WriteString(fh.in, msg+"\n"); err != nil {
				return
			}
		}
	}()

	seen := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		r := fh.next()
		if r.Error != nil || r.Result == nil {
			t.Fatalf("response %d = %+v, want success", i, r)
		}
		var res struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(r.Result, &res); err != nil || string(res.ID) != string(r.ID) {
			t.Fatalf("result/id mismatch: id %s, result id %s (%v)", r.ID, res.ID, err)
		}
		if seen[string(r.ID)] {
			t.Fatalf("id %s answered twice", r.ID)
		}
		seen[string(r.ID)] = true
	}
	if rest := fh.finish(); len(rest) != 0 {
		t.Fatalf("unexpected extra responses: %d", len(rest))
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.maxActive < 2 {
		t.Errorf("max concurrent requests = %d, want > 1 (requests were not handled in parallel)", h.maxActive)
	}
}

func TestStdioFrontend_ConcurrencyCap(t *testing.T) {
	const n, limit = 12, 3
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{MaxConcurrent: limit})

	go func() {
		for i := 0; i < n; i++ {
			if _, err := io.WriteString(fh.in, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"sleep","params":{"ms":30}}`+"\n", i)); err != nil {
				return
			}
		}
	}()
	for i := 0; i < n; i++ {
		if r := fh.next(); r.Error != nil {
			t.Fatalf("response %+v, want success (the cap must make the reader wait, not reject)", r)
		}
	}
	fh.finish()
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.maxActive > limit {
		t.Fatalf("max concurrent requests = %d, want <= %d", h.maxActive, limit)
	}
}

// TestStdioFrontend_CancelWhileSaturated (#297 follow-up of #292): with
// max_concurrent_requests=1, a long call holds the only slot and another
// request is queued behind it; the reader must still read and apply a
// cancel for the long call promptly, instead of being stuck waiting for a
// slot. The queued request then runs, and a cancel for a request still
// waiting in the queue drops it without an answer.
func TestStdioFrontend_CancelWhileSaturated(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{MaxConcurrent: 1})

	fh.send(`{"jsonrpc":"2.0","id":1,"method":"block"}`)
	waitCalled(t, h, "block")
	// Queued behind the long call: the old reader blocked right here.
	fh.send(`{"jsonrpc":"2.0","id":2,"method":"sleep","params":{"ms":1}}`)
	fh.send(`{"jsonrpc":"2.0","id":3,"method":"sleep","params":{"ms":1}}`)
	fh.send(`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":3}}`)
	fh.send(`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":1}}`)

	select {
	case id := <-h.canceled:
		if fmt.Sprint(id) != "1" {
			t.Fatalf("canceled id = %v, want 1", id)
		}
	case <-time.After(frontendWait):
		t.Fatal("cancel for the request holding the only slot was not processed")
	}
	if r := fh.next(); string(r.ID) != "2" || r.Error != nil {
		t.Fatalf("response = %+v, want the queued request 2 answered after the slot freed", r)
	}
	if rest := fh.finish(); len(rest) != 0 {
		t.Fatalf("canceled requests must not be answered, got %+v", rest)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	sleeps := 0
	for _, c := range h.calls {
		if c == "sleep" {
			sleeps++
		}
	}
	if sleeps != 1 {
		t.Fatalf("handled %d sleep requests, want 1 (request 3 was canceled while queued)", sleeps)
	}
}

func waitCalled(t *testing.T, h *fakeFrontendHandler, method string) {
	t.Helper()
	deadline := time.Now().Add(frontendWait)
	for !h.called(method) {
		if time.Now().After(deadline) {
			t.Fatalf("%s was never handled", method)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestStdioFrontend_EOFDrainsInFlight(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{})

	fh.send(`{"jsonrpc":"2.0","id":1,"method":"sleep","params":{"ms":200}}`)
	rest := fh.finish()
	if len(rest) != 1 || string(rest[0].ID) != "1" || rest[0].Error != nil {
		t.Fatalf("responses after EOF = %+v, want the in-flight request answered", rest)
	}
}

func TestStdioFrontend_EOFCancelsAfterGrace(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{ShutdownGrace: 100 * time.Millisecond})

	fh.send(`{"jsonrpc":"2.0","id":1,"method":"block"}`)
	fh.finish()
	select {
	case id := <-h.canceled:
		if fmt.Sprint(id) != "1" {
			t.Fatalf("canceled id = %v, want 1", id)
		}
	default:
		t.Fatal("a request still running after the grace period was not canceled")
	}
}

func TestStdioFrontend_ShutdownDrainsThenStops(t *testing.T) {
	h := newFakeFrontendHandler()
	fh := startFrontend(t, h, stdioFrontendOptions{})

	// One write: the reader must stop after shutdown and never handle id 3.
	fh.send(`{"jsonrpc":"2.0","id":1,"method":"sleep","params":{"ms":150}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"shutdown"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`)

	if r := fh.next(); string(r.ID) != "1" {
		t.Fatalf("first response id = %s, want 1 (in-flight request drained before shutdown)", r.ID)
	}
	if r := fh.next(); string(r.ID) != "2" || r.Error != nil {
		t.Fatalf("second response = %+v, want shutdown result", r)
	}
	select {
	case err := <-fh.done:
		if err != nil {
			t.Fatalf("serveStdio() error = %v", err)
		}
		fh.done <- nil
	case <-time.After(frontendWait):
		t.Fatal("serveStdio did not return after shutdown")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.shutdownAfter != 0 {
		t.Fatalf("shutdown handled with %d requests still active, want 0", h.shutdownAfter)
	}
	for _, c := range h.calls {
		if c == "ping" {
			t.Fatal("request after shutdown was handled")
		}
	}
}

// TestStdioFrontend_OversizedLine checks that a line over
// server.max_line_bytes gets a JSON-RPC error with id null, is skipped up
// to its next newline, and the connection keeps serving afterwards (#304
// follow-up to #298's TCP listener cap).
func TestStdioFrontend_OversizedLine(t *testing.T) {
	h := newFakeFrontendHandler()
	const maxLine = 64
	fh := startFrontend(t, h, stdioFrontendOptions{MaxLineBytes: maxLine})

	oversized := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"pad":"` + strings.Repeat("x", maxLine*2) + `"}}`
	// Send the oversized line and a normal one right after, in one write,
	// so the test also proves the reader resynchronizes on the very next
	// newline rather than treating the tail of the discarded line (or the
	// next request) as garbage.
	fh.send(oversized + "\n" + `{"jsonrpc":"2.0","id":2,"method":"ping"}`)

	r := fh.next()
	if string(r.ID) != "null" {
		t.Fatalf("oversized-line response id = %s, want null", r.ID)
	}
	if r.Error == nil || r.Error.Code != mcp.ErrCodeParseError {
		t.Fatalf("oversized-line response = %+v, want a parse-error code", r)
	}

	r = fh.next()
	if string(r.ID) != "2" || r.Error != nil {
		t.Fatalf("response after the oversized line = %+v, want id 2 success (server kept serving)", r)
	}
	if rest := fh.finish(); len(rest) != 0 {
		t.Fatalf("unexpected extra responses: %+v", rest)
	}
	h.mu.Lock()
	pings := 0
	for _, c := range h.calls {
		if c == "ping" {
			pings++
		}
	}
	h.mu.Unlock()
	if pings != 1 {
		t.Fatalf("expected exactly one ping to reach the handler, got %d", pings)
	}
}

func TestRequestIDKey(t *testing.T) {
	a, _ := requestIDKey(float64(1))
	b, _ := requestIDKey("1")
	c, _ := requestIDKey(json.Number("1.0"))
	if a == b {
		t.Fatalf("number and string ids share key %q", a)
	}
	if a != c {
		t.Fatalf("1 and 1.0 keys differ: %q vs %q", a, c)
	}
	if _, ok := requestIDKey(nil); ok {
		t.Fatal("nil id must not be keyed")
	}
}
