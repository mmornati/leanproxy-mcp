package pool

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// fakeHTTPMCP is a minimal Streamable HTTP MCP server: every POST carries
// one JSON-RPC message and a request is answered with application/json.
// It records the raw request bodies.
type fakeHTTPMCP struct {
	mu     sync.Mutex
	bodies map[string][]string // method -> raw bodies
}

func (f *fakeHTTPMCP) seen(method string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies[method]...)
}

const richCallResult = `{"content":[{"type":"text","text":"ok"},{"type":"resource_link","uri":"file:///r.txt","name":"r","title":"R"}],"structuredContent":{"id":9007199254740993,"price":1.50},"isError":false}`

func (f *fakeHTTPMCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	_ = json.Unmarshal(body, &msg)
	f.mu.Lock()
	f.bodies[msg.Method] = append(f.bodies[msg.Method], string(body))
	f.mu.Unlock()
	if len(msg.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	var result, rpcErr string
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		result = `{"protocolVersion":"` + p.ProtocolVersion + `","capabilities":{"tools":{},"resources":{},"prompts":{}},"serverInfo":{"name":"fakehttp","version":"1"}}`
	case "tools/list":
		result = `{"tools":[{"name":"t","title":"T","description":"d","inputSchema":{"type":"object"},"outputSchema":{"type":"object"},"annotations":{"destructiveHint":true},"icons":[{"src":"https://x/i.png","theme":"dark"}],"_meta":{"k":1}}]}`
	case "tools/call":
		if strings.Contains(string(msg.Params), `"boom"`) {
			rpcErr = `{"code":-32602,"message":"missing owner","data":{"field":"owner"}}`
		} else {
			result = richCallResult
		}
	case "resources/list":
		result = `{"resources":[{"uri":"file:///r.txt","name":"r","size":12345678901234567}],"nextCursor":"c2"}`
	case "prompts/get":
		result = `{"messages":[{"role":"user","content":{"type":"text","text":"hi"}}]}`
	default:
		rpcErr = `{"code":-32601,"message":"method not found"}`
	}
	w.Header().Set("Content-Type", "application/json")
	if rpcErr != "" {
		_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(msg.ID)+`,"error":`+rpcErr+`}`)
		return
	}
	_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":`+string(msg.ID)+`,"result":`+result+`}`)
}

func startFakeHTTPPool(t *testing.T) (*HTTPClientPool, *fakeHTTPMCP) {
	t.Helper()
	fake := &fakeHTTPMCP{bodies: map[string][]string{}}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	p := NewHTTPClientPool(nil)
	t.Cleanup(func() { _ = p.Close() })
	if err := p.StartServer(context.Background(), &migrate.ServerConfig{Name: "remote", HTTP: &migrate.HTTPConfig{URL: srv.URL}}); err != nil {
		t.Fatal(err)
	}
	return p, fake
}

// TestHTTPPool_RelaysMCPMethodsVerbatim covers #307 for HTTP upstreams: the
// handshake requests the latest protocol version, and tools/list,
// tools/call and resources/* travel as raw JSON both ways (no mcp-go typed
// round-trip dropping fields or rounding big integers), with upstream
// JSON-RPC errors relayed as errors.
func TestHTTPPool_RelaysMCPMethodsVerbatim(t *testing.T) {
	p, fake := startFakeHTTPPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := p.SendRequestToServer(ctx, "remote", "tools/list", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	for _, want := range []string{`"title":"T"`, `"outputSchema":{"type":"object"}`, `"destructiveHint":true`, `"theme":"dark"`, `"_meta":{"k":1}`} {
		if !strings.Contains(string(resp.Result), want) {
			t.Errorf("tools/list result lost %s: %s", want, resp.Result)
		}
	}
	inits := fake.seen("initialize")
	if len(inits) != 1 || !strings.Contains(inits[0], `"protocolVersion":"`+RequestedProtocolVersion+`"`) {
		t.Fatalf("initialize requests = %v, want one asking for %s", inits, RequestedProtocolVersion)
	}
	res, ok := p.ServerInitializeResult("remote")
	if !ok || res.ProtocolVersion != RequestedProtocolVersion || !strings.Contains(string(res.Capabilities), "resources") {
		t.Fatalf("stored session = %+v", res)
	}

	args := `{"name":"t","arguments":{"id":9007199254740993,"nested":{"f":1.50}}}`
	resp, err = p.SendRequestToServer(ctx, "remote", "tools/call", json.RawMessage(args), 5*time.Second)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if string(resp.Result) != richCallResult {
		t.Fatalf("tools/call result changed:\n got %s\nwant %s", resp.Result, richCallResult)
	}
	calls := fake.seen("tools/call")
	if len(calls) != 1 || !strings.Contains(calls[0], `"params":`+args) {
		t.Fatalf("tools/call params not relayed byte for byte: %v", calls)
	}

	resp, err = p.SendRequestToServer(ctx, "remote", "tools/call", json.RawMessage(`{"name":"boom"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("tools/call boom: transport error %v, want a JSON-RPC error", err)
	}
	if resp.Error == nil || resp.Error.Code != -32602 || resp.Error.Message != "missing owner" || string(resp.Error.Data) != `{"field":"owner"}` {
		t.Fatalf("upstream error not relayed: %+v", resp.Error)
	}

	resp, err = p.SendRequestToServer(ctx, "remote", "resources/list", json.RawMessage(`{"cursor":"c1"}`), 5*time.Second)
	if err != nil {
		t.Fatalf("resources/list: %v", err)
	}
	if !strings.Contains(string(resp.Result), `"size":12345678901234567`) || !strings.Contains(string(resp.Result), `"nextCursor":"c2"`) {
		t.Fatalf("resources/list result = %s", resp.Result)
	}
	if lists := fake.seen("resources/list"); len(lists) != 1 || !strings.Contains(lists[0], `"cursor":"c1"`) {
		t.Fatalf("resources/list cursor not forwarded: %v", lists)
	}

	// serve's proxy path relays the same way.
	presp, err := p.SendRequest(ctx, "remote", &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: "tools/call", Params: json.RawMessage(args), ID: 7}, 5*time.Second)
	if err != nil {
		t.Fatalf("SendRequest tools/call: %v", err)
	}
	if string(presp.Result) != richCallResult || presp.ID != 7 {
		t.Fatalf("SendRequest result = %+v", presp)
	}
	presp, err = p.SendRequest(ctx, "remote", &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: "prompts/get", Params: json.RawMessage(`{"name":"x"}`), ID: 8}, 5*time.Second)
	if err != nil || presp.Error != nil || !strings.Contains(string(presp.Result), `"text":"hi"`) {
		t.Fatalf("SendRequest prompts/get = %+v, %v", presp, err)
	}
}

func TestRemoteNotificationEvent(t *testing.T) {
	for method, want := range map[string]ServerEventKind{
		MethodToolsListChanged:     EventToolsListChanged,
		MethodResourcesListChanged: EventResourcesListChanged,
		MethodPromptsListChanged:   EventPromptsListChanged,
	} {
		got, ok := remoteNotificationEvent(method)
		if !ok || got != want {
			t.Errorf("%s -> %v, %v", method, got, ok)
		}
	}
	if _, ok := remoteNotificationEvent("notifications/progress"); ok {
		t.Error("progress is not a list event")
	}
	if EventResourcesListChanged.String() != "resources_list_changed" || EventPromptsListChanged.String() != "prompts_list_changed" {
		t.Error("event names")
	}
	if !forwardsRaw("resources/read") || forwardsRaw("create_issue") {
		t.Error("forwardsRaw")
	}
}
