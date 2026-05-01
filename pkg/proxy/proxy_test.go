package proxy

import (
	"context"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"
)

func TestNewProxy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := NewProxy("localhost:8080", logger)

	if proxy == nil {
		t.Fatal("NewProxy returned nil")
	}
	if proxy.upstreamAddr != "localhost:8080" {
		t.Errorf("expected upstreamAddr 'localhost:8080', got '%s'", proxy.upstreamAddr)
	}
}

func TestProxyConnect(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := NewProxy("localhost:9999", logger)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := proxy.Connect(ctx)
	if err == nil {
		t.Error("expected connection error to non-existent server")
	}
}

func TestProxyClose(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := NewProxy("localhost:9999", logger)

	err := proxy.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestProxyCloseAfterConnect(t *testing.T) {
	listener, err := net.Listen("tcp", "localhost:9999")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := NewProxy(listener.Addr().String(), logger)

	ctx := context.Background()
	if err := proxy.Connect(ctx); err != nil {
		t.Fatalf("Connect() failed: %v", err)
	}

	if err := proxy.Close(); err != nil {
		t.Errorf("Close() after Connect() failed: %v", err)
	}
}

func TestParseJSONRPCRequest(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "valid request",
			data:    []byte(`{"jsonrpc":"2.0","method":"test","params":{},"id":1}`),
			wantErr: false,
		},
		{
			name:    "invalid json",
			data:    []byte(`{invalid}`),
			wantErr: true,
		},
		{
			name:    "missing method is valid JSON but method field empty",
			data:    []byte(`{"jsonrpc":"2.0","params":{},"id":1}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseJSONRPCRequest(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJSONRPCRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseJSONRPCResponse(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			name:    "valid response with result",
			data:    []byte(`{"jsonrpc":"2.0","result":{},"id":1}`),
			wantErr: false,
		},
		{
			name:    "valid error response",
			data:    []byte(`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":1}`),
			wantErr: false,
		},
		{
			name:    "invalid json",
			data:    []byte(`{invalid}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseJSONRPCResponse(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseJSONRPCResponse() error = %v, wantErr %v", err, err != nil)
			}
		})
	}
}

func TestIsBatchRequest(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		expected bool
	}{
		{
			name:     "batch request",
			data:     []byte(`[{"jsonrpc":"2.0","method":"test","id":1},{"jsonrpc":"2.0","method":"test2","id":2}]`),
			expected: true,
		},
		{
			name:     "single request",
			data:     []byte(`{"jsonrpc":"2.0","method":"test","id":1}`),
			expected: false,
		},
		{
			name:     "empty array - not a valid batch",
			data:     []byte(`[]`),
			expected: false,
		},
		{
			name:     "invalid json",
			data:     []byte(`{invalid}`),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsBatchRequest(tt.data)
			if result != tt.expected {
				t.Errorf("IsBatchRequest() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestParseJSONRPCBatchRequest(t *testing.T) {
	data := []byte(`[
		{"jsonrpc":"2.0","method":"test","id":1},
		{"jsonrpc":"2.0","method":"test2","id":2}
	]`)

	reqs, err := ParseJSONRPCBatchRequest(data)
	if err != nil {
		t.Fatalf("ParseJSONRPCBatchRequest() failed: %v", err)
	}
	if len(reqs) != 2 {
		t.Errorf("expected 2 requests, got %d", len(reqs))
	}
}

func TestNewJSONRPCError(t *testing.T) {
	err := NewJSONRPCError(-32600, "Invalid Request")
	if err.Code != -32600 {
		t.Errorf("expected code -32600, got %d", err.Code)
	}
	if err.Message != "Invalid Request" {
		t.Errorf("expected message 'Invalid Request', got '%s'", err.Message)
	}
}

func TestJSONRPCErrorError(t *testing.T) {
	err := NewJSONRPCError(-32600, "Invalid Request")
	expected := "jsonrpc: error -32600: Invalid Request"
	if err.Error() != expected {
		t.Errorf("Error() = '%s', expected '%s'", err.Error(), expected)
	}
}

func TestProxyForwardLoopContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "localhost:9998")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := NewProxy(listener.Addr().String(), logger)

	rClient, wClient := net.Pipe()
	defer rClient.Close()
	defer wClient.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = proxy.ForwardLoop(ctx, rClient)
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestConcurrentProxyOperations(t *testing.T) {
	listener, err := net.Listen("tcp", "localhost:9997")
	if err != nil {
		t.Fatalf("failed to create test listener: %v", err)
	}
	defer listener.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := NewProxy(listener.Addr().String(), logger)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			proxy.Connect(ctx)
			proxy.Close()
		}()
	}
	wg.Wait()
}
