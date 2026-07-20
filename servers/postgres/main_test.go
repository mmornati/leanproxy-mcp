package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/postgresql"
)

func TestGetConfig_Defaults(t *testing.T) {
	os.Unsetenv("LEANPROXY_POSTGRES_CONNECTION")
	os.Unsetenv("LEANPROXY_POSTGRES_POOL_SIZE")
	os.Unsetenv("LEANPROXY_POSTGRES_STATEMENT_TIMEOUT")

	cfg := getConfig()
	if cfg.ConnectionString != "" {
		t.Errorf("expected empty connection string, got %q", cfg.ConnectionString)
	}
	if cfg.PoolSize != postgresql.DefaultPoolSize {
		t.Errorf("expected pool size %d, got %d", postgresql.DefaultPoolSize, cfg.PoolSize)
	}
}

func TestGetConfig_FromEnv(t *testing.T) {
	t.Setenv("LEANPROXY_POSTGRES_CONNECTION", "postgres://user:pass@localhost:5432/testdb")
	t.Setenv("LEANPROXY_POSTGRES_POOL_SIZE", "20")
	t.Setenv("LEANPROXY_POSTGRES_STATEMENT_TIMEOUT", "15s")

	cfg := getConfig()
	if cfg.ConnectionString != "postgres://user:pass@localhost:5432/testdb" {
		t.Errorf("connection string mismatch")
	}
	if cfg.PoolSize != 20 {
		t.Errorf("pool size = %d, want 20", cfg.PoolSize)
	}
	if cfg.StatementTimeout.String() != "15s" {
		t.Errorf("statement timeout = %v, want 15s", cfg.StatementTimeout)
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
	logger := slog.Default()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "unknown_method",
		ID:      float64(1),
	}
	initialized := true
	resp := handleRequest(nil, logger, nil, req, &initialized)
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

func TestHandleRequest_Initialized(t *testing.T) {
	logger := slog.Default()

	req := &mcp.Request{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}
	initialized := false
	resp := handleRequest(nil, logger, nil, req, &initialized)
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
