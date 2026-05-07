package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var projectRoot = "/Users/mmornati/Projects/leanproxy-mcp"

func getBinaryPath() string {
	return filepath.Join(projectRoot, "leanproxy-mcp")
}

func TestCLI_HelpCommand(t *testing.T) {
	binaryPath := getBinaryPath()
	var stdout bytes.Buffer
	cmd := exec.Command(binaryPath, "--help")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d. Output: %s", exitCode, stdout.String())
	}

	if !strings.Contains(stdout.String(), "LeanProxy MCP") {
		t.Errorf("Expected help output to contain 'LeanProxy MCP', got: %s", stdout.String())
	}
}

func TestCLI_VersionCommand(t *testing.T) {
	binaryPath := getBinaryPath()
	var stdout bytes.Buffer
	cmd := exec.Command(binaryPath, "version")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	if exitCode != 0 {
		t.Errorf("Expected exit code 0, got %d. Output: %s", exitCode, stdout.String())
	}

	if !strings.Contains(stdout.String(), ".") && !strings.Contains(stdout.String(), "v") {
		t.Errorf("Expected version output, got: %s", stdout.String())
	}
}

func TestCLI_InvalidCommand(t *testing.T) {
	binaryPath := getBinaryPath()
	var stderr bytes.Buffer
	cmd := exec.Command(binaryPath, "nonexistent-command")
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	if exitCode == 0 {
		t.Errorf("Expected non-zero exit code for invalid command")
	}

	if !strings.Contains(stderr.String(), "unknown command") && !strings.Contains(stderr.String(), "not found") {
		t.Logf("stderr: %s", stderr.String())
	}
}

func TestServer_ListCommand(t *testing.T) {
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "servers.yaml")
	os.Setenv("LEANPROXY_CONFIG", configPath)
	defer os.Unsetenv("LEANPROXY_CONFIG")

	binaryPath := getBinaryPath()
	var stdout bytes.Buffer
	cmd := exec.Command(binaryPath, "server", "list")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+configPath)

	cmd.Run()
	t.Logf("Server list output: %s", stdout.String())
}

func TestServer_AddCommand(t *testing.T) {
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "servers.yaml")
	os.Setenv("LEANPROXY_CONFIG", configPath)
	defer os.Unsetenv("LEANPROXY_CONFIG")

	binaryPath := getBinaryPath()
	var stderr bytes.Buffer
	cmd := exec.Command(binaryPath, "server", "add", "test-server", "echo", "hello", "--transport", "stdio")
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+configPath)

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	if exitCode != 0 {
		t.Logf("Exit code: %d", exitCode)
		t.Logf("stderr: %s", stderr.String())
	}
}

func TestServe_BasicStart(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping test in short mode")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "servers.yaml")
	createTestConfig(t, configPath)

	os.Setenv("LEANPROXY_CONFIG", configPath)
	defer os.Unsetenv("LEANPROXY_CONFIG")

	binaryPath := getBinaryPath()
	cmd := exec.Command(binaryPath, "serve", "--listen", "127.0.0.1:18082")
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+configPath)

	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	if err := cmd.Process.Kill(); err != nil {
		t.Logf("Failed to kill process: %v", err)
	}

	cmd.Wait()
}

func createTestConfig(t *testing.T, path string) {
	config := map[string]interface{}{
		"servers": []map[string]interface{}{
			{
				"name":      "test-echo",
				"command":   "echo",
				"args":      []string{"hello"},
				"transport": "stdio",
			},
		},
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("Failed to write config: %v", err)
	}
}

func TestCache_Commands(t *testing.T) {
	binaryPath := getBinaryPath()
	var stdout bytes.Buffer
	cmd := exec.Command(binaryPath, "cache", "--help")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	cmd.Run()
	t.Logf("Cache help output: %s", stdout.String())
}

func TestStatus_Commands(t *testing.T) {
	binaryPath := getBinaryPath()
	var stdout bytes.Buffer
	cmd := exec.Command(binaryPath, "status", "--help")
	cmd.Stdout = &stdout
	cmd.Stderr = &stdout

	cmd.Run()
	t.Logf("Status help output: %s", stdout.String())
}

func TestConfig_Validation(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantErr bool
	}{
		{
			name: "valid config",
			config: `servers:
  - name: test
    command: echo
    args: [hello]
    transport: stdio`,
			wantErr: false,
		},
		{
			name: "invalid transport",
			config: `servers:
  - name: test
    command: echo
    args: [hello]
    transport: invalid`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDir := t.TempDir()
			configPath := filepath.Join(testDir, fmt.Sprintf("config-%s.yaml", tt.name))
			if err := os.WriteFile(configPath, []byte(tt.config), 0644); err != nil {
				t.Fatalf("Failed to write config: %v", err)
			}

			binaryPath := getBinaryPath()
			var stdout bytes.Buffer
			cmd := exec.Command(binaryPath, "server", "list")
			cmd.Stdout = &stdout
			cmd.Stderr = &stdout
			cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+configPath)

			cmd.Run()
			t.Logf("Config validation: %s", stdout.String())
		})
	}
}

func TestDryRunMode(t *testing.T) {
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "servers.yaml")
	os.Setenv("LEANPROXY_CONFIG", configPath)
	defer os.Unsetenv("LEANPROXY_CONFIG")

	binaryPath := getBinaryPath()
	var stderr bytes.Buffer
	cmd := exec.Command(binaryPath, "--dry-run", "server", "add", "dryrun-test", "echo", "test")
	cmd.Stdout = &stderr
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+configPath)

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	if exitCode != 0 {
		t.Logf("Dry-run exit code: %d, stderr: %s", exitCode, stderr.String())
	}

	t.Logf("Dry-run output: %s", stderr.String())
}

func TestJSONRPC_HealthEndpoint(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	var health map[string]string
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if health["status"] != "ok" {
		t.Errorf("Expected status 'ok', got '%s'", health["status"])
	}
}

func TestJSONRPC_Initialize(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get("Content-Type")
		if contentType != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", contentType)
		}

		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("Failed to parse JSON-RPC request: %v", err)
		}

		if req["jsonrpc"] != "2.0" {
			t.Errorf("Expected JSONRPC 2.0, got %s", req["jsonrpc"])
		}

		if req["method"] == "initialize" {
			resp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":     req["id"],
				"result": map[string]interface{}{
					"protocolVersion": "1.0",
					"serverInfo": map[string]string{
						"name":    "LeanProxy-MCP",
						"version": "test",
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	requestBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method": "initialize",
		"id":     1,
	}
	body, _ := json.Marshal(requestBody)

	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	var rpcResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	if rpcResp["jsonrpc"] != "2.0" {
		t.Errorf("Expected JSONRPC 2.0 in response, got %s", rpcResp["jsonrpc"])
	}
}

func TestJSONRPC_InvalidMethod(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]interface{}
		json.Unmarshal(body, &req)

		if req["method"] == "invalid_method" {
			errResp := map[string]interface{}{
				"jsonrpc": "2.0",
				"id":     req["id"],
				"error": map[string]interface{}{
					"code":    -32601,
					"message": "Method not found",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(errResp)
		}
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	requestBody := map[string]interface{}{
		"jsonrpc": "2.0",
		"method": "invalid_method",
		"id":     1,
	}
	body, _ := json.Marshal(requestBody)

	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}
}

func TestJSONRPC_BatchRequest(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		var requests []json.RawMessage
		if err := json.Unmarshal(body, &requests); err != nil {
			t.Logf("Not a batch request: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			return
		}

		var responses []map[string]interface{}
		for _, reqRaw := range requests {
			var req map[string]interface{}
			json.Unmarshal(reqRaw, &req)
			responses = append(responses, map[string]interface{}{
				"jsonrpc": "2.0",
				"id":     req["id"],
				"result": map[string]interface{}{},
			})
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responses)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	batchRequest := []map[string]interface{}{
		{"jsonrpc": "2.0", "method": "tool1", "id": 1},
		{"jsonrpc": "2.0", "method": "tool2", "id": 2},
		{"jsonrpc": "2.0", "method": "tool3", "id": 3},
	}
	body, _ := json.Marshal(batchRequest)

	resp, err := http.Post(ts.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}
}

func TestErrorHandling(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		errResp := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":     1,
			"error": map[string]interface{}{
				"code":    -32600,
				"message": "Invalid Request",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errResp)
	})

	ts := httptest.NewServer(handler)
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", nil)
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", resp.StatusCode)
	}

	var rpcResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("Failed to parse error response: %v", err)
	}

	if rpcResp["error"] == nil {
		t.Errorf("Expected error in response")
	}
}