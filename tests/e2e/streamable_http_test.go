package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
)

// End-to-end coverage for issue #309: the Streamable HTTP front end
// (`server run --http`) of the real binary, driven by the mcp-go
// Streamable HTTP client and by raw HTTP:
//
//   - initialize, tools/list, search_tools and invoke_tool with redaction,
//     resources and prompts, through the mcp-go client;
//   - elicitation and progress relayed from protomcp upstreams (the #308
//     scenarios re-run on this transport), and cancellation;
//   - the per-tool policy (deny, confirm through elicitation);
//   - auth failures (401), Host and Origin rejection (403) with the
//     upstream never called, --no-auth refused off loopback;
//   - the session lifecycle (Mcp-Session-Id, DELETE, 404 after);
//   - two concurrent clients sharing one set of child servers.

// httpProxy is a running `server run --http`.
type httpProxy struct {
	cmd  *exec.Cmd
	url  string
	addr string
	home string
	logs *syncBuffer
}

// startHTTPProxy starts `server run --http` on a free loopback port with
// the e2e token ($LEANPROXY_SERVE_TOKEN, set by TestMain) and a scratch
// HOME, and waits for the listener.
func startHTTPProxy(t *testing.T, proxyBin, cfg string, env []string, extra ...string) *httpProxy {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	home := t.TempDir()
	args := append([]string{"server", "run", "--http", addr, "--config", cfg}, extra...)
	cmd := exec.Command(proxyBin, args...)
	cmd.Env = append(append(os.Environ(), "HOME="+home), env...)
	logs := &syncBuffer{}
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server run --http: %v", err)
	}
	p := &httpProxy{cmd: cmd, url: "http://" + addr + "/mcp", addr: addr, home: home, logs: logs}
	t.Cleanup(func() {
		p.stop(t)
		if t.Failed() {
			t.Logf("server run --http logs:\n%s", logs.String())
		}
	})
	waitForPort(t, addr)
	return p
}

// stop sends SIGTERM and waits for a clean exit.
func (p *httpProxy) stop(t *testing.T) {
	t.Helper()
	if p.cmd.ProcessState != nil {
		return
	}
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("server run --http did not exit cleanly: %v", err)
		}
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
		t.Error("server run --http did not stop on SIGTERM")
	}
}

// rawRequest sends one HTTP request to the endpoint.
func (p *httpProxy) rawRequest(t *testing.T, method, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, p.url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func bearer() map[string]string { return map[string]string{"Authorization": "Bearer " + e2eServeToken} }

// httpProto adapts protoClient (the line-oriented e2e client of #307 and
// #308) to the Streamable HTTP transport: every message is POSTed with the
// session id; the JSON or SSE answers of requests, and the events of the
// GET stream, become its incoming lines. Requests are POSTed in the
// background, so a call waiting for its answer never blocks the client's
// own answer to a server-to-client request.
type httpProto struct {
	t     *testing.T
	url   string
	sid   atomic.Value
	lines chan string
	ctx   context.Context
	stop  context.CancelFunc
	wg    sync.WaitGroup
}

func newHTTPProto(t *testing.T, url string) (*httpProto, *protoClient) {
	ctx, cancel := context.WithCancel(context.Background())
	h := &httpProto{t: t, url: url, lines: make(chan string, 1024), ctx: ctx, stop: cancel}
	h.sid.Store("")
	t.Cleanup(func() {
		cancel()
		h.wg.Wait()
	})
	return h, &protoClient{
		t:    t,
		send: h.send,
		next: func(d time.Duration) (string, bool) {
			select {
			case l := <-h.lines:
				return l, true
			case <-time.After(d):
				return "", false
			}
		},
	}
}

func (h *httpProto) newRequest(method string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(h.ctx, method, h.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+e2eServeToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if sid := h.sid.Load().(string); sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	return req, nil
}

func (h *httpProto) send(line []byte) error {
	var m struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	body := bytes.TrimSpace(line)
	if err := json.Unmarshal(body, &m); err != nil {
		return err
	}
	req, err := h.newRequest(http.MethodPost, body)
	if err != nil {
		return err
	}
	if m.Method == "" || len(m.ID) == 0 {
		// A notification or an answer: 202, at once.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusAccepted {
			data, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("POST %s: %d %s", m.Method, resp.StatusCode, data)
		}
		return nil
	}
	if m.Method == "initialize" {
		// Synchronous: every later message needs the session id.
		return h.post(req)
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		if err := h.post(req); err != nil && h.ctx.Err() == nil {
			h.t.Logf("POST %s: %v", m.Method, err)
		}
	}()
	return nil
}

// post sends a request and feeds its answer (JSON or SSE) to lines.
func (h *httpProto) post(req *http.Request) error {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		h.sid.Store(sid)
	}
	switch {
	case resp.StatusCode == http.StatusAccepted:
		return nil // canceled by the client: no answer
	case strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream"):
		h.readSSE(resp.Body)
		return nil
	default:
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		h.lines <- string(bytes.TrimSpace(data))
		return nil
	}
}

func (h *httpProto) readSSE(r io.Reader) {
	br := bufio.NewReader(r)
	var data []string
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "data: "):
			data = append(data, strings.TrimPrefix(line, "data: "))
		case line == "" && len(data) > 0:
			h.lines <- strings.Join(data, "\n")
			data = nil
		}
		if err != nil {
			return
		}
	}
}

// openGet opens the session's GET stream in the background.
func (h *httpProto) openGet() {
	h.t.Helper()
	req, err := h.newRequest(http.MethodGet, nil)
	if err != nil {
		h.t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		h.t.Fatalf("GET stream: %d", resp.StatusCode)
	}
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		defer resp.Body.Close()
		h.readSSE(resp.Body)
	}()
}

// mcpGoClient connects the mcp-go Streamable HTTP client (continuous GET
// listening, an elicitation handler) and initializes it.
func mcpGoClient(t *testing.T, url, version string, elicit client.ElicitationHandler) (*client.Client, *notes) {
	t.Helper()
	tr, err := transport.NewStreamableHTTP(url,
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer " + e2eServeToken}),
		transport.WithContinuousListening())
	if err != nil {
		t.Fatal(err)
	}
	var opts []client.ClientOption
	if elicit != nil {
		opts = append(opts, client.WithElicitationHandler(elicit))
	}
	c := client.NewClient(tr, opts...)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	n := &notes{}
	c.OnNotification(n.add)
	if _, err := c.Initialize(ctx, mcpgo.InitializeRequest{Params: mcpgo.InitializeParams{
		ProtocolVersion: version,
		ClientInfo:      mcpgo.Implementation{Name: "e2e-http", Version: "1"},
	}}); err != nil {
		t.Fatalf("initialize over HTTP: %v", err)
	}
	return c, n
}

type notes struct {
	mu  sync.Mutex
	all []mcpgo.JSONRPCNotification
}

func (n *notes) add(m mcpgo.JSONRPCNotification) {
	n.mu.Lock()
	n.all = append(n.all, m)
	n.mu.Unlock()
}

func (n *notes) of(method string) []mcpgo.JSONRPCNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []mcpgo.JSONRPCNotification
	for _, m := range n.all {
		if m.Method == method {
			out = append(out, m)
		}
	}
	return out
}

type acceptElicitation struct {
	mu       sync.Mutex
	messages []string
}

func (a *acceptElicitation) Elicit(_ context.Context, req mcpgo.ElicitationRequest) (*mcpgo.ElicitationResult, error) {
	a.mu.Lock()
	a.messages = append(a.messages, req.Params.Message)
	a.mu.Unlock()
	return &mcpgo.ElicitationResult{ElicitationResponse: mcpgo.ElicitationResponse{
		Action: mcpgo.ElicitationResponseActionAccept, Content: map[string]any{"name": "Ada " + protoAWSKey},
	}}, nil
}

// texter extracts the first text item of a tool result, failing the test
// otherwise: tx.of(c.CallTool(...)).
type texter struct{ t *testing.T }

func (x texter) of(res *mcpgo.CallToolResult, err error) string {
	t := x.t
	t.Helper()
	if err != nil || res == nil || len(res.Content) == 0 {
		t.Fatalf("tool call: %v %+v", err, res)
	}
	text, ok := res.Content[0].(mcpgo.TextContent)
	if !ok {
		t.Fatalf("first content item is not text: %+v", res.Content[0])
	}
	return text.Text
}

func invokeReq(server, tool string, args map[string]any, meta *mcpgo.Meta) mcpgo.CallToolRequest {
	a := map[string]any{"server": server, "tool": tool}
	if args != nil {
		a["arguments"] = args
	}
	return mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{Name: "invoke_tool", Arguments: a, Meta: meta}}
}

// TestStreamableHTTP_MCPGoClient is the issue's first acceptance
// criterion, plus the #307/#308 features, through the mcp-go client.
func TestStreamableHTTP_MCPGoClient(t *testing.T) {
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeRelayConfig(t, fmt.Sprintf("hg%d", os.Getpid()), fakeBin)
	p := startHTTPProxy(t, proxyBin, cfg, nil)

	elicit := &acceptElicitation{}
	c, n := mcpGoClient(t, p.url, "2025-11-25", elicit)
	tx := texter{t}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	tools, err := c.ListTools(ctx, mcpgo.ListToolsRequest{})
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("tools/list: %v %+v", err, tools)
	}
	names := map[string]bool{}
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"search_tools", "invoke_tool", "list_tools"} {
		if !names[want] {
			t.Fatalf("tools/list lacks %s: %v", want, names)
		}
	}

	// invoke_tool, redacted: protomcp's rich tool returns an AWS key.
	deadline := time.Now().Add(20 * time.Second)
	var rich string
	for {
		res, err := c.CallTool(ctx, invokeReq(a, "rich", nil, nil))
		if err == nil && !res.IsError && len(res.Content) > 0 {
			rich = texter{t}.of(res, err)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("invoke_tool rich never succeeded: %v %+v", err, res)
		}
		time.Sleep(150 * time.Millisecond)
	}
	if strings.Contains(rich, protoAWSKey) || !strings.HasPrefix(rich, "key ") {
		t.Fatalf("invoke_tool result not redacted: %q", rich)
	}

	// search_tools finds an upstream tool.
	search := tx.of(c.CallTool(ctx, mcpgo.CallToolRequest{Params: mcpgo.CallToolParams{
		Name: "search_tools", Arguments: map[string]any{"query": "delete a repository"},
	}}))
	if !strings.Contains(search, "delete_repo") {
		t.Fatalf("search_tools: %s", search)
	}

	// Resources and prompts of both upstreams, namespaced.
	resources, err := c.ListResources(ctx, mcpgo.ListResourcesRequest{})
	if err != nil || len(resources.Resources) != 4 {
		t.Fatalf("resources/list: %v %+v", err, resources)
	}
	uri := "leanproxy://" + a + "/file:///" + a + "/notes.txt"
	read, err := c.ReadResource(ctx, mcpgo.ReadResourceRequest{Params: mcpgo.ReadResourceParams{URI: uri}})
	if err != nil || len(read.Contents) != 1 {
		t.Fatalf("resources/read: %v %+v", err, read)
	}
	if raw, _ := json.Marshal(read.Contents[0]); strings.Contains(string(raw), protoAWSKey) {
		t.Fatalf("resources/read not redacted: %s", raw)
	}
	prompts, err := c.ListPrompts(ctx, mcpgo.ListPromptsRequest{})
	if err != nil || len(prompts.Prompts) != 2 {
		t.Fatalf("prompts/list: %v %+v", err, prompts)
	}
	if _, err := c.GetPrompt(ctx, mcpgo.GetPromptRequest{Params: mcpgo.GetPromptParams{Name: b + ".greet", Arguments: map[string]string{"who": "Ada"}}}); err != nil {
		t.Fatalf("prompts/get: %v", err)
	}

	// Elicitation relayed mid-call over the call's SSE stream (the
	// redaction of the answer is checked by the relay scenarios).
	text := tx.of(c.CallTool(ctx, invokeReq(a, "ask_user", nil, nil)))
	if !strings.Contains(text, `"action":"accept"`) || !strings.Contains(text, "Ada") {
		t.Fatalf("ask_user: %s", text)
	}
	elicit.mu.Lock()
	shown := append([]string(nil), elicit.messages...)
	elicit.mu.Unlock()
	if len(shown) != 1 || shown[0] != "["+a+"] What is your name?" {
		t.Fatalf("elicitation shown = %q", shown)
	}

	// Progress with the client's own token, before the result.
	start := time.Now()
	text = tx.of(c.CallTool(ctx, invokeReq(a, "slow", nil, &mcpgo.Meta{ProgressToken: "http-tok"})))
	if time.Since(start) < 1500*time.Millisecond || !strings.Contains(text, "lp-progress-") {
		t.Fatalf("slow: %s after %s", text, time.Since(start))
	}
	progress := n.of("notifications/progress")
	if len(progress) < 2 {
		t.Fatalf("got %d progress notifications, want >= 2", len(progress))
	}
	for _, m := range progress {
		if tok := m.Params.AdditionalFields["progressToken"]; tok != "http-tok" {
			t.Fatalf("progress with token %v", tok)
		}
	}

	// A client that gives up (closes the request) cancels the upstream
	// call: the transport has no resumability, so the result could never
	// be delivered.
	hangCtx, hangCancel := context.WithTimeout(ctx, 700*time.Millisecond)
	_, err = c.CallTool(hangCtx, invokeReq(a, "hang", nil, nil))
	hangCancel()
	if err == nil {
		t.Fatal("the abandoned call returned a result")
	}
	if text = tx.of(c.CallTool(ctx, invokeReq(a, "last_cancel", nil, nil))); strings.HasSuffix(text, "last canceled: ") {
		t.Fatalf("the upstream never saw the cancellation: %s", text)
	}

	// Every revision LeanProxy negotiates works over HTTP.
	for _, v := range []string{"2025-03-26", "2025-06-18"} {
		cv, _ := mcpGoClient(t, p.url, v, nil)
		if _, err := cv.ListTools(ctx, mcpgo.ListToolsRequest{}); err != nil {
			t.Fatalf("%s tools/list: %v", v, err)
		}
	}
}

// TestStreamableHTTP_RelayScenarios re-runs the #308 relay scenarios
// (elicitation, sampling, roots, progress, both cancellations, resource
// updates) on the HTTP transport, with a capable and an incapable client
// connected at once.
func TestStreamableHTTP_RelayScenarios(t *testing.T) {
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeRelayConfig(t, fmt.Sprintf("hr%d", os.Getpid()), fakeBin)
	p := startHTTPProxy(t, proxyBin, cfg, nil)

	capH, capable := newHTTPProto(t, p.url)
	res := protoInitializeWith(t, capable, relayCaps)
	if !strings.Contains(string(res["capabilities"]), `"subscribe":true`) {
		t.Fatalf("resources.subscribe not advertised: %s", res["capabilities"])
	}
	capH.openGet()
	incH, incapable := newHTTPProto(t, p.url)
	protoInitializeWith(t, incapable, map[string]interface{}{})
	incH.openGet()

	// The relay tools are known after the first background refresh.
	deadline := time.Now().Add(20 * time.Second)
	for {
		m := relayFrontEnd(viaInvokeTool).call(capable, b, "client_caps", nil)
		if m.Error == nil && !strings.Contains(m.raw, `"isError":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("relay tools never callable: %s", m.raw)
		}
		time.Sleep(150 * time.Millisecond)
	}

	checkIncapable(t, incapable, viaInvokeTool, a)
	if n := len(requestsOf(capable, 0, "elicitation/create")); n != 0 {
		t.Fatalf("another session's elicitation reached the capable client (%d)", n)
	}
	checkRelay(t, capable, viaInvokeTool, a, b)
}

// TestStreamableHTTP_Policy runs the per-tool policy (#314) over HTTP:
// deny, confirm through elicitation (approve, deny), and a client without
// elicitation refused; refused calls never reach the upstream.
func TestStreamableHTTP_Policy(t *testing.T) {
	e := newPinEnv(t, "polh")
	writePolicyUpstream(e)
	cfg := e.policyConfig()
	p := startHTTPProxy(t, e.proxyBin, cfg, e.env())

	_, pc := newHTTPProto(t, p.url)
	c := newPolicyClient(pc)
	protoInitializeWith(t, c.protoClient, map[string]interface{}{"elicitation": map[string]interface{}{}})
	waitListed(t, c.protoClient, e.server)

	requirePolicyRefusal(t, c.call("tools/call", invoke(e.server, "delete_all")), "deny glob", "rules[1]")
	requirePolicyRefusal(t, c.call("tools/call", invoke(e.server, "drop_tables")), "unknown tool", `does not advertise a tool named "drop_tables"`)
	if m := c.call("tools/call", invoke(e.server, "echo")); m.Error != nil {
		t.Fatalf("echo must be allowed: %s", m.raw)
	}
	c.set("approve")
	if m := c.call("tools/call", invoke(e.server, "add")); m.Error != nil {
		t.Fatalf("approved add: %s", m.raw)
	}
	c.set("deny")
	requirePolicyRefusal(t, c.call("tools/call", invoke(e.server, "add")), "confirm deny", "requires confirmation")
	if n := c.askedCount(); n != 2 {
		t.Fatalf("confirmations asked: %d, want 2", n)
	}

	_, incapable := newHTTPProto(t, p.url)
	protoInitializeWith(t, incapable, map[string]interface{}{})
	requirePolicyRefusal(t, incapable.call("tools/call", invoke(e.server, "add")), "no elicitation", "does not support MCP elicitation")

	if calls := strings.Fields(e.upstreamCalls()); strings.Join(calls, ",") != "echo,add" {
		t.Fatalf("upstream calls %v: refused calls must never reach the upstream", calls)
	}
}

// TestStreamableHTTP_Security covers the issue's security criteria against
// the real binary; the upstream's call log proves no rejected request
// reached it.
func TestStreamableHTTP_Security(t *testing.T) {
	e := newPinEnv(t, "sech")
	writePolicyUpstream(e)
	cfg := e.policyConfig()
	p := startHTTPProxy(t, e.proxyBin, cfg, e.env())
	port := p.addr[strings.LastIndex(p.addr, ":")+1:]

	initBody := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"sec","version":"1"}}}`
	resp, _ := p.rawRequest(t, http.MethodPost, initBody, bearer())
	sid := resp.Header.Get("Mcp-Session-Id")
	if resp.StatusCode != http.StatusOK || len(sid) < 40 {
		t.Fatalf("initialize: %d, session id %q", resp.StatusCode, sid)
	}
	withSID := func(h map[string]string) map[string]string {
		out := map[string]string{"Mcp-Session-Id": sid}
		for k, v := range h {
			out[k] = v
		}
		return out
	}
	echo := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"` + e.server + `","tool":"echo","arguments":{"text":"x"}}}}`

	for name, tc := range map[string]struct {
		hdr  map[string]string
		want int
	}{
		"evil origin":   {withSID(map[string]string{"Authorization": "Bearer " + e2eServeToken, "Origin": "https://evil.example"}), http.StatusForbidden},
		"evil host":     {withSID(map[string]string{"Authorization": "Bearer " + e2eServeToken, "Host": "evil.example:" + port}), http.StatusForbidden},
		"missing token": {withSID(nil), http.StatusUnauthorized},
		"wrong token":   {withSID(map[string]string{"Authorization": "Bearer " + e2eServeToken + "-wrong"}), http.StatusUnauthorized},
	} {
		resp, body := p.rawRequest(t, http.MethodPost, echo, tc.hdr)
		if resp.StatusCode != tc.want {
			t.Fatalf("%s: status %d, want %d: %s", name, resp.StatusCode, tc.want, body)
		}
		if tc.want == http.StatusUnauthorized && !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
			t.Fatalf("%s: 401 without a Bearer challenge", name)
		}
	}
	if calls := e.upstreamCalls(); strings.TrimSpace(calls) != "" {
		t.Fatalf("a rejected request reached the upstream: %q", calls)
	}

	// Session lifecycle: a valid request works, DELETE ends the session,
	// the id is then unknown (404: the client must initialize again).
	if resp, body := p.rawRequest(t, http.MethodPost, `{"jsonrpc":"2.0","id":3,"method":"ping"}`, withSID(bearer())); resp.StatusCode != http.StatusOK {
		t.Fatalf("ping: %d %s", resp.StatusCode, body)
	}
	if resp, _ := p.rawRequest(t, http.MethodPost, `{"jsonrpc":"2.0","id":4,"method":"ping"}`, bearer()); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no session id: %d", resp.StatusCode)
	}
	if resp, _ := p.rawRequest(t, http.MethodDelete, "", withSID(bearer())); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE: %d", resp.StatusCode)
	}
	if resp, _ := p.rawRequest(t, http.MethodPost, `{"jsonrpc":"2.0","id":5,"method":"ping"}`, withSID(bearer())); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("ping after DELETE: %d", resp.StatusCode)
	}

	// doctor security reports the running front end's exposure (status
	// file under the proxy's HOME) and never the token.
	doctor := exec.Command(e.proxyBin, "--config", cfg, "doctor", "security")
	doctor.Env = append(os.Environ(), "HOME="+p.home)
	out, err := doctor.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Running: "+p.url) || !strings.Contains(string(out), "Exposure: loopback only") ||
		!strings.Contains(string(out), "Authentication: bearer token required") || strings.Contains(string(out), e2eServeToken) {
		t.Fatalf("doctor security: %v\n%s", err, out)
	}
	p.stop(t)

	// --no-auth is refused off loopback; a non-loopback bind without a
	// token never starts.
	for _, args := range [][]string{
		{"server", "run", "--http", "0.0.0.0:0", "--no-auth", "--config", cfg},
		{"server", "run", "--http", "127.0.0.1:0", "--no-auth", "--http-token", e2eServeToken, "--config", cfg},
		{"server", "run", "--http", "127.0.0.1:0", "--stdio", "--config", cfg},
	} {
		cmd := exec.Command(e.proxyBin, args...)
		cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("%v started:\n%s", args, out)
		}
	}
}

// childPIDs lists the processes whose parent is pid (Linux /proc).
func childPIDs(t *testing.T, pid int) []int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Skip("no /proc")
	}
	var out []int
	for _, e := range entries {
		child, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...: comm may hold spaces, cut after ')'.
		s := string(data)
		fields := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(fields) > 1 && fields[1] == strconv.Itoa(pid) {
			out = append(out, child)
		}
	}
	return out
}

// TestStreamableHTTP_SharedChildServers: two HTTP clients working at the
// same time share one set of child servers.
func TestStreamableHTTP_SharedChildServers(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("counts child processes through /proc")
	}
	proxyBin, fakeBin := buildProtoBinaries(t)
	cfg, a, b := writeRelayConfig(t, fmt.Sprintf("hs%d", os.Getpid()), fakeBin)
	p := startHTTPProxy(t, proxyBin, cfg, nil)

	c1, _ := mcpGoClient(t, p.url, "2025-11-25", nil)
	c2, _ := mcpGoClient(t, p.url, "2025-06-18", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i, c := range []*client.Client{c1, c2} {
		for k := 0; k < 10; k++ {
			wg.Add(1)
			go func(c *client.Client, server string, k int) {
				defer wg.Done()
				deadline := time.Now().Add(20 * time.Second)
				for {
					res, err := c.CallTool(ctx, invokeReq(server, "echo", map[string]any{"k": k}, nil))
					if err == nil && !res.IsError {
						return
					}
					if time.Now().After(deadline) {
						errs <- fmt.Errorf("echo %s %d: %v %+v", server, k, err, res)
						return
					}
					time.Sleep(100 * time.Millisecond)
				}
			}(c, []string{a, b}[(i+k)%2], k)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if kids := childPIDs(t, p.cmd.Process.Pid); len(kids) != 2 {
		t.Fatalf("child processes = %v, want exactly 2 (one per configured server)", kids)
	}
	if n := strings.Count(p.logs.String(), "stdio server started"); n != 2 {
		t.Fatalf("child servers started %d times, want 2", n)
	}
}
