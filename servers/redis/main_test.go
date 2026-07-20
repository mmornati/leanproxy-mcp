package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/redistools"
)

func TestGetConfig_Defaults(t *testing.T) {
	os.Unsetenv("LEANPROXY_REDIS_ADDRESS")
	os.Unsetenv("LEANPROXY_REDIS_PASSWORD")
	os.Unsetenv("LEANPROXY_REDIS_POOL_SIZE")
	os.Unsetenv("LEANPROXY_REDIS_TLS")

	cfg := getConfig()
	if cfg.Address != "127.0.0.1:6379" {
		t.Errorf("expected default address 127.0.0.1:6379, got %q", cfg.Address)
	}
	if cfg.PoolSize != redistools.DefaultPoolSize {
		t.Errorf("expected pool size %d, got %d", redistools.DefaultPoolSize, cfg.PoolSize)
	}
	if cfg.Password != "" {
		t.Errorf("expected empty password, got %q", cfg.Password)
	}
	if cfg.UseTLS {
		t.Error("expected TLS disabled")
	}
}

func TestGetConfig_FromEnv(t *testing.T) {
	t.Setenv("LEANPROXY_REDIS_ADDRESS", "10.0.0.1:6380")
	t.Setenv("LEANPROXY_REDIS_PASSWORD", "secret")
	t.Setenv("LEANPROXY_REDIS_POOL_SIZE", "20")
	t.Setenv("LEANPROXY_REDIS_TLS", "true")

	cfg := getConfig()
	if cfg.Address != "10.0.0.1:6380" {
		t.Errorf("address = %q, want 10.0.0.1:6380", cfg.Address)
	}
	if cfg.Password != "secret" {
		t.Errorf("password = %q, want secret", cfg.Password)
	}
	if cfg.PoolSize != 20 {
		t.Errorf("pool size = %d, want 20", cfg.PoolSize)
	}
	if !cfg.UseTLS {
		t.Error("expected TLS enabled")
	}
}

func TestParseInt(t *testing.T) {
	tests := []struct {
		input string
		want  int
		err   bool
	}{
		{"10", 10, false},
		{"0", 0, false},
		{"abc", 0, true},
		{"", 0, true},
		{"-1", 0, true},
	}
	for _, tt := range tests {
		got, err := parseInt(tt.input)
		if tt.err && err == nil {
			t.Errorf("parseInt(%q) expected error", tt.input)
		}
		if !tt.err && err != nil {
			t.Errorf("parseInt(%q) unexpected error: %v", tt.input, err)
		}
		if got != tt.want {
			t.Errorf("parseInt(%q) = %d, want %d", tt.input, got, tt.want)
		}
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