package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func writeMCPServerScript(t *testing.T, path string) {
	t.Helper()
	script := `#!/usr/bin/env python3
import sys, json, os

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
            result = {"content": [{"type": "text", "text": f"Echo: {msg}"}]}
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
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to write MCP server script: %v", err)
	}
}

func TestMCPServer_LoadAndToolCall(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			t.Skip("python3/python not available")
		}
	}

	testDir := t.TempDir()

	serverScript := filepath.Join(testDir, "mcp-test-server.py")
	writeMCPServerScript(t, serverScript)

	configPath := filepath.Join(testDir, "servers.yaml")
	config := fmt.Sprintf(`servers:
  - name: test-server
    transport: stdio
    stdio:
      command: %s
      args:
        - %s
`, pythonPath, serverScript)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	t.Logf("Starting leanproxy server run --stdio with config %s", configPath)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get working directory: %v", err)
	}
	binaryPath := filepath.Join(wd, "leanproxy-mcp")

	var stderr bytes.Buffer
	stdinReader, stdinWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create stdin pipe: %v", err)
	}
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create stdout pipe: %v", err)
	}

	cmd := exec.Command(binaryPath,
		"server", "run", "--stdio",
		"--config", configPath,
	)
	cmd.Stdin = stdinReader
	cmd.Stdout = stdoutWriter
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"LEANPROXY_CONFIG="+configPath,
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start leanproxy: %v", err)
	}

	stdoutWriter.Close()
	stdinReader.Close()

	defer func() {
		cmd.Process.Kill()
		cmd.Wait()
	}()

	serverOut := bufio.NewReader(stdoutReader)

	sendRequest := func(req map[string]interface{}) map[string]interface{} {
		reqBytes, _ := json.Marshal(req)
		_, err := stdinWriter.Write(append(reqBytes, '\n'))
		if err != nil {
			t.Fatalf("Failed to write request: %v", err)
		}

		line, err := serverOut.ReadBytes('\n')
		if err != nil {
			t.Fatalf("Failed to read response: %v.\nStderr: %s", err, stderr.String())
		}

		var resp map[string]interface{}
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("Failed to parse response: %s\nRaw: %s\nStderr: %s", err, string(line), stderr.String())
		}
		return resp
	}

	sendNotification := func(req map[string]interface{}) {
		reqBytes, _ := json.Marshal(req)
		_, err := stdinWriter.Write(append(reqBytes, '\n'))
		if err != nil {
			t.Fatalf("Failed to write notification: %v", err)
		}
	}

	t.Run("initialize handshake", func(t *testing.T) {
		resp := sendRequest(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"clientInfo": map[string]string{
					"name":    "test-client",
					"version": "1.0",
				},
			},
			"id": 1,
		})

		if errVal, hasErr := resp["error"]; hasErr {
			t.Fatalf("Unexpected error: %v", errVal)
		}

		result, ok := resp["result"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected result, got: %v", resp)
		}

		sv, ok := result["protocolVersion"].(string)
		if !ok || sv != "2024-11-05" {
			t.Errorf("Expected protocolVersion 2024-11-05, got %v", result["protocolVersion"])
		}

		si, ok := result["serverInfo"].(map[string]interface{})
		if !ok {
			t.Errorf("Expected serverInfo in result")
		} else if name, _ := si["name"].(string); name != "leanproxy-mcp" {
			t.Errorf("Expected server name leanproxy-mcp, got %v", si["name"])
		}
	})

	sendNotification(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	callTool := func(name string, args map[string]interface{}, id int) map[string]interface{} {
		params := map[string]interface{}{
			"name":      name,
			"arguments": args,
		}
		return sendRequest(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "tools/call",
			"params":  params,
			"id":      id,
		})
	}

	assertToolResult := func(t *testing.T, resp map[string]interface{}, expectedText string) {
		t.Helper()
		if errVal, hasErr := resp["error"]; hasErr {
			t.Fatalf("Unexpected error: %v", errVal)
		}

		result, ok := resp["result"].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected result in response, got: %v", resp)
		}

		content, ok := result["content"].([]interface{})
		if !ok || len(content) == 0 {
			t.Fatalf("Expected content in result, got: %v", result)
		}

		textItem, ok := content[0].(map[string]interface{})
		if !ok {
			t.Fatalf("Expected content item to be object, got: %v", content[0])
		}

		text, ok := textItem["text"].(string)
		if !ok {
			t.Fatalf("Expected text field, got: %v", textItem)
		}

		if text != expectedText {
			t.Errorf("Expected %q, got %q", expectedText, text)
		}
	}

	t.Run("echo tool call", func(t *testing.T) {
		resp := callTool("test-server_echo", map[string]interface{}{
			"message": "hello world",
		}, 2)
		assertToolResult(t, resp, "Echo: hello world")
	})

	t.Run("add tool call", func(t *testing.T) {
		resp := callTool("test-server_add", map[string]interface{}{
			"a": 3,
			"b": 4,
		}, 3)
		assertToolResult(t, resp, "Result: 7")
	})
}

func TestMCPServer_ServerListAfterLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping in short mode")
	}
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			t.Skip("python3/python not available")
		}
	}

	testDir := t.TempDir()

	serverScript := filepath.Join(testDir, "mcp-test-server.py")
	writeMCPServerScript(t, serverScript)

	configPath := filepath.Join(testDir, "servers.yaml")
	config := fmt.Sprintf(`servers:
  - name: test-server
    transport: stdio
    stdio:
      command: %s
      args:
        - %s
`, pythonPath, serverScript)
	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}

	os.Setenv("LEANPROXY_CONFIG", configPath)
	defer os.Unsetenv("LEANPROXY_CONFIG")

	stdout, stderr, exitCode := runBinary("server", "list")
	t.Logf("server list output: stdout=%s stderr=%s exit=%d", stdout, stderr, exitCode)

	if exitCode != 0 {
		t.Fatalf("server list should succeed, got exit code %d", exitCode)
	}

	if !strings.Contains(stdout, "test-server") {
		t.Errorf("Expected test-server in server list output, got: %s", stdout)
	}
}
