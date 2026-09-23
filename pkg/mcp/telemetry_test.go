package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// withInMemoryTracer installs an in-memory span recorder as the global otel
// tracer provider for the duration of the test and restores whatever was
// installed before (issue #317 acceptance criterion: "with an in-memory
// exporter in tests, one invoke_tool produces a SERVER span with a child
// upstream CLIENT span").
func withInMemoryTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	SetTelemetryActive(true)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		SetTelemetryActive(false)
		_ = tp.Shutdown(context.Background())
	})
	return exp
}

func TestTelemetryMiddleware_InvokeToolProducesServerAndClientSpans(t *testing.T) {
	exp := withInMemoryTracer(t)

	p := newLifecyclePool("github")
	p.setTools("github", "get_file_contents")
	h := NewHandler(p, nil)
	h.Use(TelemetryMiddleware())

	args, _ := json.Marshal(map[string]interface{}{
		"server": "github",
		"tool":   "get_file_contents",
		"arguments": map[string]interface{}{
			"owner":  "octocat",
			"secret": "super-secret-value-should-never-appear-on-a-span",
		},
	})
	params, _ := json.Marshal(ToolsCallParams{Name: "invoke_tool", Arguments: args})
	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, Params: params, ID: float64(1)}

	resp, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.Error)

	spans := exp.GetSpans()
	require.Len(t, spans, 2, "expected one SERVER span and one child CLIENT span")

	var server, client tracetest.SpanStub
	for _, s := range spans {
		switch s.SpanKind.String() {
		case "server":
			server = s
		case "client":
			client = s
		}
	}
	require.NotEmpty(t, server.Name, "no SERVER span recorded")
	require.NotEmpty(t, client.Name, "no CLIENT span recorded")

	assert.Equal(t, server.SpanContext.SpanID(), client.Parent.SpanID(),
		"the upstream CLIENT span must be a child of the request's SERVER span")
	assert.Equal(t, codes.Ok, server.Status.Code)
	assert.Equal(t, codes.Ok, client.Status.Code)

	assertAttr := func(s tracetest.SpanStub, key, want string) {
		t.Helper()
		for _, a := range s.Attributes {
			if string(a.Key) == key {
				assert.Equal(t, want, a.Value.AsString())
				return
			}
		}
		t.Errorf("attribute %q not found on span %q", key, s.Name)
	}
	assertAttr(server, "mcp.method.name", MethodToolsCall)
	assertAttr(server, "mcp.tool.name", "get_file_contents")
	assertAttr(client, "mcp.server.name", "github")
	assertAttr(client, "mcp.method.name", MethodToolsCall)

	// Never record argument or result payload content on a span.
	for _, s := range spans {
		for _, a := range s.Attributes {
			assert.NotContains(t, a.Value.Emit(), "super-secret-value-should-never-appear-on-a-span",
				"span %q attribute %q leaked payload content", s.Name, a.Key)
			assert.NotContains(t, a.Value.Emit(), "octocat",
				"span %q attribute %q leaked payload content", s.Name, a.Key)
		}
	}
}

func TestTelemetryMiddleware_ErrorSetsErrorType(t *testing.T) {
	exp := withInMemoryTracer(t)

	p := newLifecyclePool("gh")
	h := NewHandler(p, nil)
	h.Use(TelemetryMiddleware())

	// tools/call with an unknown tool name (not a gateway tool, no known
	// server prefix) fails parseToolName and returns a JSON-RPC error.
	params, _ := json.Marshal(ToolsCallParams{Name: "unknown_tool"})
	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, Params: params, ID: float64(2)}

	resp, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp.Error)

	spans := exp.GetSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, codes.Error, spans[0].Status.Code)
	var sawErrorType bool
	for _, a := range spans[0].Attributes {
		if string(a.Key) == "error.type" {
			sawErrorType = true
		}
	}
	assert.True(t, sawErrorType, "expected error.type attribute on a failed request's span")
}

func TestTelemetryMiddleware_NotificationSkipsSpan(t *testing.T) {
	exp := withInMemoryTracer(t)

	p := newLifecyclePool()
	h := NewHandler(p, nil)
	h.Use(TelemetryMiddleware())

	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodInitialized, ID: nil}
	_, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)

	assert.Empty(t, exp.GetSpans(), "a notification must not open a span")
}

func TestTelemetrySnapshot_TracksRequests(t *testing.T) {
	withInMemoryTracer(t)
	before := TelemetrySnapshot()

	p := newLifecyclePool("srv")
	p.setTools("srv", "do_thing")
	h := NewHandler(p, nil)
	h.Use(TelemetryMiddleware())

	args, _ := json.Marshal(map[string]interface{}{"server": "srv", "tool": "do_thing"})
	params, _ := json.Marshal(ToolsCallParams{Name: "invoke_tool", Arguments: args})
	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, Params: params, ID: float64(3)}

	_, err := h.HandleRequest(context.Background(), req)
	require.NoError(t, err)

	after := TelemetrySnapshot()
	assert.Equal(t, before.RequestsTotal+1, after.RequestsTotal)
	assert.Equal(t, before.InFlight, after.InFlight, "in-flight must return to its starting value once the request completes")
}

func TestTelemetryMiddleware_SpanCarriesRequestIDMetricsDoNot(t *testing.T) {
	// jsonrpc.request.id must stay on the span only: attaching it to a
	// metric would create one time series per request (unbounded
	// cardinality). We cannot inspect exported metric attributes directly
	// here (no in-memory metric reader is wired in this test file), but we
	// can confirm the span carries the id and that recording a second
	// request with a different id does not panic or corrupt the first
	// span's attributes (a regression test for an earlier bug where the
	// metric attribute slice was built by appending onto the span's
	// backing array).
	exp := withInMemoryTracer(t)

	p := newLifecyclePool()
	h := NewHandler(p, nil)
	h.Use(TelemetryMiddleware())

	for _, id := range []float64{1, 2, 3} {
		req := &Request{JSONRPC: JSONRPCVersion, Method: MethodPing, ID: id}
		_, err := h.HandleRequest(context.Background(), req)
		require.NoError(t, err)
	}

	spans := exp.GetSpans()
	require.Len(t, spans, 3)
	seen := map[string]bool{}
	for _, s := range spans {
		for _, a := range s.Attributes {
			if string(a.Key) == "jsonrpc.request.id" {
				seen[a.Value.AsString()] = true
			}
		}
	}
	assert.Equal(t, map[string]bool{"1": true, "2": true, "3": true}, seen,
		"each span must keep its own request id, unaffected by later requests")
}

func TestTraced_WrapsWithChildSpan(t *testing.T) {
	exp := withInMemoryTracer(t)

	inner := Middleware(func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			return next(ctx, req)
		}
	})

	pipeline := Chain(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{JSONRPC: JSONRPCVersion, Result: json.RawMessage(`{}`), ID: req.ID}, nil
	}, TelemetryMiddleware(), Traced("cache", inner))

	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodPing, ID: float64(1)}
	_, err := pipeline(context.Background(), req)
	require.NoError(t, err)

	spans := exp.GetSpans()
	require.Len(t, spans, 2)
	var named bool
	for _, s := range spans {
		if s.Name == "mcp.middleware.cache" {
			named = true
		}
	}
	assert.True(t, named, "expected a named child span for the wrapped middleware")
}
