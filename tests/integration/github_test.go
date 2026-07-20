//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitHubServer_Initialize(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set — skipping integration test")
	}

	serverPath := buildGitHubServer(t)
	if serverPath == "" {
		t.Fatal("failed to build github server binary")
	}

	cmd := exec.CommandContext(context.Background(), serverPath)
	cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer cmd.Process.Kill()

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "integration-test",
				"version": "1.0",
			},
		},
		"id": 1,
	}

	reqBytes, _ := json.Marshal(request)
	fmt.Fprintln(stdin, string(reqBytes))

	var resp map[string]interface{}
	decoder := json.NewDecoder(stdout)
	if err := decoder.Decode(&resp); err != nil {
		t.Fatalf("decode response: %v\nstderr: %s", err, stderr.String())
	}

	if errVal, ok := resp["error"]; ok {
		t.Fatalf("initialize error: %v\nstderr: %s", errVal, stderr.String())
	}

	result, ok := resp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", resp["result"])
	}

	serverInfo, ok := result["serverInfo"].(map[string]interface{})
	if !ok {
		t.Fatal("expected serverInfo in result")
	}

	if serverInfo["name"] != "leanproxy-mcp-github" {
		t.Errorf("server name = %v, want leanproxy-mcp-github", serverInfo["name"])
	}

	capabilities, ok := result["capabilities"].(map[string]interface{})
	if !ok {
		t.Fatal("expected capabilities in result")
	}
	if _, ok := capabilities["tools"]; !ok {
		t.Error("expected tools capability")
	}
}

func TestGitHubServer_ToolsList(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set — skipping integration test")
	}

	serverPath := buildGitHubServer(t)
	if serverPath == "" {
		t.Fatal("failed to build github server binary")
	}

	cmd := exec.CommandContext(context.Background(), serverPath)
	cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer cmd.Process.Kill()

	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "integration-test",
				"version": "1.0",
			},
		},
		"id": 1,
	}
	reqBytes, _ := json.Marshal(initReq)
	fmt.Fprintln(stdin, string(reqBytes))

	decoder := json.NewDecoder(stdout)
	var initResp map[string]interface{}
	if err := decoder.Decode(&initResp); err != nil {
		t.Fatalf("decode init: %v\nstderr: %s", err, stderr.String())
	}

	toolsReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/list",
		"id":      2,
	}
	reqBytes, _ = json.Marshal(toolsReq)
	fmt.Fprintln(stdin, string(reqBytes))

	var toolsResp map[string]interface{}
	if err := decoder.Decode(&toolsResp); err != nil {
		t.Fatalf("decode tools/list: %v\nstderr: %s", err, stderr.String())
	}

	if errVal, ok := toolsResp["error"]; ok {
		t.Fatalf("tools/list error: %v\nstderr: %s", errVal, stderr.String())
	}

	result, ok := toolsResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", toolsResp["result"])
	}

	toolsList, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools array in result")
	}

	if len(toolsList) < 2 {
		t.Fatalf("expected at least 2 tools (list_repos, get_issue), got %d", len(toolsList))
	}

	toolNames := make(map[string]bool)
	for _, tRaw := range toolsList {
		tool, ok := tRaw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := tool["name"].(string)
		toolNames[name] = true
	}

	if !toolNames["list_repos"] {
		t.Error("expected list_repos tool")
	}
	if !toolNames["get_issue"] {
		t.Error("expected get_issue tool")
	}
	if !toolNames["create_pr"] {
		t.Error("expected create_pr tool (authenticated mode)")
	}
}

func TestGitHubServer_Ping(t *testing.T) {
	if os.Getenv("GITHUB_TOKEN") == "" {
		t.Skip("GITHUB_TOKEN not set — skipping integration test")
	}

	serverPath := buildGitHubServer(t)
	if serverPath == "" {
		t.Fatal("failed to build github server binary")
	}

	cmd := exec.CommandContext(context.Background(), serverPath)
	cmd.Env = append(os.Environ(), "GITHUB_TOKEN="+os.Getenv("GITHUB_TOKEN"))

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer cmd.Process.Kill()

	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "integration-test",
				"version": "1.0",
			},
		},
		"id": 1,
	}
	reqBytes, _ := json.Marshal(initReq)
	fmt.Fprintln(stdin, string(reqBytes))

	decoder := json.NewDecoder(stdout)
	var initResp map[string]interface{}
	if err := decoder.Decode(&initResp); err != nil {
		t.Fatalf("decode init: %v\nstderr: %s", err, stderr.String())
	}

	pingReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "ping",
		"id":      2,
	}
	reqBytes, _ = json.Marshal(pingReq)
	fmt.Fprintln(stdin, string(reqBytes))

	var pingResp map[string]interface{}
	if err := decoder.Decode(&pingResp); err != nil {
		t.Fatalf("decode ping: %v\nstderr: %s", err, stderr.String())
	}

	if errVal, ok := pingResp["error"]; ok {
		t.Fatalf("ping error: %v\nstderr: %s", errVal, stderr.String())
	}

	result, ok := pingResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", pingResp["result"])
	}

	if result["status"] != "ok" {
		t.Errorf("ping status = %v, want ok", result["status"])
	}
}

func TestGitHubServer_ReadOnlyNotice(t *testing.T) {
	serverPath := buildGitHubServer(t)
	if serverPath == "" {
		t.Fatal("failed to build github server binary")
	}

	cmd := exec.CommandContext(context.Background(), serverPath)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer cmd.Process.Kill()

	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "integration-test",
				"version": "1.0",
			},
		},
		"id": 1,
	}
	reqBytes, _ := json.Marshal(request)
	fmt.Fprintln(stdin, string(reqBytes))

	var resp map[string]interface{}
	decoder := json.NewDecoder(stdout)
	if err := decoder.Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	stderrOutput := stderr.String()
	if !strings.Contains(stderrOutput, "GITHUB_TOKEN not set") {
		t.Errorf("expected read-only notice in stderr, got: %s", stderrOutput)
	}

	toolsReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/list",
		"id":      2,
	}
	reqBytes, _ = json.Marshal(toolsReq)
	fmt.Fprintln(stdin, string(reqBytes))

	var toolsResp map[string]interface{}
	if err := decoder.Decode(&toolsResp); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	result, ok := toolsResp["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected result object, got %T", toolsResp["result"])
	}

	toolsList, ok := result["tools"].([]interface{})
	if !ok {
		t.Fatal("expected tools array")
	}

	for _, tRaw := range toolsList {
		tool, ok := tRaw.(map[string]interface{})
		if !ok {
			continue
		}
		if tool["name"] == "create_pr" {
			t.Error("create_pr should not be available in read-only mode")
		}
	}
}

func TestGitHubServer_ReadOnlyCreatePR(t *testing.T) {
	serverPath := buildGitHubServer(t)
	if serverPath == "" {
		t.Fatal("failed to build github server binary")
	}

	cmd := exec.CommandContext(context.Background(), serverPath)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start server: %v", err)
	}
	defer cmd.Process.Kill()

	initReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo": map[string]interface{}{
				"name":    "integration-test",
				"version": "1.0",
			},
		},
		"id": 1,
	}
	reqBytes, _ := json.Marshal(initReq)
	fmt.Fprintln(stdin, string(reqBytes))

	decoder := json.NewDecoder(stdout)
	var initResp map[string]interface{}
	if err := decoder.Decode(&initResp); err != nil {
		t.Fatalf("decode init: %v", err)
	}

	prReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "create_pr",
			"arguments": map[string]interface{}{
				"owner": "test",
				"repo":  "test",
				"title": "Test",
				"head":  "branch",
				"base":  "main",
			},
		},
		"id": 2,
	}
	reqBytes, _ = json.Marshal(prReq)
	fmt.Fprintln(stdin, string(reqBytes))

	var prResp map[string]interface{}
	if err := decoder.Decode(&prResp); err != nil {
		t.Fatalf("decode create_pr: %v", err)
	}

	if _, ok := prResp["error"]; !ok {
		t.Error("expected error when calling create_pr in read-only mode")
	}
}

func buildGitHubServer(t *testing.T) string {
	t.Helper()

	repoRoot := os.Getenv("LEANPROXY_REPO_ROOT")
	if repoRoot == "" {
		repoRoot = findRepoRoot(t)
	}
	if repoRoot == "" {
		t.Skip("could not determine repo root — skipping integration test")
	}

	binaryPath := filepath.Join(repoRoot, "dist", "github-mcp-server-test")
	mainPath := filepath.Join(repoRoot, "servers", "github", "main.go")

	cmd := exec.Command("go", "build", "-o", binaryPath, mainPath)
	cmd.Dir = repoRoot
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build github server: %v\noutput: %s", err, string(output))
	}

	return binaryPath
}

func findRepoRoot(t *testing.T) string {
	t.Helper()

	if dir, err := os.Getwd(); err == nil {
		for dir != "/" {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir
			}
			dir = filepath.Dir(dir)
		}
	}

	return ""
}
