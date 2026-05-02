package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/gateway"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/router"
)

type mockRouter struct {
	routeFunc func(ctx context.Context, method string) (*registry.ServerEntry, error)
}

func (m *mockRouter) Route(ctx context.Context, method string) (*registry.ServerEntry, error) {
	if m.routeFunc != nil {
		return m.routeFunc(ctx, method)
	}
	return &registry.ServerEntry{ID: "default-server"}, nil
}

func (m *mockRouter) RouteBatch(ctx context.Context, methods []string) ([]*registry.ServerEntry, []error) {
	results := make([]*registry.ServerEntry, len(methods))
	for i := range methods {
		results[i] = &registry.ServerEntry{ID: "default-server"}
	}
	return results, nil
}

type mockGatewayTools struct {
	listServersFunc  func(ctx context.Context) ([]gateway.ServerInfo, error)
	invokeToolFunc   func(ctx context.Context, params gateway.InvokeToolParams) (interface{}, error)
	searchToolsFunc  func(ctx context.Context, query string) ([]gateway.ToolSearchResult, error)
	listToolsFunc    func() []gateway.Tool
}

func (m *mockGatewayTools) ListTools() []gateway.Tool {
	if m.listToolsFunc != nil {
		return m.listToolsFunc()
	}
	return nil
}

func (m *mockGatewayTools) ListServers(ctx context.Context) ([]gateway.ServerInfo, error) {
	if m.listServersFunc != nil {
		return m.listServersFunc(ctx)
	}
	return []gateway.ServerInfo{}, nil
}

func (m *mockGatewayTools) InvokeTool(ctx context.Context, params gateway.InvokeToolParams) (interface{}, error) {
	if m.invokeToolFunc != nil {
		return m.invokeToolFunc(ctx, params)
	}
	return nil, nil
}

func (m *mockGatewayTools) SearchTools(ctx context.Context, query string) ([]gateway.ToolSearchResult, error) {
	if m.searchToolsFunc != nil {
		return m.searchToolsFunc(ctx, query)
	}
	return []gateway.ToolSearchResult{}, nil
}

func TestIsGatewayTool(t *testing.T) {
	tests := []struct {
		method string
		isGW   bool
	}{
		{"list_servers", true},
		{"invoke_tool", true},
		{"search_tools", true},
		{"namespace.tool", false},
		{"some_tool", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			result := isGatewayTool(tt.method)
			if result != tt.isGW {
				t.Errorf("isGatewayTool(%q) = %v, want %v", tt.method, result, tt.isGW)
			}
		})
	}
}

func TestTrimNewline(t *testing.T) {
	tests := []struct {
		input    []byte
		expected []byte
	}{
		{[]byte("hello\n"), []byte("hello")},
		{[]byte("hello\r\n"), []byte("hello")},
		{[]byte("hello\n\r"), []byte("hello\n")},
		{[]byte("hello"), []byte("hello")},
		{[]byte(""), []byte("")},
		{[]byte("\n"), []byte("")},
		{[]byte("\r\n"), []byte("")},
	}

	for _, tt := range tests {
		t.Run(string(tt.input), func(t *testing.T) {
			result := trimNewline(tt.input)
			if !bytes.Equal(result, tt.expected) {
				t.Errorf("trimNewline(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsBatchRequest(t *testing.T) {
	batch := []byte(`[{"jsonrpc":"2.0","method":"test","id":1}]`)
	single := []byte(`{"jsonrpc":"2.0","method":"test","id":1}`)

	if !isBatchRequest(batch) {
		t.Error("expected batch request to return true")
	}
	if isBatchRequest(single) {
		t.Error("expected single request to return false")
	}
}

func TestWriteResponse(t *testing.T) {
	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	resp := &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"key":"value"}`),
		ID:      float64(1),
	}

	writeResponse(writer, resp)
	writer.Flush()

	output := readBuf.String()

	var parsed proxy.JSONRPCResponse
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if parsed.ID != 1.0 {
		t.Errorf("expected ID 1.0, got %v", parsed.ID)
	}
}

func TestWriteError(t *testing.T) {
	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	writeError(writer, proxy.ErrCodeMethodNotFound, "Method not found")
	writer.Flush()

	output := readBuf.String()

	var parsed proxy.JSONRPCResponse
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("invalid JSON response: %v", err)
	}

	if parsed.Error == nil {
		t.Fatal("expected error in response")
	}

	if parsed.Error.Code != proxy.ErrCodeMethodNotFound {
		t.Errorf("expected error code %d, got %d", proxy.ErrCodeMethodNotFound, parsed.Error.Code)
	}

	if parsed.Error.Message != "Method not found" {
		t.Errorf("expected message 'Method not found', got %q", parsed.Error.Message)
	}
}

type mockReadWriter struct {
	reader io.Reader
	writer *bytes.Buffer
}

func (m *mockReadWriter) Read(p []byte) (n int, err error) {
	return m.reader.Read(p)
}

func (m *mockReadWriter) Write(p []byte) (n int, err error) {
	return m.writer.Write(p)
}

type mockPool struct {
	sendRequestFunc func(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error)
}

func (m *mockPool) SendRequest(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
	if m.sendRequestFunc != nil {
		return m.sendRequestFunc(ctx, serverName, req, timeout)
	}
	return &proxy.JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{}`),
		ID:      req.ID,
	}, nil
}

func TestHandleConnection_GatewayTool(t *testing.T) {
	mockR := &mockRouter{}
	mockGT := &mockGatewayTools{
		listServersFunc: func(ctx context.Context) ([]gateway.ServerInfo, error) {
			return []gateway.ServerInfo{
				{Name: "server1", Status: "healthy"},
			}, nil
		},
	}
	mockP := &mockPool{}

	input := `{"jsonrpc":"2.0","method":"list_servers","id":1}` + "\n"
	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	handleConnection(&mockReadWriter{reader: bytes.NewReader([]byte(input)), writer: readBuf}, mockR, mockGT, mockP)

	writer.Flush()
	output := readBuf.String()

	if output == "" {
		t.Fatal("expected response for gateway tool, got empty")
	}

	var resp proxy.JSONRPCResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}
}

func TestHandleConnection_ParseError(t *testing.T) {
	mockR := &mockRouter{}
	mockGT := &mockGatewayTools{}
	mockP := &mockPool{}

	input := `not valid json` + "\n"
	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	handleConnection(&mockReadWriter{reader: bytes.NewReader([]byte(input)), writer: readBuf}, mockR, mockGT, mockP)

	writer.Flush()
	output := readBuf.String()

	var resp proxy.JSONRPCResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response for malformed JSON")
	}

	if resp.Error.Code != proxy.ErrCodeParseError {
		t.Errorf("expected parse error code %d, got %d", proxy.ErrCodeParseError, resp.Error.Code)
	}
}

func TestHandleConnection_EOF(t *testing.T) {
	mockR := &mockRouter{}
	mockGT := &mockGatewayTools{}
	mockP := &mockPool{}

	input := ``
	readBuf := &bytes.Buffer{}

	handleConnection(&mockReadWriter{reader: bytes.NewReader([]byte(input)), writer: readBuf}, mockR, mockGT, mockP)
}

func TestHandleSingleRequest_RouteError(t *testing.T) {
	mockR := &mockRouter{
		routeFunc: func(ctx context.Context, method string) (*registry.ServerEntry, error) {
			return nil, router.NewRouterError(proxy.ErrCodeMethodNotFound, "method not found", router.ErrToolNotFound)
		},
	}
	mockGT := &mockGatewayTools{}
	mockP := &mockPool{}

	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"unknown.tool","id":1}`), writer, mockR, mockGT, mockP)

	writer.Flush()
	output := readBuf.String()

	var resp proxy.JSONRPCResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error == nil {
		t.Fatal("expected error response")
	}

	if resp.Error.Code != proxy.ErrCodeMethodNotFound {
		t.Errorf("expected method not found error, got %d", resp.Error.Code)
	}
}

func TestHandleSingleRequest_SuccessfulRoute(t *testing.T) {
	mockR := &mockRouter{
		routeFunc: func(ctx context.Context, method string) (*registry.ServerEntry, error) {
			return &registry.ServerEntry{ID: "server1"}, nil
		},
	}
	mockGT := &mockGatewayTools{}
	mockP := &mockPool{
		sendRequestFunc: func(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Result:  json.RawMessage(`{"success":true}`),
				ID:      req.ID,
			}, nil
		},
	}

	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	handleSingleRequest(ctx, []byte(`{"jsonrpc":"2.0","method":"test.tool","id":1}`), writer, mockR, mockGT, mockP)

	writer.Flush()
	output := readBuf.String()

	var resp proxy.JSONRPCResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	if resp.ID != 1.0 {
		t.Errorf("expected ID 1.0, got %v", resp.ID)
	}
}

func TestHandleBatchRequest(t *testing.T) {
	mockR := &mockRouter{
		routeFunc: func(ctx context.Context, method string) (*registry.ServerEntry, error) {
			return &registry.ServerEntry{ID: "server1"}, nil
		},
	}
	mockGT := &mockGatewayTools{}
	mockP := &mockPool{
		sendRequestFunc: func(ctx context.Context, serverName string, req *proxy.JSONRPCRequest, timeout time.Duration) (*proxy.JSONRPCResponse, error) {
			return &proxy.JSONRPCResponse{
				JSONRPC: "2.0",
				Result:  json.RawMessage(`{"ok":true}`),
				ID:      req.ID,
			}, nil
		},
	}

	batchInput := `[{"jsonrpc":"2.0","method":"tool1","id":1},{"jsonrpc":"2.0","method":"tool2","id":2}]`
	readBuf := &bytes.Buffer{}
	writer := bufio.NewWriter(readBuf)

	handleBatchRequest(ctx, []byte(batchInput), writer, mockR, mockGT, mockP)

	writer.Flush()
	output := readBuf.String()

	if output == "" {
		t.Fatal("expected batch response, got empty")
	}

	if output[0] != '[' {
		t.Errorf("expected batch response to start with [, got: %q", output)
	}
}

func TestHandleGatewayToolSync(t *testing.T) {
	mockGT := &mockGatewayTools{
		listServersFunc: func(ctx context.Context) ([]gateway.ServerInfo, error) {
			return []gateway.ServerInfo{
				{Name: "test-server", Status: "healthy", Transport: "stdio", ToolCount: 5},
			}, nil
		},
	}

	req := &proxy.JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "list_servers",
		ID:      1,
	}

	resp := handleGatewayToolSync(ctx, req, mockGT)

	if resp == nil {
		t.Fatal("expected response, got nil")
	}

	if resp.Error != nil {
		t.Errorf("unexpected error: %v", resp.Error)
	}

	if resp.ID != 1 {
		t.Errorf("expected ID 1, got %v", resp.ID)
	}
}