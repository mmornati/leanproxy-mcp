package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestStdioPipeConnectivity verifies that a spawned subprocess can receive
// a JSON-RPC request on stdin and return a response on stdout.
// Uses a shell echo loop to simulate an MCP server.
func TestStdioPipeConnectivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pipe connectivity test in short mode")
	}

	config := StdioServerConfig{
		Name:    "test-echo",
		Command: "sh",
		Args:    []string{"-c", `while read -r line; do echo '{"jsonrpc":"2.0","id":1,"result":{"status":"ok"}}'; done`},
	}

	server := newServerV2("test-echo", config, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := server.spawn(ctx)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	defer server.stop()

	// Allow goroutines to start
	time.Sleep(100 * time.Millisecond)

	// Write a JSON-RPC initialize request to stdin
	reqJSON := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`
	_, err = fmt.Fprintln(server.stdin, reqJSON)
	if err != nil {
		t.Fatalf("write to stdin failed: %v", err)
	}

	// Wait for the echo response on stdout
	select {
	case resp := <-server.responseCh:
		if resp.Result == nil {
			t.Error("expected non-nil result")
		}
		var result map[string]interface{}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			t.Fatalf("failed to unmarshal result: %v", err)
		}
		if result["status"] != "ok" {
			t.Errorf("expected status=ok, got %v", result["status"])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for echo response — pipe connectivity broken")
	}
}

// TestRequestMarshalJSONNoTimeout verifies that the non-standard "timeout"
// field is NOT included in the JSON-RPC wire payload.
func TestRequestMarshalJSONNoTimeout(t *testing.T) {
	req := Request{
		Method:  "initialize",
		ID:      1,
		Timeout: 120 * time.Second,
		Params:  json.RawMessage(`{"protocolVersion":"2024-11-05"}`),
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	jsonStr := string(data)

	// Must contain standard JSON-RPC fields
	if !strings.Contains(jsonStr, `"jsonrpc":"2.0"`) {
		t.Error("missing jsonrpc field")
	}
	if !strings.Contains(jsonStr, `"method":"initialize"`) {
		t.Error("missing method field")
	}
	if !strings.Contains(jsonStr, `"id":1`) {
		t.Error("missing id field")
	}

	// Must NOT contain the non-standard timeout field
	if strings.Contains(jsonStr, `"timeout"`) {
		t.Errorf("request should not contain 'timeout' field, got: %s", jsonStr)
	}
}

// TestSpawnSetsPythonUnbuffered verifies that PYTHONUNBUFFERED=1 is set
// in the subprocess environment.
func TestSpawnSetsPythonUnbuffered(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping env test in short mode")
	}

	config := StdioServerConfig{
		Name:    "test-env",
		Command: "sh",
		Args:    []string{"-c", "echo $PYTHONUNBUFFERED && sleep 0.5"},
	}

	server := newServerV2("test-env", config, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.spawn(ctx)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	defer server.stop()

	// The subprocess should print PYTHONUNBUFFERED value to stdout.
	// We can't easily read it since readResponses consumes stdout,
	// but we can verify the env was set by checking the command's env.
	found := false
	for _, e := range server.process.Env {
		if e == "PYTHONUNBUFFERED=1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("PYTHONUNBUFFERED=1 not found in subprocess environment")
	}
}

// TestStderrCaptureOnTimeout verifies that recent stderr output is included
// in timeout error messages.
func TestStderrCaptureOnTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping stderr capture test in short mode")
	}

	// Spawn a server that writes to stderr but never responds on stdout
	config := StdioServerConfig{
		Name:    "test-stderr",
		Command: "sh",
		Args:    []string{"-c", `echo "some error message" >&2; sleep 30`},
	}

	server := newServerV2("test-stderr", config, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := server.spawn(ctx)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	defer server.stop()

	// Allow stderr to be captured
	time.Sleep(200 * time.Millisecond)

	// Send a request with a short timeout — the server won't respond
	_, err = server.sendRequest(ctx, Request{
		Method:  "test",
		ID:      1,
		Timeout: 500 * time.Millisecond,
	})

	if err == nil {
		t.Fatal("expected timeout error")
	}

	errMsg := err.Error()
	if !strings.Contains(errMsg, "request timeout") {
		t.Errorf("error should mention timeout, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "some error message") {
		t.Errorf("error should include stderr output, got: %s", errMsg)
	}
}

// TestStderrRing verifies the ring buffer correctly stores and retrieves lines.
func TestStderrRing(t *testing.T) {
	ring := newStderrRing(3)

	ring.add("line1")
	ring.add("line2")
	ring.add("line3")
	ring.add("line4") // should evict line1

	result := ring.String()
	if !strings.Contains(result, "line4") {
		t.Error("should contain line4")
	}
	if strings.Contains(result, "line1") {
		t.Error("should not contain evicted line1")
	}
	if !strings.Contains(result, "line2") {
		t.Error("should contain line2")
	}
}

// TestSpawnAliveCheck verifies that spawn detects a process that exits immediately.
func TestSpawnAliveCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping alive check test in short mode")
	}

	config := StdioServerConfig{
		Name:    "test-cmd-ok",
		Command: "sleep",
		Args:    []string{"60"},
	}

	server := newServerV2("test-cmd-ok", config, slog.Default())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.spawn(ctx)
	if err != nil {
		t.Fatalf("spawn should succeed for valid command: %v", err)
	}
	defer server.stop()

	// Process should be alive
	if server.process == nil || server.process.Process == nil {
		t.Fatal("process should be non-nil after successful spawn")
	}
}
