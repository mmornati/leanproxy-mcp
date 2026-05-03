package socket

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestServerLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		RateLimit:  100,
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Serve(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	select {
	case err := <-errChan:
		t.Logf("Serve returned: %v", err)
	default:
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"test.echo","params":{},"id":1}`
	_, err = fmt.Fprintf(conn, "%s\n", req)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	resp := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	var rpcResp map[string]interface{}
	if err := json.Unmarshal(resp[:n], &rpcResp); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if rpcResp["id"] != float64(1) {
		t.Errorf("Expected id 1, got %v", rpcResp["id"])
	}

	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	if _, err := os.Stat(socketPath); err == nil {
		t.Error("Socket file should be removed after shutdown")
	}
}

func TestJSONRPCRequestParsing(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		RateLimit:  100,
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	resolver := &mockTokenResolver{}
	handler := NewHandler(resolver, nil, nil, nil, nil)
	handler.RegisterMethods(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"token.resolve","params":{"uri":"api://example"},"id":1}`
	_, err = fmt.Fprintf(conn, "%s\n", req)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	resp := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	var rpcResp map[string]interface{}
	if err := json.Unmarshal(resp[:n], &rpcResp); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if rpcResp["id"] != float64(1) {
		t.Errorf("Expected id 1, got %v", rpcResp["id"])
	}

	result, ok := rpcResp["result"].(map[string]interface{})
	if !ok {
		t.Error("Expected result to be a map")
	} else {
		_ = result
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}

func TestMalformedRequest(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		RateLimit:  100,
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	req := `{invalid jsonrpc request}`
	_, err = fmt.Fprintf(conn, "%s\n", req)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	resp := make([]byte, 4096)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := conn.Read(resp)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	var rpcResp map[string]interface{}
	if err := json.Unmarshal(resp[:n], &rpcResp); err != nil {
		t.Fatalf("Unmarshal response failed: %v", err)
	}

	if rpcResp["error"] == nil {
		t.Error("Expected error response for malformed request")
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	server.Shutdown(shutdownCtx)
	shutdownCancel()
}

func TestConcurrentConnections(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		RateLimit:  100,
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	conn1, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial 1 failed: %v", err)
	}
	defer conn1.Close()

	conn2, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial 2 failed: %v", err)
	}
	defer conn2.Close()

	req := `{"jsonrpc":"2.0","method":"test.echo","params":{},"id":1}`
	_, err = fmt.Fprintf(conn1, "%s\n", req)
	if err != nil {
		t.Fatalf("Write 1 failed: %v", err)
	}

	_, err = fmt.Fprintf(conn2, "%s\n", req)
	if err != nil {
		t.Fatalf("Write 2 failed: %v", err)
	}

	resp1 := make([]byte, 4096)
	conn1.SetReadDeadline(time.Now().Add(2 * time.Second))
	n1, err := conn1.Read(resp1)
	if err != nil {
		t.Fatalf("Read 1 failed: %v", err)
	}

	resp2 := make([]byte, 4096)
	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	n2, err := conn2.Read(resp2)
	if err != nil {
		t.Fatalf("Read 2 failed: %v", err)
	}

	var rpcResp1, rpcResp2 map[string]interface{}
	json.Unmarshal(resp1[:n1], &rpcResp1)
	json.Unmarshal(resp2[:n2], &rpcResp2)

	if rpcResp1["id"] != float64(1) || rpcResp2["id"] != float64(1) {
		t.Error("Both connections should receive valid responses")
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	server.Shutdown(shutdownCtx)
	shutdownCancel()
}

func TestMessageTooLarge(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 100,
		RateLimit:  100,
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	time.Sleep(100 * time.Millisecond)

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial failed: %v", err)
	}
	defer conn.Close()

	largeReq := `{"jsonrpc":"2.0","method":"test.echo","params":{},"id":1}` + string(make([]byte, 200))
	_, err = fmt.Fprintf(conn, "%s\n", largeReq)
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	server.Shutdown(shutdownCtx)
	shutdownCancel()
}