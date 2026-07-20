package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/filesystemtools"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

func TestGetAllowedRoots_Empty(t *testing.T) {
	os.Unsetenv("LEANPROXY_FILESYSTEM_ROOTS")
	roots := getAllowedRoots()
	if roots != nil {
		t.Errorf("expected nil, got %v", roots)
	}
}

func TestGetAllowedRoots_Single(t *testing.T) {
	t.Setenv("LEANPROXY_FILESYSTEM_ROOTS", "/workspace/project")
	roots := getAllowedRoots()
	if len(roots) != 1 {
		t.Fatalf("expected 1 root, got %d", len(roots))
	}
	if roots[0] != "/workspace/project" {
		t.Errorf("root = %q, want %q", roots[0], "/workspace/project")
	}
}

func TestGetAllowedRoots_Multiple(t *testing.T) {
	t.Setenv("LEANPROXY_FILESYSTEM_ROOTS", "/workspace/project,/workspace/data")
	roots := getAllowedRoots()
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(roots))
	}
	if roots[0] != "/workspace/project" {
		t.Errorf("root[0] = %q, want %q", roots[0], "/workspace/project")
	}
	if roots[1] != "/workspace/data" {
		t.Errorf("root[1] = %q, want %q", roots[1], "/workspace/data")
	}
}

func TestGetAllowedRoots_WithSpaces(t *testing.T) {
	t.Setenv("LEANPROXY_FILESYSTEM_ROOTS", " /workspace/project , /workspace/data ")
	roots := getAllowedRoots()
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(roots))
	}
	if roots[0] != "/workspace/project" {
		t.Errorf("root[0] = %q, want %q", roots[0], "/workspace/project")
	}
}

func TestGetAllowedRoots_EmptyParts(t *testing.T) {
	t.Setenv("LEANPROXY_FILESYSTEM_ROOTS", "/workspace/project,,/workspace/data")
	roots := getAllowedRoots()
	if len(roots) != 2 {
		t.Fatalf("expected 2 roots, got %d", len(roots))
	}
}

func TestHandleInitialize(t *testing.T) {
	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "initialize",
		ID:      float64(1),
	}
	initialized := false
	resp := handleInitialize(req, &initialized)

	if !initialized {
		t.Error("expected initialized to be true")
	}
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result mcp.InitializeResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result.ServerInfo.Name != serverName {
		t.Errorf("server name = %q, want %q", result.ServerInfo.Name, serverName)
	}
	if result.ServerInfo.Version != serverVersion {
		t.Errorf("server version = %q, want %q", result.ServerInfo.Version, serverVersion)
	}
}

func TestHandleToolsList(t *testing.T) {
	dir, err := os.MkdirTemp("", "filesystem-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := filesystemtools.NewFilesystemClient(slog.Default(), []string{dir})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "tools/list",
		ID:      float64(1),
	}
	resp := handleToolsList(client, req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result mcp.ToolsListResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Tools) == 0 {
		t.Fatal("expected at least one tool")
	}

	names := make(map[string]bool)
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}

	expected := []string{"read_file", "write_file", "list_directory", "file_info", "search_files", "read_multiple_files"}
	for _, name := range expected {
		if !names[name] {
			t.Errorf("expected tool %q not found", name)
		}
	}
}

func TestHandlePing(t *testing.T) {
	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "ping",
		ID:      float64(1),
	}
	resp := handlePing(req)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	var result map[string]string
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}
}

func TestHandleRequest_UnknownMethod(t *testing.T) {
	dir, err := os.MkdirTemp("", "filesystem-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := filesystemtools.NewFilesystemClient(slog.Default(), []string{dir})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "unknown_method",
		ID:      float64(1),
	}
	initialized := true
	resp := handleRequest(nil, slog.Default(), client, req, &initialized)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != mcp.ErrCodeMethodNotFound {
		t.Errorf("error code = %d, want %d", resp.Error.Code, mcp.ErrCodeMethodNotFound)
	}
}

func TestHandleToolsCall_NoName(t *testing.T) {
	dir, err := os.MkdirTemp("", "filesystem-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := filesystemtools.NewFilesystemClient(slog.Default(), []string{dir})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	params, _ := json.Marshal(mcp.ToolsCallParams{Name: ""})
	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "tools/call",
		Params:  params,
		ID:      float64(1),
	}
	initialized := true
	resp := handleRequest(nil, slog.Default(), client, req, &initialized)
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for empty tool name")
	}
}

func TestHandleRequest_Initialized(t *testing.T) {
	dir, err := os.MkdirTemp("", "filesystem-server-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	client, err := filesystemtools.NewFilesystemClient(slog.Default(), []string{dir})
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	initialized := false
	resp := handleRequest(nil, slog.Default(), client, req, &initialized)
	if resp != nil {
		t.Fatal("expected nil response for notification")
	}
	if !initialized {
		t.Error("expected initialized to be true")
	}
}

func TestTrimNewline(t *testing.T) {
	tests := []struct {
		input []byte
		want  []byte
	}{
		{[]byte("hello\n"), []byte("hello")},
		{[]byte("hello\r\n"), []byte("hello")},
		{[]byte("hello"), []byte("hello")},
		{[]byte(""), []byte("")},
		{[]byte("\n"), []byte("")},
	}
	for _, tt := range tests {
		got := trimNewline(tt.input)
		if string(got) != string(tt.want) {
			t.Errorf("trimNewline(%q) = %q, want %q", string(tt.input), string(got), string(tt.want))
		}
	}
}
