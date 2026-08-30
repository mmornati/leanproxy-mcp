package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/cache"
	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/sidecar"
)

const (
	testAWSKey = "AKIAIOSFODNN7EXAMPLE"
	testGHPat  = "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
)

// withBuiltInRedactor installs the default redactor for the duration of a test.
func withBuiltInRedactor(t *testing.T) {
	t.Helper()
	prev := globalRedactor.Load()
	prevDetector := providerDetector.Load()
	prevInjector := breakpointInjector.Load()
	t.Cleanup(func() {
		globalRedactor.Store(prev)
		providerDetector.Store(prevDetector)
		breakpointInjector.Store(prevInjector)
	})
	initRedactor(nil)
	providerDetector.Store(cache.NewProviderDetector())
	breakpointInjector.Store(cache.NewBreakpointInjector(cache.WithStrategy(cache.StrategyOff)))
}

func TestInitRedactor_DefaultsToBuiltInsWhenNoConfig(t *testing.T) {
	prev := globalRedactor.Load()
	t.Cleanup(func() { globalRedactor.Store(prev) })

	initRedactor(nil)
	if globalRedactor.Load() == nil {
		t.Fatal("redactor must be enabled by default with no config")
	}
	initRedactor(&migrate.Config{})
	if globalRedactor.Load() == nil {
		t.Fatal("redactor must be enabled by default with a config lacking a bouncer block")
	}
}

func TestInitRedactor_ExplicitDisable(t *testing.T) {
	prev := globalRedactor.Load()
	t.Cleanup(func() { globalRedactor.Store(prev) })

	off := false
	initRedactor(&migrate.Config{Bouncer: &bouncer.Config{Enabled: &off}})
	if globalRedactor.Load() != nil {
		t.Fatal("enabled: false must disable the redactor")
	}
}

func TestInitRedactor_CustomPatternsFromBouncerBlock(t *testing.T) {
	prev := globalRedactor.Load()
	t.Cleanup(func() { globalRedactor.Store(prev) })

	initRedactor(&migrate.Config{Bouncer: &bouncer.Config{
		Patterns: []bouncer.PatternDef{{Name: "internal", Pattern: `itk_[a-f0-9]{16}`}},
	}})
	r := globalRedactor.Load()
	if r == nil {
		t.Fatal("expected redactor")
	}
	out, n, err := r.RedactJSON([]byte(`{"t":"itk_0123456789abcdef"}`))
	if err != nil || n != 1 || strings.Contains(string(out), "itk_0123") {
		t.Fatalf("custom pattern from bouncer.patterns not applied: out=%s n=%d err=%v", out, n, err)
	}
}

// Every byte that leaves the proxy — to the upstream server, the embedder,
// the semantic cache and the client — must already be redacted.
func TestHandleSingleRequest_RedactsParamsBeforeForwarding(t *testing.T) {
	withBuiltInRedactor(t)

	var forwarded []byte
	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		forwarded = append([]byte(nil), req.Params...)
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{}`), ID: req.ID}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx,
		[]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"t","arguments":{"token":"`+testAWSKey+`"}},"id":1}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if forwarded == nil {
		t.Fatal("request was not forwarded")
	}
	if bytes.Contains(forwarded, []byte(testAWSKey)) {
		t.Fatalf("secret forwarded upstream unredacted: %s", forwarded)
	}
	if !bytes.Contains(forwarded, []byte(bouncer.SecretRedacted)) {
		t.Fatalf("expected redaction marker in forwarded params: %s", forwarded)
	}
}

func TestHandleSingleRequest_RedactsUpstreamResponse(t *testing.T) {
	withBuiltInRedactor(t)

	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		return &proxy.JSONRPCResponse{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`{"content":[{"type":"text","text":"GITHUB_TOKEN=` + testGHPat + `"}]}`),
			ID:      req.ID,
		}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx,
		[]byte(`{"jsonrpc":"2.0","method":"resources/read","params":{"uri":"file:///.env"},"id":1}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if strings.Contains(buf.String(), testGHPat) {
		t.Fatalf("secret in upstream response reached the client: %s", buf.String())
	}
	if !strings.Contains(buf.String(), bouncer.SecretRedacted) {
		t.Fatalf("expected redaction marker in client response: %s", buf.String())
	}
}

func TestHandleSingleRequest_RedactsUpstreamErrorMessage(t *testing.T) {
	withBuiltInRedactor(t)

	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		return &proxy.JSONRPCResponse{
			JSONRPC: "2.0",
			Error:   errors.NewJSONRPCError(errors.ErrCodeInternalError, "auth failed for key "+testAWSKey),
			ID:      req.ID,
		}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"t"},"id":1}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if strings.Contains(buf.String(), testAWSKey) {
		t.Fatalf("secret in upstream error reached the client: %s", buf.String())
	}
}

func TestHandleSingleRequestAsync_RedactsBothDirections(t *testing.T) {
	withBuiltInRedactor(t)

	var forwarded []byte
	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		forwarded = append([]byte(nil), req.Params...)
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`"` + testGHPat + `"`), ID: req.ID}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequestAsync(ctx,
		[]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"k":"`+testAWSKey+`"},"id":1}`),
		w, &sync.Mutex{}, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if bytes.Contains(forwarded, []byte(testAWSKey)) || strings.Contains(buf.String(), testGHPat) {
		t.Fatalf("leak: forwarded=%s response=%s", forwarded, buf.String())
	}
}

func TestHandleBatchRequest_RedactsBothDirections(t *testing.T) {
	withBuiltInRedactor(t)

	var forwarded [][]byte
	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		forwarded = append(forwarded, append([]byte(nil), req.Params...))
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`"` + testGHPat + `"`), ID: req.ID}, nil
	}}

	line := []byte(`[{"jsonrpc":"2.0","method":"a","params":{"k":"` + testAWSKey + `"},"id":1},{"jsonrpc":"2.0","method":"b","params":{"k":"` + testAWSKey + `"},"id":2}]`)

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleBatchRequest(ctx, line, w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()
	if len(forwarded) != 2 {
		t.Fatalf("expected 2 forwarded requests, got %d", len(forwarded))
	}
	for _, f := range forwarded {
		if bytes.Contains(f, []byte(testAWSKey)) {
			t.Fatalf("batch forwarded secret: %s", f)
		}
	}
	if strings.Contains(buf.String(), testGHPat) {
		t.Fatalf("batch response leaked secret: %s", buf.String())
	}

	buf.Reset()
	w = bufio.NewWriter(&buf)
	forwarded = nil
	handleBatchRequestAsync(ctx, line, w, &sync.Mutex{}, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()
	for _, f := range forwarded {
		if bytes.Contains(f, []byte(testAWSKey)) {
			t.Fatalf("async batch forwarded secret: %s", f)
		}
	}
	if strings.Contains(buf.String(), testGHPat) {
		t.Fatalf("async batch response leaked secret: %s", buf.String())
	}
}

func TestHandleGatewayToolSync_RedactsResult(t *testing.T) {
	withBuiltInRedactor(t)

	gt := &mockGatewayTools{listServersFunc: func(context.Context) ([]gateway.ServerInfo, error) {
		return []gateway.ServerInfo{{Name: "srv-" + testAWSKey, Status: "ok"}}, nil
	}}
	resp := handleGatewayToolSync(ctx, &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: "list_servers", ID: 1}, gt)
	if resp == nil || bytes.Contains(resp.Result, []byte(testAWSKey)) {
		t.Fatalf("gateway tool result leaked secret: %v", resp)
	}
}

// The embedder and semantic cache see params only after the regex pass.
func TestSemanticCacheLookup_SeesRedactedParams(t *testing.T) {
	withBuiltInRedactor(t)

	req, err := proxy.ParseJSONRPCRequest([]byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"t","arguments":{"k":"` + testAWSKey + `"}},"id":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := redactParams(req); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(req.Params, []byte(testAWSKey)) {
		t.Fatalf("params still contain secret after redactParams: %s", req.Params)
	}
}

func TestRedactParams_InvalidJSONStillRedacted(t *testing.T) {
	withBuiltInRedactor(t)
	req := &proxy.JSONRPCRequest{Params: json.RawMessage(`{"k":"` + testAWSKey + `"`)} // truncated
	if err := redactParams(req); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(req.Params, []byte(testAWSKey)) {
		t.Fatalf("malformed params bypassed redaction: %s", req.Params)
	}
}

// A sidecar that fails or returns garbage must reject the request rather
// than let possibly-unredacted params through.
func TestHandleSingleRequest_SidecarFailureIsFailClosed(t *testing.T) {
	withBuiltInRedactor(t)
	prevSidecar := globalSidecar
	t.Cleanup(func() { globalSidecar = prevSidecar })

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"test","response":"not json at all","done":true}`))
	}))
	defer ts.Close()
	var err error
	globalSidecar, err = sidecar.NewManager(sidecar.Config{Provider: "ollama", Model: "test", URL: ts.URL}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	forwarded := false
	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		forwarded = true
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{}`), ID: req.ID}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"k":"v"},"id":1}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if forwarded {
		t.Fatal("request forwarded despite sidecar returning invalid JSON")
	}
	if !strings.Contains(buf.String(), redactionFailedMessage) {
		t.Fatalf("expected fail-closed error, got %s", buf.String())
	}
}

// A sidecar that returns the documented fallback sentinel (Ollama down,
// empty response, decode error) must NOT cause every request to be rejected.
// The regex layer has already cleaned the input by the time we reach the
// sidecar, so the regex-redacted payload is forwarded with a Warn.
func TestHandleSingleRequest_SidecarFallbackIsAvailable(t *testing.T) {
	withBuiltInRedactor(t)
	prevSidecar := globalSidecar
	prevAlways := globalAlwaysCallSidecar.Load()
	t.Cleanup(func() {
		globalSidecar = prevSidecar
		globalAlwaysCallSidecar.Store(prevAlways)
	})
	globalAlwaysCallSidecar.Store(false)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"model":"test","response":"","done":true}`))
	}))
	defer ts.Close()
	var err error
	globalSidecar, err = sidecar.NewManager(sidecar.Config{Provider: "ollama", Model: "test", URL: ts.URL}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	forwarded := false
	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		forwarded = true
		return &proxy.JSONRPCResponse{JSONRPC: "2.0", Result: json.RawMessage(`{}`), ID: req.ID}, nil
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"k":"v"},"id":1}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if !forwarded {
		t.Fatalf("request must be forwarded on sidecar fallback, got: %s", buf.String())
	}
}

// B1: cached responses must go through redactResponse on the cache-hit
// path so any future code change that stores an unredacted entry fails
// closed rather than leaking. Exercise the redact-on-cache-hit logic
// directly via the in-package redactResponse helper: the cache-hit branch
// in handleSingleRequest calls redactResponse on a freshly-built
// cachedResponse, so testing redactResponse in isolation covers the
// redaction half. The wiring itself is exercised by the other tests.
func TestRedactResponse_RedactsCachedResponse(t *testing.T) {
	withBuiltInRedactor(t)

	resp := &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"token":"` + testGHPat + `"}`),
		ID:      1,
	}
	if err := redactResponse(resp); err != nil {
		t.Fatalf("redactResponse: %v", err)
	}
	if bytes.Contains(resp.Result, []byte(testGHPat)) {
		t.Fatalf("redactResponse leaked secret from cached response: %s", resp.Result)
	}
}

// B3: SendRequest transport errors (URL with embedded credentials, etc.)
// must be redacted before reaching the client.
func TestHandleSingleRequest_RedactsUpstreamSendError(t *testing.T) {
	withBuiltInRedactor(t)

	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		return nil, fmt.Errorf("dial tcp: lookup http://user:%s@upstream.example.com: no such host", testAWSKey)
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"k":"v"},"id":7}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	if strings.Contains(buf.String(), testAWSKey) {
		t.Fatalf("SendRequest err leaked credential: %s", buf.String())
	}
	if !strings.Contains(buf.String(), bouncer.SecretRedacted) {
		t.Fatalf("expected redaction marker in error response, got: %s", buf.String())
	}
}

// C1: a well-formed request whose SendRequest fails must preserve the
// request ID on the error response (JSON-RPC 2.0 §4 says parse errors
// are the only exception), and the error message must be redacted.
func TestHandleSingleRequest_PreservesIDOnSendError(t *testing.T) {
	withBuiltInRedactor(t)

	mockP := &mockPool{sendRequestFunc: func(_ context.Context, _ string, req *proxy.JSONRPCRequest, _ time.Duration) (*proxy.JSONRPCResponse, error) {
		return nil, fmt.Errorf("upstream failed: %s", testAWSKey)
	}}

	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"tools/call","params":{"k":"v"},"id":42}`),
		w, &mockRouter{}, &mockGatewayTools{}, mockP)
	w.Flush()

	var parsed proxy.JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("response is not valid JSON: %v body=%s", err, buf.String())
	}
	id, ok := parsed.ID.(float64)
	if !ok || id != 42 {
		t.Fatalf("ID 42 must be preserved, got %v", parsed.ID)
	}
	if parsed.Error == nil {
		t.Fatal("expected error response")
	}
	if strings.Contains(parsed.Error.Message, testAWSKey) {
		t.Fatalf("error message leaked credential: %s", parsed.Error.Message)
	}
}

// B2: invoke_tool error responses from the gateway must have their error
// message string redacted before reaching the client.
func TestHandleGatewayToolSync_RedactsInvokeToolError(t *testing.T) {
	withBuiltInRedactor(t)

	gt := &mockGatewayTools{invokeToolFunc: func(_ context.Context, _ gateway.InvokeToolParams) (interface{}, error) {
		return nil, fmt.Errorf("tool failed: %s in /home/me", testAWSKey)
	}}
	resp := handleGatewayToolSync(ctx, &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: "invoke_tool", Params: json.RawMessage(`{"name":"x"}`), ID: 1}, gt)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response")
	}
	if strings.Contains(resp.Error.Message, testAWSKey) {
		t.Fatalf("invoke_tool error leaked credential: %s", resp.Error.Message)
	}
}

// B2: list_servers errors must also be redacted.
func TestHandleGatewayToolSync_RedactsListServersError(t *testing.T) {
	withBuiltInRedactor(t)

	gt := &mockGatewayTools{listServersFunc: func(context.Context) ([]gateway.ServerInfo, error) {
		return nil, fmt.Errorf("registry unreachable: %s", testAWSKey)
	}}
	resp := handleGatewayToolSync(ctx, &proxy.JSONRPCRequest{JSONRPC: "2.0", Method: "list_servers", ID: 1}, gt)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response")
	}
	if strings.Contains(resp.Error.Message, testAWSKey) {
		t.Fatalf("list_servers error leaked credential: %s", resp.Error.Message)
	}
}
