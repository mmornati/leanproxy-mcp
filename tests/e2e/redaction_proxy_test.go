package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file is the end-to-end integration harness for the secret-redaction
// wiring introduced by PR #274 and the pool/concurrency fixes in PR #275,
// guarded by the PR #280 review fixes. It drives the real `serve` binary over
// a real TCP socket and asserts the observable redaction guarantees.
//
// Reachable surface: the `serve` TCP proxy routes gateway tools
// (list_servers / list_tools / invoke_tool) in-process and, since PR #281,
// backend tool calls (both the standard tools/call form and the namespaced
// server.tool method form) through a router registry populated from each
// server's tool cache. What IS covered here, end to end through the binary:
//
//   - gateway-tool results are redacted before reaching the client (#274),
//   - batch gateway-tool responses are redacted (#274 batch wiring),
//   - invoke_tool error messages are redacted (#274 / #280 B2),
//   - async responses over one connection are serialized without corruption
//     (#275 writer race) and stay redacted under concurrency,
//   - backend tool calls route to the stdio backend with params redacted
//     upstream and responses redacted downstream (#281),
//   - the process stays up and never leaks a secret in any response.

// proxyClient is a minimal newline-delimited JSON-RPC client for the serve
// TCP listener.
type proxyClient struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialProxy(t *testing.T, addr string) *proxyClient {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &proxyClient{t: t, conn: conn, r: bufio.NewReader(conn)}
}

// call sends one JSON-RPC request and reads exactly one response line.
func (c *proxyClient) call(method string, params interface{}, id int) map[string]interface{} {
	c.t.Helper()
	req := map[string]interface{}{"jsonrpc": "2.0", "method": method, "id": id}
	if params != nil {
		req["params"] = params
	}
	raw, err := json.Marshal(req)
	if err != nil {
		c.t.Fatalf("marshal request: %v", err)
	}
	if _, err := c.conn.Write(append(raw, '\n')); err != nil {
		c.t.Fatalf("write request: %v", err)
	}
	return c.readResponse()
}

func (c *proxyClient) readResponse() map[string]interface{} {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read response line: %v", err)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(line, &resp); err != nil {
		c.t.Fatalf("response %q is not a JSON-RPC object: %v", string(line), err)
	}
	return resp
}

// readRawResponse reads exactly one newline-delimited response without
// assuming its JSON shape (batch responses are arrays).
func (c *proxyClient) readRawResponse() []byte {
	c.t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		c.t.Fatalf("set read deadline: %v", err)
	}
	line, err := c.r.ReadBytes('\n')
	if err != nil {
		c.t.Fatalf("read response line: %v", err)
	}
	return line
}

// waitForPort polls a TCP listener until it accepts connections.
func waitForPort(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("serve listener %s never came up", addr)
}

// startRedactionServe writes a config and launches the serve binary with the
// given stdio backend script and optional bouncer YAML block. Returns the
// listener address and the pidfile path.
func startRedactionServe(t *testing.T, script, serverName, bouncerBlock string) (addr, pidFile string) {
	t.Helper()
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			t.Skip("python3/python not available")
		}
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	config := fmt.Sprintf(`version: "1.0"
servers:
  - name: %s
    transport: stdio
    enabled: true
    stdio:
      command: %s
      args: ["%s"]
%s`, serverName, pythonPath, script, bouncerBlock)
	writeFile(t, configPath, config)

	port := freePort(t)
	addr = fmt.Sprintf("127.0.0.1:%d", port)
	pidFile = filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", addr,
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	t.Cleanup(func() { stopServe(t, pidFile, logFile) })

	waitForPort(t, addr)
	return addr, pidFile
}

// secretServerName carries a built-in-detectable secret so gateway results
// must redact it, and a custom token built-ins do not match (used by
// assertNoSecret).
const (
	secretServerName = "srv-AKIAIOSFODNN7EXAMPLE"
	customToken      = "customtok_SECRETXYZ123"
)

func responseText(t *testing.T, resp map[string]interface{}) string {
	t.Helper()
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal response for assertion: %v", err)
	}
	return string(raw)
}

func assertNoSecret(t *testing.T, resp map[string]interface{}) {
	t.Helper()
	body := responseText(t, resp)
	for _, secret := range []string{"AKIAIOSFODNN7EXAMPLE", customToken} {
		if strings.Contains(body, secret) {
			t.Fatalf("secret %q leaked to client: %s", secret, body)
		}
	}
}

// writeLoggingMCPServerScript writes an MCP stdio server like
// writeMCPServerScript but additionally appends every raw request line to
// logPath and injects a built-in-detectable secret into the echo response so
// tests can assert both redaction directions over a real tool call.
func writeLoggingMCPServerScript(t *testing.T, path, logPath string) {
	t.Helper()
	script := fmt.Sprintf(`#!/usr/bin/env python3
import sys, json, os

SECRET = "AKIAIOSFODNN7EXAMPLE"
LOG = %q

TOOLS = [
    {
        "name": "echo",
        "description": "Echo input back",
        "inputSchema": {
            "type": "object",
            "properties": {"message": {"type": "string"}},
            "required": ["message"]
        }
    },
    {
        "name": "add",
        "description": "Add two numbers",
        "inputSchema": {
            "type": "object",
            "properties": {"a": {"type": "number"}, "b": {"type": "number"}},
            "required": ["a", "b"]
        }
    }
]

for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    with open(LOG, "a") as f:
        f.write(line + "\n")
    try:
        req = json.loads(line)
    except json.JSONDecodeError:
        continue

    method = req.get("method", "")
    rid = req.get("id")
    params = req.get("params", {})

    if method == "initialize":
        resp = {
            "jsonrpc": "2.0", "id": rid,
            "result": {
                "protocolVersion": "2024-11-05",
                "capabilities": {"tools": {}},
                "serverInfo": {"name": "test-mcp-server", "version": "1.0.0"}
            }
        }
    elif method == "notifications/initialized":
        continue
    elif method == "tools/list":
        resp = {"jsonrpc": "2.0", "id": rid, "result": {"tools": TOOLS}}
    elif method == "tools/call":
        tool_name = params.get("name", "")
        args = params.get("arguments", {})
        if tool_name == "echo":
            msg = args.get("message", "")
            result = {"content": [{"type": "text", "text": f"Echo: {msg} {SECRET}"}]}
        elif tool_name == "add":
            a = args.get("a", 0)
            b = args.get("b", 0)
            result = {"content": [{"type": "text", "text": f"Result: {a + b}"}]}
        else:
            result = {"isError": True, "content": [{"type": "text", "text": f"Unknown tool: {tool_name}"}]}
        resp = {"jsonrpc": "2.0", "id": rid, "result": result}
    elif method == "ping":
        resp = {"jsonrpc": "2.0", "id": rid, "result": {}}
    else:
        resp = {"jsonrpc": "2.0", "id": rid, "result": {}}

    sys.stdout.write(json.dumps(resp) + "\n")
    sys.stdout.flush()
`, logPath)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to write logging MCP server script: %v", err)
	}
}

// TestServe_274_RedactionWiring: PR #274 guarantees gateway-tool results are
// redacted before reaching the client, in both single and batch requests,
// with the request ID preserved. Runs against the real binary.
func TestServe_274_RedactionWiring(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	script := filepath.Join(t.TempDir(), "mcp-test-server.py")
	writeMCPServerScript(t, script)

	addr, pidFile := startRedactionServe(t, script, secretServerName, "")
	c := dialProxy(t, addr)

	// Single list_servers: the server name embeds an AWS key that must be
	// redacted in the result.
	resp := c.call("list_servers", nil, 1)
	if id, ok := resp["id"].(float64); !ok || id != 1 {
		t.Fatalf("list_servers ID not preserved: %v", resp["id"])
	}
	if !strings.Contains(responseText(t, resp), "[SECRET_REDACTED]") {
		t.Fatalf("list_servers result not redacted: %s", responseText(t, resp))
	}
	assertNoSecret(t, resp)

	// Batch of gateway tools: every element must be redacted and each ID
	// preserved.
	batch := []map[string]interface{}{
		{"jsonrpc": "2.0", "method": "list_servers", "id": 10},
		{"jsonrpc": "2.0", "method": "list_servers", "id": 11},
	}
	raw, err := json.Marshal(batch)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.conn.Write(append(raw, '\n')); err != nil {
		t.Fatalf("write batch: %v", err)
	}
	var elems []map[string]interface{}
	if err := json.Unmarshal(c.readRawResponse(), &elems); err != nil {
		t.Fatalf("batch response is not an array: %v", err)
	}
	if len(elems) != 2 {
		t.Fatalf("expected 2 batch responses, got %d", len(elems))
	}
	for _, e := range elems {
		assertNoSecret(t, e)
		if !strings.Contains(responseText(t, e), "[SECRET_REDACTED]") {
			t.Fatalf("batch element not redacted: %s", responseText(t, e))
		}
	}

	// invoke_tool error path (B2): the gateway error must reach the client
	// without leaking the request params, and the process must stay up.
	resp = c.call("invoke_tool", map[string]interface{}{"server_name": "srv-a", "name": "echo", "arguments": map[string]interface{}{"message": "hi"}}, 3)
	assertNoSecret(t, resp)

	if !pidAlive(t, pidFile) {
		t.Fatalf("serve exited during redaction assertions")
	}
}

// TestServe_275_ConcurrentAsyncResponses: PR #275 serialized the async
// writer; over a single connection N concurrent requests must each yield a
// parseable, correctly-IDed, redacted response (no interleaved/corrupted
// writes).
func TestServe_275_ConcurrentAsyncResponses(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	script := filepath.Join(t.TempDir(), "mcp-test-server.py")
	writeMCPServerScript(t, script)

	addr, pidFile := startRedactionServe(t, script, secretServerName, "")
	c := dialProxy(t, addr)

	const n = 30
	want := make(map[int]bool, n)
	for i := 0; i < n; i++ {
		req := map[string]interface{}{"jsonrpc": "2.0", "method": "list_servers", "id": 100 + i}
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.conn.Write(append(raw, '\n')); err != nil {
			t.Fatalf("write request %d: %v", i, err)
		}
		want[100+i] = true
	}

	for i := 0; i < n; i++ {
		resp := c.readResponse()
		id, ok := resp["id"].(float64)
		if !ok {
			t.Fatalf("response missing/odd id: %s", responseText(t, resp))
		}
		if !want[int(id)] {
			t.Fatalf("unexpected response id %v", id)
		}
		delete(want, int(id))
		assertNoSecret(t, resp)
		if !strings.Contains(responseText(t, resp), "[SECRET_REDACTED]") {
			t.Fatalf("concurrent response not redacted: %s", responseText(t, resp))
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing responses for ids %v", want)
	}
	if !pidAlive(t, pidFile) {
		t.Fatalf("serve exited during concurrent requests")
	}
}

// TestServe_281_BackendToolRouting: PR #281 — the serve TCP listener must
// route real backend tool calls. Before this fix the router registry was
// never populated in serve mode, so every backend tools/call (and namespaced
// server.tool method) returned -32601 Method not found and the entire
// request/response redaction pipeline was unreachable for real tool traffic.
//
// This test drives real tool traffic through the listener:
//   - a secret sent in tool arguments must reach the stdio backend redacted
//     (verified via a logging backend that records every request line), and
//   - a secret injected into the backend response must be redacted before it
//     reaches the client,
//   - the forwarded request must carry the bare tool name plus an arguments
//     envelope (the backend MCP contract), for both the tools/call and the
//     namespaced-method forms.
func TestServe_281_BackendToolRouting(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	logPath := filepath.Join(testDir, "backend-received.log")
	script := filepath.Join(testDir, "mcp-logging-server.py")
	writeLoggingMCPServerScript(t, script, logPath)

	const secret = "AKIAIOSFODNN7EXAMPLE"
	serverName := "srv-281"

	addr, pidFile := startRedactionServe(t, script, serverName, "")
	c := dialProxy(t, addr)

	// 1. Standard MCP tools/call form with a namespaced tool name.
	resp := c.call("tools/call", map[string]interface{}{
		"name":      serverName + ".echo",
		"arguments": map[string]interface{}{"message": "hello " + secret},
	}, 1)
	if errVal, hasErr := resp["error"]; hasErr {
		t.Fatalf("tools/call unexpected error: %v", errVal)
	}
	body := responseText(t, resp)
	if strings.Contains(body, secret) {
		t.Fatalf("backend tool response leaked secret to client: %s", body)
	}
	if !strings.Contains(body, "[SECRET_REDACTED]") {
		t.Fatalf("expected redacted response from backend tool: %s", body)
	}
	if !strings.Contains(body, "hello [SECRET_REDACTED]") {
		t.Fatalf("expected echoed redacted message in response: %s", body)
	}

	// 2. Namespaced-method form (server.tool as the JSON-RPC method).
	resp = c.call(serverName+".echo", map[string]interface{}{
		"message": "method form " + secret,
	}, 2)
	if errVal, hasErr := resp["error"]; hasErr {
		t.Fatalf("namespaced method unexpected error: %v", errVal)
	}
	body = responseText(t, resp)
	if strings.Contains(body, secret) {
		t.Fatalf("namespaced-method response leaked secret: %s", body)
	}
	if !strings.Contains(body, "method form [SECRET_REDACTED]") {
		t.Fatalf("expected echoed redacted message (method form): %s", body)
	}

	// 3. The backend must have received redacted params, forwarded in the
	//    MCP tools/call envelope with the bare tool name.
	var logData []byte
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err == nil && len(data) > 0 {
			logData = data
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(logData) == 0 {
		t.Fatalf("backend never wrote request log at %s", logPath)
	}
	logText := string(logData)
	if strings.Contains(logText, secret) {
		t.Fatalf("backend received unredacted secret: %s", logText)
	}
	if !strings.Contains(logText, "[SECRET_REDACTED]") {
		t.Fatalf("backend did not receive redacted params: %s", logText)
	}
	for _, line := range strings.Split(logText, "\n") {
		if !strings.Contains(line, `"method":"tools/call"`) {
			continue
		}
		var req struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.Fatalf("malformed backend request line: %s", line)
		}
		if req.Params.Name != "echo" {
			t.Fatalf("expected bare tool name %q in forwarded params, got %q", "echo", req.Params.Name)
		}
	}

	if !pidAlive(t, pidFile) {
		t.Fatalf("serve exited during backend tool routing")
	}
}
