package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGateway is a minimal Streamable HTTP MCP endpoint.
type fakeGateway struct {
	t  *testing.T
	mu sync.Mutex
	// sessions are the ids the gateway knows; forgetting them simulates a
	// restart.
	sessions map[string]bool
	next     int
	// seen records "<METHOD> <jsonrpc method>" per request, with the
	// session and protocol headers it carried.
	seen []string
	// push is written to the GET stream of each new listener.
	push    string
	deleted []string
}

func (g *fakeGateway) note(format string, args ...any) {
	g.mu.Lock()
	g.seen = append(g.seen, fmt.Sprintf(format, args...))
	g.mu.Unlock()
}

func (g *fakeGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer tok" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	session := r.Header.Get(headerSession)
	known := func() bool {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.sessions[session]
	}
	switch r.Method {
	case http.MethodDelete:
		g.mu.Lock()
		g.deleted = append(g.deleted, session)
		delete(g.sessions, session)
		g.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case http.MethodGet:
		if !known() {
			http.NotFound(w, r)
			return
		}
		g.note("GET %s", session)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if g.push != "" {
			fmt.Fprintf(w, "data: %s\n\n", g.push)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case http.MethodPost:
		body, _ := io.ReadAll(r.Body)
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.Unmarshal(body, &msg)
		if msg.Method == methodInitialize {
			g.mu.Lock()
			g.next++
			id := fmt.Sprintf("s%d", g.next)
			g.sessions[id] = true
			g.mu.Unlock()
			g.note("POST initialize -> %s", id)
			w.Header().Set(headerSession, id)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{}}}`, msg.ID)
			return
		}
		if !known() {
			http.NotFound(w, r)
			return
		}
		g.note("POST %s session=%s proto=%s", msg.Method, session, r.Header.Get(headerProtocol))
		switch {
		case msg.Method == "" || len(msg.ID) == 0:
			w.WriteHeader(http.StatusAccepted) // a notification, or the client's answer
		case msg.Method == "tools/call":
			// An SSE answer: a progress notification, then the result.
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"jsonrpc\":\"2.0\",\"method\":\"notifications/progress\",\"params\":{\"progress\":1}}\n\n")
			fmt.Fprintf(w, "event: message\ndata: {\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"content\":[]}}\n\n", msg.ID)
		case msg.Method == "boom":
			http.Error(w, "exploded", http.StatusInternalServerError)
		default:
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, "{\n  \"jsonrpc\": \"2.0\",\n  \"id\": %s,\n  \"result\": {}\n}", msg.ID)
		}
	}
}

// harness runs a bridge against a fake gateway and exposes the client's
// two pipes.
type harness struct {
	gw    *fakeGateway
	in    *io.PipeWriter
	lines chan string
	done  chan struct{}
}

func start(t *testing.T, token string, push string) *harness {
	t.Helper()
	gw := &fakeGateway{t: t, sessions: map[string]bool{}, push: push}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	b := New(Options{URL: srv.URL, Token: token, Logger: slog.New(slog.DiscardHandler)}, outW)
	h := &harness{gw: gw, in: inW, lines: make(chan string, 64), done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			h.lines <- sc.Text()
		}
	}()
	go func() {
		_ = b.Run(context.Background(), inR)
		outW.Close()
		close(h.done)
	}()
	t.Cleanup(func() { inW.Close() })
	return h
}

func (h *harness) send(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(h.in, line+"\n"); err != nil {
		t.Fatal(err)
	}
}

// expect waits for a line containing every part.
func (h *harness) expect(t *testing.T, parts ...string) string {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case line := <-h.lines:
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(line, p)
			}
			if ok {
				return line
			}
		case <-deadline:
			t.Fatalf("no line containing %q", parts)
		}
	}
}

func (h *harness) handshake(t *testing.T) {
	t.Helper()
	h.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"claude-ai","version":"1"}}}`)
	h.expect(t, `"id":1`, `"protocolVersion":"2025-06-18"`)
	h.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func (h *harness) sawRequest(want string) bool {
	h.gw.mu.Lock()
	defer h.gw.mu.Unlock()
	for _, s := range h.gw.seen {
		if strings.Contains(s, want) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestBridgeRelaysRequestsNotificationsAndStreams(t *testing.T) {
	h := start(t, "tok", `{"jsonrpc":"2.0","id":"srv-1","method":"elicitation/create","params":{}}`)
	h.handshake(t)

	// What the gateway initiates arrives on the listening stream.
	h.expect(t, `"id":"srv-1"`, `"method":"elicitation/create"`)
	// The client's answer goes back as a POST.
	h.send(t, `{"jsonrpc":"2.0","id":"srv-1","result":{"action":"accept"}}`)

	// A JSON answer, pretty-printed by the gateway, comes out as one line.
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if line := h.expect(t, `"id":2`); strings.Contains(line, "\n") || !strings.Contains(line, `"result":{}`) {
		t.Fatalf("answer not compacted to one line: %q", line)
	}
	// An SSE answer: the notification and the result both arrive.
	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"x"}}`)
	h.expect(t, `"method":"notifications/progress"`)
	h.expect(t, `"id":3`, `"result"`)

	// Session and negotiated protocol revision are sent after the handshake.
	if !h.sawRequest("POST tools/list session=s1 proto=2025-06-18") {
		t.Fatalf("headers not carried: %v", h.gw.seen)
	}
	waitFor(t, "the client's answer to reach the gateway", func() bool { return h.sawRequest("POST  session=s1") })

	// Closing stdin ends the gateway session.
	h.in.Close()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge did not stop when stdin closed")
	}
	if len(h.gw.deleted) != 1 || h.gw.deleted[0] != "s1" {
		t.Fatalf("session not deleted: %v", h.gw.deleted)
	}
}

// A gateway that restarted answers 404 for the old session: the bridge
// replays the client's handshake and the request still succeeds.
func TestBridgeReinitializesAfterAGatewayRestart(t *testing.T) {
	h := start(t, "tok", "")
	h.handshake(t)
	h.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	h.expect(t, `"id":2`)

	h.gw.mu.Lock()
	h.gw.sessions = map[string]bool{} // the restart
	h.gw.mu.Unlock()

	h.send(t, `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	h.expect(t, `"id":3`, `"result"`)
	if !h.sawRequest("POST initialize -> s2") || !h.sawRequest("POST notifications/initialized session=s2") ||
		!h.sawRequest("POST tools/list session=s2 proto=2025-06-18") {
		t.Fatalf("handshake not replayed on the new session: %v", h.gw.seen)
	}
	// The replayed initialize result is not sent to the client a second time.
	select {
	case line := <-h.lines:
		t.Fatalf("unexpected extra line: %s", line)
	case <-time.After(100 * time.Millisecond):
	}
}

// A request that cannot be delivered is answered with a JSON-RPC error
// carrying its id; a notification is not answered at all.
func TestBridgeAnswersFailedRequestsWithAnError(t *testing.T) {
	h := start(t, "tok", "")
	h.handshake(t)
	h.send(t, `{"jsonrpc":"2.0","id":7,"method":"boom"}`)
	line := h.expect(t, `"id":7`, `"error"`)
	if !strings.Contains(line, "500") || !strings.Contains(line, "exploded") {
		t.Fatalf("error does not explain the failure: %s", line)
	}

	bad := start(t, "wrong", "")
	bad.send(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if line := bad.expect(t, `"id":1`, `"error"`); !strings.Contains(line, "refused the token") {
		t.Fatalf("a 401 must say the token was refused: %s", line)
	}
	bad.send(t, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	bad.send(t, `not json`)
	select {
	case line := <-bad.lines:
		t.Fatalf("a failed notification or a garbage line was answered: %s", line)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestBridgeReportsAnUnreachableGateway(t *testing.T) {
	var out bytes.Buffer
	b := New(Options{URL: "http://127.0.0.1:1/mcp", Logger: slog.New(slog.DiscardHandler)}, &out)
	_ = b.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n"))
	if !strings.Contains(out.String(), `"id":1`) || !strings.Contains(out.String(), "daemon status") {
		t.Fatalf("no helpful error for an unreachable gateway: %s", out.String())
	}
}

func TestReadSSE(t *testing.T) {
	stream := "event: message\ndata: {\"a\":1}\n\n: comment\ndata: {\"b\":\ndata: 2}\r\n\r\ndata: {\"c\":3}"
	var got []string
	if err := readSSE(strings.NewReader(stream), func(d []byte) { got = append(got, string(d)) }); err != nil {
		t.Fatal(err)
	}
	want := []string{`{"a":1}`, "{\"b\":\n2}", `{"c":3}`}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}
