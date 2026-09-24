package streamhttp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

const testWait = 5 * time.Second

// testToken is built at runtime: never a literal credential in the tree.
var testToken = "stream" + strings.Repeat("0123456789", 3)

// fakeUpstream is a pool.ServerSource with one server, "docs". Its tools
// (the tool name of tools/call) behave like a real upstream would:
//
//   - ask: sends elicitation/create through the handler's relay, with no
//     caller context (as a stdio upstream does), and returns the answer;
//   - progress: sends three progress notifications with the proxy token;
//   - hang: blocks until canceled, and reports the cancellation;
//   - echo: returns its arguments.
type fakeUpstream struct {
	h        *mcp.Handler
	calls    atomic.Int64
	canceled chan struct{}
}

func (f *fakeUpstream) SendRequestToServer(ctx context.Context, name, method string, params json.RawMessage, _ time.Duration) (*pool.Response, error) {
	switch method {
	case mcp.MethodInitialize:
		return &pool.Response{Result: json.RawMessage(`{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"docs","version":"1"}}`)}, nil
	case mcp.MethodToolsList:
		return &pool.Response{Result: json.RawMessage(`{"tools":[{"name":"echo","description":"Echo","inputSchema":{"type":"object"}}]}`)}, nil
	case mcp.MethodToolsCall:
	default:
		return &pool.Response{Result: json.RawMessage(`{}`)}, nil
	}
	f.calls.Add(1)
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      struct {
			ProgressToken string `json:"progressToken"`
		} `json:"_meta"`
	}
	_ = json.Unmarshal(params, &p)
	text := func(s string) *pool.Response {
		b, _ := json.Marshal(s)
		return &pool.Response{Result: json.RawMessage(`{"content":[{"type":"text","text":` + string(b) + `}]}`)}
	}
	switch p.Name {
	case "ask":
		result, rpcErr := f.h.HandleServerRequest(context.Background(), name, pool.MethodElicitationCreate, json.RawMessage(`{"message":"name?","requestedSchema":{"type":"object"}}`))
		if rpcErr != nil {
			return text(fmt.Sprintf("refused %d %s", rpcErr.Code, rpcErr.Message)), nil
		}
		return text("answer " + string(result)), nil
	case "progress":
		for i := 1; i <= 3; i++ {
			f.h.HandleServerNotification(ctx, name, mcp.NotificationProgress,
				json.RawMessage(fmt.Sprintf(`{"progressToken":%q,"progress":%d,"total":3}`, p.Meta.ProgressToken, i)))
		}
		return text("progress done"), nil
	case "hang":
		<-ctx.Done()
		if f.canceled != nil {
			f.canceled <- struct{}{}
		}
		return nil, ctx.Err()
	default:
		return text("echo " + string(p.Arguments)), nil
	}
}

func (f *fakeUpstream) SendRequestToServerWithID(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration, _ int) (*pool.Response, error) {
	return f.SendRequestToServer(ctx, name, method, params, timeout)
}
func (f *fakeUpstream) SendServerNotification(context.Context, string, string, map[string]interface{}) error {
	return nil
}
func (f *fakeUpstream) ListServers() []string { return []string{"docs"} }
func (f *fakeUpstream) GetServerState(string) (pool.ServerState, error) {
	return pool.StateRunning, nil
}
func (f *fakeUpstream) GetServerTransport(string) (string, error)   { return "stdio", nil }
func (f *fakeUpstream) RestartServer(context.Context, string) error { return nil }
func (f *fakeUpstream) Close() error                                { return nil }

// countingHandler counts the requests that reach the handler.
type countingHandler struct {
	*mcp.Handler
	n atomic.Int64
}

func (c *countingHandler) HandleRequest(ctx context.Context, req *mcp.Request) (*mcp.Response, error) {
	c.n.Add(1)
	return c.Handler.HandleRequest(ctx, req)
}

type testServer struct {
	t   *testing.T
	srv *Server
	up  *fakeUpstream
	h   *countingHandler
	url string
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func startServer(t *testing.T, mod func(*Options)) *testServer {
	t.Helper()
	up := &fakeUpstream{canceled: make(chan struct{}, 4)}
	h := mcp.NewHandler(up, discard())
	up.h = h
	ch := &countingHandler{Handler: h}
	opts := Options{Addr: "127.0.0.1:0", Token: testToken, Logger: discard()}
	if mod != nil {
		mod(&opts)
	}
	srv, err := New(ch, opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), testWait)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testServer{t: t, srv: srv, up: up, h: ch, url: srv.URL()}
}

// request sends one HTTP request to the endpoint with the usual headers
// (bearer token, JSON body, both response types accepted), then applies
// hdr overrides ("" deletes a header).
func (ts *testServer) request(method, sid, body string, hdr map[string]string) *http.Response {
	ts.t.Helper()
	req, err := http.NewRequest(method, ts.url, strings.NewReader(body))
	if err != nil {
		ts.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
	} else if method == http.MethodGet {
		req.Header.Set("Accept", "text/event-stream")
	}
	if sid != "" {
		req.Header.Set(HeaderSessionID, sid)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		ts.t.Fatal(err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func expectStatus(t *testing.T, resp *http.Response, want int) string {
	t.Helper()
	body := readBody(t, resp)
	if resp.StatusCode != want {
		t.Fatalf("status %d, want %d: %s", resp.StatusCode, want, body)
	}
	return body
}

// initialize opens a session declaring caps and returns its id.
func (ts *testServer) initialize(version, caps string) string {
	ts.t.Helper()
	resp := ts.request(http.MethodPost, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"`+version+`","capabilities":`+caps+`,"clientInfo":{"name":"t","version":"1"}}}`, nil)
	body := expectStatus(ts.t, resp, http.StatusOK)
	sid := resp.Header.Get(HeaderSessionID)
	if len(sid) != 43 || !strings.Contains(body, `"protocolVersion":"`+version+`"`) {
		ts.t.Fatalf("initialize: session %q, body %s", sid, body)
	}
	expectStatus(ts.t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil), http.StatusAccepted)
	return sid
}

// sseEvents reads the data of each SSE event of body into a channel,
// closed at the end of the stream.
func sseEvents(body io.ReadCloser) <-chan string {
	out := make(chan string, 64)
	go func() {
		defer close(out)
		defer body.Close()
		r := bufio.NewReader(body)
		var data []string
		for {
			line, err := r.ReadString('\n')
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, "data: "):
				data = append(data, strings.TrimPrefix(line, "data: "))
			case line == "" && len(data) > 0:
				out <- strings.Join(data, "\n")
				data = nil
			}
			if err != nil {
				return
			}
		}
	}()
	return out
}

func nextEvent(t *testing.T, events <-chan string) string {
	t.Helper()
	select {
	case e, ok := <-events:
		if !ok {
			t.Fatal("stream ended")
		}
		return e
	case <-time.After(testWait):
		t.Fatal("no event")
	}
	return ""
}

const invokeAsk = `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"ask"}}}`

func TestNew_RefusesUnauthenticatedExposure(t *testing.T) {
	h := mcp.NewHandler(&fakeUpstream{}, discard())
	for _, addr := range []string{"0.0.0.0:8765", ":8765", "192.0.2.1:8765", "example.com:8765"} {
		if _, err := New(h, Options{Addr: addr}); !errors.Is(err, ErrUnauthenticatedExposure) {
			t.Errorf("New(%s, no token) = %v, want ErrUnauthenticatedExposure", addr, err)
		}
		if _, err := New(h, Options{Addr: addr, Token: testToken}); err != nil {
			t.Errorf("New(%s, token) = %v", addr, err)
		}
	}
	if _, err := New(h, Options{Addr: "127.0.0.1:8765"}); err != nil {
		t.Errorf("loopback without a token must be allowed: %v", err)
	}
	if _, err := New(h, Options{Addr: "nonsense"}); err == nil {
		t.Error("an invalid address must be refused")
	}
}

func TestSessionLifecycle(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{}`)
	if ts.srv.SessionCount() != 1 {
		t.Fatalf("sessions = %d", ts.srv.SessionCount())
	}

	resp := ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{HeaderProtocolVersion: "2025-11-25"})
	body := expectStatus(t, resp, http.StatusOK)
	if resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(body, `"search_tools"`) || !strings.Contains(body, `"id":2`) {
		t.Fatalf("tools/list = %s %s", resp.Header.Get("Content-Type"), body)
	}
	// Number ids are echoed exactly, string ids too.
	body = expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":12345678901234567,"method":"ping"}`, nil), http.StatusOK)
	if !strings.Contains(body, `"id":12345678901234567`) {
		t.Fatalf("ping = %s", body)
	}
	body = expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":"abc","method":"ping"}`, nil), http.StatusOK)
	if !strings.Contains(body, `"id":"abc"`) {
		t.Fatalf("ping = %s", body)
	}

	expectStatus(t, ts.request(http.MethodPost, "", `{"jsonrpc":"2.0","id":3,"method":"ping"}`, nil), http.StatusBadRequest)
	expectStatus(t, ts.request(http.MethodPost, "nope", `{"jsonrpc":"2.0","id":3,"method":"ping"}`, nil), http.StatusNotFound)

	expectStatus(t, ts.request(http.MethodDelete, sid, "", nil), http.StatusNoContent)
	expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":4,"method":"ping"}`, nil), http.StatusNotFound)
	expectStatus(t, ts.request(http.MethodDelete, sid, "", nil), http.StatusNotFound)
	if ts.srv.SessionCount() != 0 {
		t.Fatalf("sessions after DELETE = %d", ts.srv.SessionCount())
	}

	// A second initialize opens a distinct session.
	a, b := ts.initialize("2025-06-18", `{}`), ts.initialize("2025-03-26", `{}`)
	if a == b {
		t.Fatal("session ids must be unique")
	}
}

func TestInitializeFailureHandsOutNoSession(t *testing.T) {
	ts := startServer(t, nil)
	resp := ts.request(http.MethodPost, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":"bad"}`, nil)
	body := expectStatus(t, resp, http.StatusOK)
	if resp.Header.Get(HeaderSessionID) != "" || !strings.Contains(body, `"error"`) {
		t.Fatalf("failed initialize: session %q, %s", resp.Header.Get(HeaderSessionID), body)
	}
	if ts.srv.SessionCount() != 0 {
		t.Fatalf("a failed initialize left %d sessions", ts.srv.SessionCount())
	}
	// initialize must be alone and a request.
	expectStatus(t, ts.request(http.MethodPost, "", `[{"jsonrpc":"2.0","id":1,"method":"initialize"}]`, nil), http.StatusBadRequest)
	expectStatus(t, ts.request(http.MethodPost, "", `{"jsonrpc":"2.0","method":"initialize"}`, nil), http.StatusBadRequest)
}

func TestAuthentication(t *testing.T) {
	ts := startServer(t, nil)
	for name, hdr := range map[string]map[string]string{
		"missing token": {"Authorization": ""},
		"wrong token":   {"Authorization": "Bearer " + testToken + "x"},
		"wrong scheme":  {"Authorization": "Basic " + testToken},
	} {
		for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
			resp := ts.request(method, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`, hdr)
			expectStatus(t, resp, http.StatusUnauthorized)
			if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
				t.Errorf("%s %s: no WWW-Authenticate challenge", name, method)
			}
		}
	}
	if n := ts.h.n.Load(); n != 0 {
		t.Fatalf("%d unauthenticated requests reached the handler", n)
	}
}

func TestNoAuthOnLoopback(t *testing.T) {
	ts := startServer(t, func(o *Options) { o.Token = "" })
	resp := ts.request(http.MethodPost, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`, map[string]string{"Authorization": ""})
	expectStatus(t, resp, http.StatusOK)
}

func TestHostAndOriginValidation(t *testing.T) {
	ts := startServer(t, func(o *Options) {
		o.AllowedOrigins = []string{"https://app.example"}
		o.AllowedHosts = []string{"gateway.lan"}
	})
	port := ts.srv.Addr()[strings.LastIndex(ts.srv.Addr(), ":")+1:]
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`

	for name, hdr := range map[string]map[string]string{
		"rebinding host":        {"Host": "evil.example:" + port},
		"host without port":     {"Host": "127.0.0.1"},
		"evil origin":           {"Origin": "https://evil.example"},
		"null origin":           {"Origin": "null"},
		"loopback other port":   {"Origin": "http://127.0.0.1:1"},
		"evil origin, no token": {"Origin": "https://evil.example", "Authorization": ""},
	} {
		t.Run(name, func(t *testing.T) {
			expectStatus(t, ts.request(http.MethodPost, "", init, hdr), http.StatusForbidden)
			expectStatus(t, ts.request(http.MethodGet, "x", "", hdr), http.StatusForbidden)
		})
	}
	if n := ts.h.n.Load(); n != 0 {
		t.Fatalf("%d rejected requests reached the handler", n)
	}

	// Allowlisted origin: served, with CORS headers; its preflight too.
	resp := ts.request(http.MethodPost, "", init, map[string]string{"Origin": "https://app.example"})
	expectStatus(t, resp, http.StatusOK)
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://app.example" || !strings.Contains(resp.Header.Get("Access-Control-Expose-Headers"), HeaderSessionID) {
		t.Fatalf("CORS headers = %v", resp.Header)
	}
	pre := ts.request(http.MethodOptions, "", "", map[string]string{"Origin": "https://app.example", "Authorization": ""})
	expectStatus(t, pre, http.StatusNoContent)
	if !strings.Contains(pre.Header.Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatalf("preflight headers = %v", pre.Header)
	}
	// Same origin, localhost and an allowlisted host name.
	expectStatus(t, ts.request(http.MethodPost, "", init, map[string]string{"Origin": "http://" + ts.srv.Addr()}), http.StatusOK)
	expectStatus(t, ts.request(http.MethodPost, "", init, map[string]string{"Host": "localhost:" + port}), http.StatusOK)
	expectStatus(t, ts.request(http.MethodPost, "", init, map[string]string{"Host": "gateway.lan:" + port}), http.StatusOK)
}

func TestTransportErrors(t *testing.T) {
	ts := startServer(t, func(o *Options) { o.MaxBodyBytes = 1024 })
	sid := ts.initialize("2025-11-25", `{}`)
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`

	expectStatus(t, ts.request(http.MethodPost, sid, ping, map[string]string{"Content-Type": "text/plain"}), http.StatusUnsupportedMediaType)
	expectStatus(t, ts.request(http.MethodPost, sid, ping, map[string]string{"Accept": "text/html"}), http.StatusNotAcceptable)
	expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":2,"method":"ping","params":{"pad":"`+strings.Repeat("x", 2048)+`"}}`, nil), http.StatusRequestEntityTooLarge)
	expectStatus(t, ts.request(http.MethodPost, sid, `{not json`, nil), http.StatusBadRequest)
	expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0"}`, nil), http.StatusBadRequest)
	expectStatus(t, ts.request(http.MethodPost, sid, ping, map[string]string{HeaderProtocolVersion: "1999-01-01"}), http.StatusBadRequest)
	expectStatus(t, ts.request(http.MethodPost, sid, ping, map[string]string{HeaderProtocolVersion: "2025-06-18"}), http.StatusBadRequest)
	expectStatus(t, ts.request(http.MethodPut, sid, ping, nil), http.StatusMethodNotAllowed)
	expectStatus(t, ts.request(http.MethodGet, sid, "", map[string]string{"Accept": "application/json"}), http.StatusNotAcceptable)

	wrong, err := http.NewRequest(http.MethodPost, strings.TrimSuffix(ts.url, "/mcp")+"/other", strings.NewReader(ping))
	if err != nil {
		t.Fatal(err)
	}
	wrong.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(wrong)
	if err != nil {
		t.Fatal(err)
	}
	expectStatus(t, resp, http.StatusNotFound)

	// The session survives every rejected request.
	expectStatus(t, ts.request(http.MethodPost, sid, ping, nil), http.StatusOK)
}

func TestBatching(t *testing.T) {
	ts := startServer(t, nil)
	batch := `[{"jsonrpc":"2.0","id":1,"method":"ping"},{"jsonrpc":"2.0","method":"notifications/initialized"},{"jsonrpc":"2.0","id":"two","method":"ping"}]`

	old := ts.initialize("2025-03-26", `{}`)
	resp := ts.request(http.MethodPost, old, batch, map[string]string{"Accept": "application/json"})
	body := expectStatus(t, resp, http.StatusOK)
	var out []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &out); err != nil || len(out) != 2 {
		t.Fatalf("batch = %s (%v)", body, err)
	}
	// Notifications and responses only: 202.
	expectStatus(t, ts.request(http.MethodPost, old, `[{"jsonrpc":"2.0","method":"notifications/initialized"}]`, nil), http.StatusAccepted)

	current := ts.initialize("2025-06-18", `{}`)
	expectStatus(t, ts.request(http.MethodPost, current, batch, nil), http.StatusBadRequest)
}

func TestElicitationOnTheCallStream(t *testing.T) {
	// MaxConcurrent 1: the client's answer must not need a slot.
	ts := startServer(t, func(o *Options) { o.MaxConcurrent = 1 })
	sid := ts.initialize("2025-11-25", `{"elicitation":{}}`)

	resp := ts.request(http.MethodPost, sid, invokeAsk, nil)
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("call response: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	events := sseEvents(resp.Body)
	var req struct {
		ID     string          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	ev := nextEvent(t, events)
	if err := json.Unmarshal([]byte(ev), &req); err != nil || req.Method != "elicitation/create" || !strings.HasPrefix(req.ID, "lp-") ||
		!strings.Contains(string(req.Params), `[docs] name?`) {
		t.Fatalf("elicitation event = %s", ev)
	}
	answer := `{"jsonrpc":"2.0","id":"` + req.ID + `","result":{"action":"accept","content":{"name":"Ada"}}}`
	expectStatus(t, ts.request(http.MethodPost, sid, answer, nil), http.StatusAccepted)
	final := nextEvent(t, events)
	if !strings.Contains(final, `"id":7`) || !strings.Contains(final, `Ada`) {
		t.Fatalf("call result = %s", final)
	}
	if _, ok := <-events; ok {
		t.Fatal("the stream must end after the response")
	}
}

func TestElicitationWithoutCapabilityIsRefused(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{}`)
	resp := ts.request(http.MethodPost, sid, invokeAsk, nil)
	body := expectStatus(t, resp, http.StatusOK)
	if resp.Header.Get("Content-Type") != "application/json" || !strings.Contains(body, "refused -32601") {
		t.Fatalf("call = %s", body)
	}
}

func TestElicitationWithoutStreamFailsFast(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{"elicitation":{}}`)
	// A client that only takes JSON and has no GET stream open cannot be
	// asked: the upstream is answered at once.
	start := time.Now()
	body := expectStatus(t, ts.request(http.MethodPost, sid, invokeAsk, map[string]string{"Accept": "application/json"}), http.StatusOK)
	if !strings.Contains(body, "refused") || time.Since(start) > 2*time.Second {
		t.Fatalf("call = %s after %s", body, time.Since(start))
	}
}

func TestServerRequestOnGetStream(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{"elicitation":{}}`)
	get := ts.request(http.MethodGet, sid, "", nil)
	if get.StatusCode != http.StatusOK || get.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("GET = %d %s", get.StatusCode, get.Header.Get("Content-Type"))
	}
	events := sseEvents(get.Body)
	// Only one GET stream per session.
	expectStatus(t, ts.request(http.MethodGet, sid, "", nil), http.StatusConflict)

	// No call in flight: the relay picks the only capable session, and
	// the request travels on its GET stream.
	got := make(chan string, 1)
	go func() {
		result, rpcErr := ts.up.h.HandleServerRequest(context.Background(), "docs", pool.MethodElicitationCreate, json.RawMessage(`{"message":"idle?"}`))
		if rpcErr != nil {
			got <- rpcErr.Message
			return
		}
		got <- string(result)
	}()
	var req struct {
		ID string `json:"id"`
	}
	ev := nextEvent(t, events)
	if err := json.Unmarshal([]byte(ev), &req); err != nil || req.ID == "" {
		t.Fatalf("GET event = %s", ev)
	}
	expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":"`+req.ID+`","result":{"action":"decline"}}`, nil), http.StatusAccepted)
	select {
	case r := <-got:
		if !strings.Contains(r, "decline") {
			t.Fatalf("upstream got %s", r)
		}
	case <-time.After(testWait):
		t.Fatal("answer not delivered")
	}

	// DELETE ends the GET stream.
	expectStatus(t, ts.request(http.MethodDelete, sid, "", nil), http.StatusNoContent)
	for range events {
	}
}

func TestProgressOnTheCallStream(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{}`)
	call := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"progress"},"_meta":{"progressToken":42}}}`
	resp := ts.request(http.MethodPost, sid, call, nil)
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %s", resp.Header.Get("Content-Type"))
	}
	var progress int
	var last string
	for ev := range sseEvents(resp.Body) {
		last = ev
		if strings.Contains(ev, "notifications/progress") {
			if !strings.Contains(ev, `"progressToken":42`) {
				t.Fatalf("progress with a foreign token: %s", ev)
			}
			progress++
		}
	}
	if progress != 3 || !strings.Contains(last, `"id":8`) || !strings.Contains(last, "progress done") {
		t.Fatalf("progress = %d, last event %s", progress, last)
	}
}

func TestClientCancel(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{}`)
	done := make(chan *http.Response, 1)
	go func() {
		done <- ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","id":"h1","method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"hang"}}}`, nil)
	}()
	deadline := time.Now().Add(testWait)
	for ts.up.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	expectStatus(t, ts.request(http.MethodPost, sid, `{"jsonrpc":"2.0","method":"`+mcp.NotificationCancelled+`","params":{"requestId":"h1"}}`, nil), http.StatusAccepted)
	select {
	case <-ts.up.canceled:
	case <-time.After(testWait):
		t.Fatal("the upstream call was not canceled")
	}
	select {
	case resp := <-done:
		// No answer for a canceled request.
		if body := expectStatus(t, resp, http.StatusAccepted); body != "" {
			t.Fatalf("canceled request answered: %s", body)
		}
	case <-time.After(testWait):
		t.Fatal("canceled POST never ended")
	}
}

func TestDuplicateInflightID(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{}`)
	hang := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"hang"}}}`
	go func() { _ = readBody(t, ts.request(http.MethodPost, sid, hang, nil)) }()
	deadline := time.Now().Add(testWait)
	for ts.up.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	body := expectStatus(t, ts.request(http.MethodPost, sid, hang, nil), http.StatusOK)
	if !strings.Contains(body, "already in flight") {
		t.Fatalf("duplicate id = %s", body)
	}
}

func TestSessionCapAndIdleExpiry(t *testing.T) {
	ts := startServer(t, func(o *Options) {
		o.MaxSessions = 2
		o.SessionIdleTimeout = 300 * time.Millisecond
	})
	a := ts.initialize("2025-11-25", `{}`)
	b := ts.initialize("2025-11-25", `{}`)
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	resp := ts.request(http.MethodPost, "", init, nil)
	expectStatus(t, resp, http.StatusServiceUnavailable)
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("503 without Retry-After")
	}

	// b keeps a GET stream open: in use, it never expires. a does.
	get := ts.request(http.MethodGet, b, "", nil)
	events := sseEvents(get.Body)
	deadline := time.Now().Add(testWait)
	for ts.srv.SessionCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	expectStatus(t, ts.request(http.MethodPost, a, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, nil), http.StatusNotFound)
	time.Sleep(500 * time.Millisecond)
	expectStatus(t, ts.request(http.MethodPost, b, `{"jsonrpc":"2.0","id":2,"method":"ping"}`, nil), http.StatusOK)
	// Room again for a new session.
	ts.initialize("2025-11-25", `{}`)
	_ = get.Body.Close()
	for range events {
	}
}

func TestSessionBoundToPrincipal(t *testing.T) {
	ts := startServer(t, nil)
	sid := ts.initialize("2025-11-25", `{}`)
	r, err := http.NewRequest(http.MethodPost, ts.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+testToken)
	if ts.srv.lookup(sid, ts.srv.principalOf(r)) == nil {
		t.Fatal("the creating principal must find its session")
	}
	r.Header.Set("Authorization", "Bearer another-"+testToken)
	if ts.srv.lookup(sid, ts.srv.principalOf(r)) != nil {
		t.Fatal("another credential must not reach the session")
	}
}

func TestShutdownEndsStreams(t *testing.T) {
	up := &fakeUpstream{}
	h := mcp.NewHandler(up, discard())
	up.h = h
	srv, err := New(h, Options{Addr: "127.0.0.1:0", Token: testToken, Logger: discard()})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	ts := &testServer{t: t, srv: srv, up: up, url: srv.URL()}
	sid := ts.initialize("2025-11-25", `{}`)
	events := sseEvents(ts.request(http.MethodGet, sid, "", nil).Body)

	ctx, cancel := context.WithTimeout(context.Background(), testWait)
	defer cancel()
	start := time.Now()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("shutdown took %s with an open GET stream", d)
	}
	for range events {
	}
}

// TestNoGoroutineLeak opens and abandons many GET streams and calls
// (clients that disconnect mid-stream), then checks the goroutine count
// returns to its baseline.
func TestNoGoroutineLeak(t *testing.T) {
	ts := startServer(t, nil)
	settle := func() int {
		var n int
		for i := 0; i < 50; i++ {
			runtime.GC()
			n = runtime.NumGoroutine()
			time.Sleep(20 * time.Millisecond)
			if runtime.NumGoroutine() == n {
				break
			}
		}
		return n
	}
	sid := ts.initialize("2025-11-25", `{}`)
	http.DefaultClient.CloseIdleConnections()
	base := settle()

	for i := 0; i < 20; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+testToken)
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set(HeaderSessionID, sid)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %d: %d", i, resp.StatusCode)
		}
		cancel()
		_ = resp.Body.Close()
		// The server notices the disconnect and frees the stream slot.
		deadline := time.Now().Add(testWait)
		for {
			ts.srv.mu.Lock()
			sess := ts.srv.sessions[sid]
			ts.srv.mu.Unlock()
			sess.mu.Lock()
			free := sess.get == nil
			sess.mu.Unlock()
			if free {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("GET stream slot never freed after the client disconnected")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.url, strings.NewReader(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"docs","tool":"hang"}}}`, 100+i)))
			req.Header.Set("Authorization", "Bearer "+testToken)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(HeaderSessionID, sid)
			if resp, err := http.DefaultClient.Do(req); err == nil {
				_ = resp.Body.Close()
			}
		}(i)
	}
	wg.Wait()
	for i := 0; i < 10; i++ {
		select {
		case <-ts.up.canceled:
		case <-time.After(testWait):
			t.Fatal("a disconnected call was not canceled upstream")
		}
	}
	http.DefaultClient.CloseIdleConnections()

	deadline := time.Now().Add(testWait)
	for {
		n := settle()
		if n <= base+2 {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			t.Fatalf("goroutines: %d, baseline %d\n%s", n, base, buf[:runtime.Stack(buf, true)])
		}
	}
}
