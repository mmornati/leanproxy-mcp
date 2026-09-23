package pool

import (
	"context"
	stderrors "errors"
	"log/slog"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
)

// TestStdioPool_PreservesUpstreamErrorCodeAndMessage is the regression test
// for #296 point 1 (and the "stdio pool drops the structured upstream
// error" follow-up noted for #291): a subprocess's own
// {"error":{"code":...,"message":...,"data":...}} must reach
// StdioPool.SendRequestToServer with its original code, message and data,
// not collapsed into a generic ErrCodeServerError with a "jsonrpc: error
// N: msg" string for a message.
func TestStdioPool_PreservesUpstreamErrorCodeAndMessage(t *testing.T) {
	// A tiny "server" that always answers with a fixed JSON-RPC error,
	// regardless of the request it receives.
	script := `while read -r line; do printf '%s\n' '{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing owner","data":{"hint":"pass owner"}}}'; done`

	p := NewStdioPool(2, 5*time.Minute, slog.Default())
	defer p.Close()

	cfg := &migrate.ServerConfig{
		Name: "fake-error-srv",
		Stdio: &migrate.StdioConfig{
			Command: "sh",
			Args:    []string{"-c", script},
		},
		TimeoutValue: 5 * time.Second,
	}
	if err := p.StartServer(context.Background(), cfg); err != nil {
		t.Fatalf("StartServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := p.SendRequestToServer(ctx, "fake-error-srv", "tools/call", []byte(`{"name":"missing_owner","arguments":{}}`), 5*time.Second)
	if err != nil {
		t.Fatalf("SendRequestToServer returned a transport error instead of a structured response: %v", err)
	}
	if resp == nil || resp.Error == nil {
		t.Fatalf("expected an upstream error in the response, got: %+v", resp)
	}
	if resp.Error.Code != -32602 {
		t.Errorf("resp.Error.Code = %d, want -32602 (unchanged from upstream)", resp.Error.Code)
	}
	if resp.Error.Message != "missing owner" {
		t.Errorf("resp.Error.Message = %q, want %q (unchanged from upstream)", resp.Error.Message, "missing owner")
	}
	if len(resp.Error.Data) == 0 {
		t.Error("resp.Error.Data was dropped; expected the upstream data object to survive")
	}
}

// TestStdioPool_SendRequest_PreservesJSONRPCErrorType covers the
// proxy.JSONRPCRequest entry point (StdioPool.SendRequest, used by `serve`)
// with the same guarantee: the returned error unwraps (via errors.As) to
// the original *errors.JSONRPCError.
func TestStdioPool_SendRequest_PreservesJSONRPCErrorType(t *testing.T) {
	script := `while read -r line; do printf '%s\n' '{"jsonrpc":"2.0","id":1,"error":{"code":-32602,"message":"missing owner"}}'; done`

	p := NewStdioPool(2, 5*time.Minute, slog.Default())
	defer p.Close()

	cfg := &migrate.ServerConfig{
		Name: "fake-error-srv-2",
		Stdio: &migrate.StdioConfig{
			Command: "sh",
			Args:    []string{"-c", script},
		},
		TimeoutValue: 5 * time.Second,
	}
	if err := p.StartServer(context.Background(), cfg); err != nil {
		t.Fatalf("StartServer: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// UnifiedPool.SendRequest (the proxy.JSONRPCRequest entry point used by
	// `serve`) must propagate the same structured error, unwrappable via
	// errors.As, not a generic transport failure.
	unified := NewUnifiedPool(p, NewHTTPClientPool(nil), nil, slog.Default())
	req := &proxy.JSONRPCRequest{Method: "tools/call", Params: []byte(`{"name":"missing_owner","arguments":{}}`), ID: 1}
	_, sendErr := unified.SendRequest(ctx, "fake-error-srv-2", req, 5*time.Second)
	if sendErr == nil {
		t.Fatal("expected UnifiedPool.SendRequest to return the structured upstream error")
	}

	var rpcErr *errors.JSONRPCError
	if !stderrors.As(sendErr, &rpcErr) {
		t.Fatalf("expected sendErr to unwrap to *errors.JSONRPCError, got: %v (%T)", sendErr, sendErr)
	}
	if rpcErr.Code != -32602 || rpcErr.Message != "missing owner" {
		t.Errorf("rpcErr = %+v, want code -32602 message %q", rpcErr, "missing owner")
	}
}
