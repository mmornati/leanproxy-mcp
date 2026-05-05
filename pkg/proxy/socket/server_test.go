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

	"github.com/stretchr/testify/require"
)

func waitForSocket(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := net.Dial("unix", path); err == nil {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("socket not ready")
}

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
	require.NoError(t, err, "NewServer failed")

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

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

	conn.Close()
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
	require.NoError(t, err, "NewServer failed")

	resolver := &mockTokenResolver{}
	handler := NewHandler(resolver, nil, nil, nil, nil)
	handler.RegisterMethods(server)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial failed")
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

	conn.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
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

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return nil, fmt.Errorf("echo error")
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial failed")

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

	conn.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
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
	require.NoError(t, err, "NewServer failed")

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn1, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial 1 failed")
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

	conn1.Close()
	conn2.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}
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
	require.NoError(t, err, "NewServer failed")

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return nil, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial failed")
	defer conn.Close()

	largeReq := `{"jsonrpc":"2.0","method":"test.echo","params":{},"id":1}` + string(make([]byte, 200))
	_, err = fmt.Fprintf(conn, "%s\n", largeReq)
	require.NoError(t, err, "Write failed")

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "server not ready after write")

	conn.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}

func TestAuthenticateWithToken(t *testing.T) {
	config := ServerConfig{
		Path:       "/tmp/test.sock",
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		AuthToken:  "secret-token",
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if !server.Authenticate("secret-token") {
		t.Error("Expected valid token to authenticate")
	}

	if server.Authenticate("wrong-token") {
		t.Error("Expected invalid token to fail authentication")
	}
}

func TestAuthenticateNoTokenConfigured(t *testing.T) {
	config := ServerConfig{
		Path:       "/tmp/test.sock",
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
	}

	server, err := NewServer(config, nil)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if !server.Authenticate("any-token") {
		t.Error("Expected any token to succeed when no auth token configured")
	}
}

func TestRequestWithAuthToken(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		AuthToken:  "my-secret-token",
	}

	server, err := NewServer(config, nil)
	require.NoError(t, err, "NewServer failed")

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial failed")
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"test.echo","params":{},"id":1,"auth_token":"my-secret-token"}`
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

	if rpcResp["error"] != nil {
		t.Error("Expected successful response with valid token")
	}

	conn.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}

func TestRequestWithoutAuthToken(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		AuthToken:  "my-secret-token",
	}

	server, err := NewServer(config, nil)
	require.NoError(t, err, "NewServer failed")

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial failed")
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

	errObj, ok := rpcResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected error response when auth token missing")
	}

	if errObj["code"].(float64) != -32604 {
		t.Errorf("Expected error code -32604 (unauthorized), got %v", errObj["code"])
	}

	conn.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}

func TestRequestWithInvalidAuthToken(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	config := ServerConfig{
		Path:       socketPath,
		Perm:       0700,
		MaxMsgSize: 1024 * 1024,
		AuthToken:  "my-secret-token",
	}

	server, err := NewServer(config, nil)
	require.NoError(t, err, "NewServer failed")

	server.RegisterMethod("test.echo", func(ctx context.Context, params json.RawMessage) (interface{}, error) {
		return map[string]string{"echo": "ok"}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		server.Serve(ctx)
	}()

	require.NoError(t, waitForSocket(socketPath, 100*time.Millisecond), "socket not ready")

	conn, err := net.Dial("unix", socketPath)
	require.NoError(t, err, "Dial failed")
	defer conn.Close()

	req := `{"jsonrpc":"2.0","method":"test.echo","params":{},"id":1,"auth_token":"wrong-token"}`
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

	errObj, ok := rpcResp["error"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected error response when auth token is invalid")
	}

	if errObj["code"].(float64) != -32604 {
		t.Errorf("Expected error code -32604 (unauthorized), got %v", errObj["code"])
	}

	conn.Close()
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	server.Shutdown(shutdownCtx)
}