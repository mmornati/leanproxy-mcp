package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
)

func TestHTTPTransportConfig_Defaults(t *testing.T) {
	config := DefaultHTTPTransportConfig()

	if config.Port != "8080" {
		t.Errorf("expected port 8080, got %s", config.Port)
	}
	if config.ReadTimeout != 30*time.Second {
		t.Errorf("expected read timeout 30s, got %v", config.ReadTimeout)
	}
	if config.WriteTimeout != 30*time.Second {
		t.Errorf("expected write timeout 30s, got %v", config.WriteTimeout)
	}
	if config.MaxHeaderBytes != 1<<20 {
		t.Errorf("expected max header bytes 1MB, got %d", config.MaxHeaderBytes)
	}
}

func TestStreamableHTTPHandler_PostMcp(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`{"status":"ok"}`),
			ID:      req.ID,
		}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	body := []byte(`{"jsonrpc":"2.0","method":"test","id":1}`)
	resp, err := http.Post(server.URL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rpcResp.JSONRPC != "2.0" {
		t.Errorf("expected jsonrpc 2.0, got %s", rpcResp.JSONRPC)
	}
	if rpcResp.ID == nil {
		t.Error("expected id to be present, got nil")
	} else {
		id, ok := rpcResp.ID.(float64)
		if !ok || id != 1 {
			t.Errorf("expected id 1, got %v", rpcResp.ID)
		}
	}
}

func TestStreamableHTTPHandler_PostMcpParseError(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	body := []byte(`invalid json`)
	resp, err := http.Post(server.URL+"/mcp", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}

	var rpcResp JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if rpcResp.Error == nil {
		t.Fatal("expected error in response")
	}
	if rpcResp.Error.Code != errors.ErrCodeParseError {
		t.Errorf("expected parse error code %d, got %d", errors.ErrCodeParseError, rpcResp.Error.Code)
	}
}

func TestStreamableHTTPHandler_GetMcp(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/mcp")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected content-type application/json, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestStreamableHTTPHandler_GetSSE(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/sse", nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("expected content-type text/event-stream, got %s", resp.Header.Get("Content-Type"))
	}
}

func TestStreamableHTTPHandler_Health(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/health")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var health map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if health["status"] != "healthy" {
		t.Errorf("expected status healthy, got %s", health["status"])
	}
}

func TestStreamableHTTPHandler_NotFound(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	resp, err := http.Get(server.URL + "/unknown")
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", resp.StatusCode)
	}
}

func TestStreamableHTTPHandler_PostSSE(t *testing.T) {
	handler := func(ctx context.Context, req *JSONRPCRequest) (*JSONRPCResponse, error) {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			Result:  json.RawMessage(`{"status":"ok"}`),
			ID:      req.ID,
		}, nil
	}

	mcpHandler := StreamableHTTPHandler(handler, nil)
	server := httptest.NewServer(mcpHandler)
	defer server.Close()

	body := []byte(`{"jsonrpc":"2.0","method":"test","id":1}`)
	resp, err := http.Post(server.URL+"/sse", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestHTTPTransport_New(t *testing.T) {
	config := HTTPTransportConfig{
		Port:         "9090",
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	transport := NewHTTPTransport(config)
	if transport == nil {
		t.Fatal("expected non-nil transport")
	}

	if transport.GetAddr() != ":9090" {
		t.Errorf("expected addr :9090, got %s", transport.GetAddr())
	}
}

func TestHTTPTransport_Options(t *testing.T) {
	config := DefaultHTTPTransportConfig()
	transport := NewHTTPTransport(config, WithHTTPLogger(nil))

	if transport == nil {
		t.Fatal("expected non-nil transport")
	}
}