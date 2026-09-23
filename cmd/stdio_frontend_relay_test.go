package cmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// relaySource answers tools/call by asking the client for input through
// the handler's relay, the way a stdio upstream does mid-call (#308).
type relaySource struct {
	resourceSource
	h *mcp.Handler
}

func (s *relaySource) SendRequestToServer(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error) {
	if method != mcp.MethodToolsCall {
		return s.resourceSource.SendRequestToServer(ctx, name, method, params, timeout)
	}
	result, rpcErr := s.h.HandleServerRequest(context.Background(), name, pool.MethodElicitationCreate, json.RawMessage(`{"message":"name?"}`))
	if rpcErr != nil {
		return &pool.Response{Result: json.RawMessage(`{"content":[{"type":"text","text":"refused ` + rpcErr.Message + `"}]}`)}, nil
	}
	text, _ := json.Marshal(string(result))
	return &pool.Response{Result: json.RawMessage(`{"content":[{"type":"text","text":` + string(text) + `}]}`)}, nil
}

// stdioHarness runs serveStdio on pipes and reads its output lines.
type stdioHarness struct {
	t     *testing.T
	in    *io.PipeWriter
	lines chan string
	done  chan error
}

func startStdioHarness(t *testing.T, h requestHandler, opts stdioFrontendOptions) *stdioHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	s := &stdioHarness{t: t, in: inW, lines: make(chan string, 16), done: make(chan error, 1)}
	go func() { s.done <- serveStdio(context.Background(), inR, outW, h, opts) }()
	go func() {
		var buf []byte
		tmp := make([]byte, 4096)
		for {
			n, err := outR.Read(tmp)
			buf = append(buf, tmp[:n]...)
			for {
				i := strings.IndexByte(string(buf), '\n')
				if i < 0 {
					break
				}
				s.lines <- string(buf[:i])
				buf = buf[i+1:]
			}
			if err != nil {
				close(s.lines)
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = inW.Close()
		select {
		case <-s.done:
		case <-time.After(frontendWait):
			t.Error("serveStdio did not return")
		}
	})
	return s
}

func (s *stdioHarness) write(line string) {
	s.t.Helper()
	if _, err := io.WriteString(s.in, line+"\n"); err != nil {
		s.t.Fatal(err)
	}
}

func (s *stdioHarness) next() string {
	s.t.Helper()
	select {
	case l := <-s.lines:
		return l
	case <-time.After(frontendWait):
		s.t.Fatal("no output")
	}
	return ""
}

// TestServeStdio_RelaysServerRequestToClient (#308): a server-to-client
// request raised during a tool call is written to stdout under a proxy id;
// the client's answer is read and handled even though the only
// concurrency slot is held by the call waiting for it.
func TestServeStdio_RelaysServerRequestToClient(t *testing.T) {
	src := &relaySource{}
	h := mcp.NewHandler(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src.h = h
	s := startStdioHarness(t, h, stdioFrontendOptions{MaxConcurrent: 1})

	s.write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{"elicitation":{}},"clientInfo":{"name":"t","version":"1"}}}`)
	s.next()
	s.write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	s.write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"ask"}}}`)
	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	line := s.next()
	if err := json.Unmarshal([]byte(line), &req); err != nil || req.Method != "elicitation/create" || !strings.HasPrefix(req.ID, "lp-") {
		t.Fatalf("server-to-client request = %s (%v)", line, err)
	}
	if !strings.Contains(string(req.Params), `"message":"[docs] name?"`) {
		t.Fatalf("params = %s", req.Params)
	}

	// An answer to an unknown id is dropped silently (no error line).
	s.write(`{"jsonrpc":"2.0","id":"lp-999","result":{}}`)
	s.write(`{"jsonrpc":"2.0","id":"` + req.ID + `","result":{"action":"decline"}}`)
	line = s.next()
	if !strings.Contains(line, `"id":2`) || !strings.Contains(line, `decline`) {
		t.Fatalf("tool call = %s", line)
	}
	s.write(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if line = s.next(); !strings.Contains(line, `"id":3`) {
		t.Fatalf("ping = %s", line)
	}
}

// TestServeStdio_IncapableClientGetsNoRequests: without the capability the
// upstream is refused at once and nothing is written to the client.
func TestServeStdio_IncapableClientGetsNoRequests(t *testing.T) {
	src := &relaySource{}
	h := mcp.NewHandler(src, slog.New(slog.NewTextHandler(io.Discard, nil)))
	src.h = h
	s := startStdioHarness(t, h, stdioFrontendOptions{})
	s.write(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	s.next()
	s.write(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	s.write(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"ask"}}}`)
	if line := s.next(); !strings.Contains(line, `"id":2`) || !strings.Contains(line, "refused") {
		t.Fatalf("tool call = %s", line)
	}
}

func TestHandleControlLine(t *testing.T) {
	h := mcp.NewHandler(&resourceSource{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	session, closeSession := h.OpenSession(func(string, json.RawMessage) {})
	defer closeSession()
	sent := make(chan string, 1)
	session.EnableRequests(func(id, _ string, _ json.RawMessage) error {
		sent <- id
		return nil
	})
	inflight := &serveInflight{entries: map[string]*inflightRequest{}}

	// Not control lines.
	for _, line := range []string{`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`, `[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, `not json`, `{"jsonrpc":"2.0","method":"notifications/initialized"}`} {
		if handleControlLine([]byte(line), session, inflight) {
			t.Fatalf("%s handled as a control line", line)
		}
	}

	// A client answer is delivered to the pending request.
	got := make(chan json.RawMessage, 1)
	go func() {
		result, _ := session.Request(context.Background(), "roots/list", nil)
		got <- result
	}()
	var sentID string
	select {
	case sentID = <-sent:
	case <-time.After(frontendWait):
		t.Fatal("request not sent")
	}
	if !handleControlLine([]byte(`{"jsonrpc":"2.0","id":"`+sentID+`","result":{"roots":[]}}`), session, inflight) {
		t.Fatal("client answer not handled")
	}
	select {
	case r := <-got:
		if string(r) != `{"roots":[]}` {
			t.Fatalf("result = %s", r)
		}
	case <-time.After(frontendWait):
		t.Fatal("answer not delivered")
	}

	// A cancel marks and cancels the named request only.
	ctx, cancel := context.WithCancel(context.Background())
	entry := &inflightRequest{cancel: cancel}
	inflight.entries["n:5"] = entry
	if !handleControlLine([]byte(`{"jsonrpc":"2.0","method":"`+methodCancelled+`","params":{"requestId":6}}`), session, inflight) || ctx.Err() != nil {
		t.Fatal("cancel of another id canceled the request")
	}
	if !handleControlLine([]byte(`{"jsonrpc":"2.0","method":"`+methodCancelled+`","params":{"requestId":5}}`), session, inflight) || ctx.Err() == nil || !entry.canceledByClient.Load() {
		t.Fatal("cancel not applied")
	}
	if !handleControlLine([]byte(`{"jsonrpc":"2.0","method":"`+methodCancelled+`"}`), session, inflight) {
		t.Fatal("a malformed cancel is still consumed")
	}
}
