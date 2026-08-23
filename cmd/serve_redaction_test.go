package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
