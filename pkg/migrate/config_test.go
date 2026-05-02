package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func TestLoadConfigMinimal(t *testing.T) {
	yamlContent := `
servers:
  - name: test-server
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	ctx := context.Background()
	cfg, err := LoadConfig(ctx, configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if cfg == nil {
		t.Fatal("LoadConfig() returned nil config")
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(cfg.Servers))
	}
	server := cfg.Servers[0]
	if server.Name != "test-server" {
		t.Errorf("Name = %v, want test-server", server.Name)
	}
	if server.Transport != registry.TransportStdio {
		t.Errorf("Transport = %v, want stdio", server.Transport)
	}
	if server.Enabled == nil || !*server.Enabled {
		t.Error("Enabled should default to true")
	}
	if server.TimeoutValue != 30*1e9 {
		t.Errorf("TimeoutValue = %v, want 30s", server.TimeoutValue)
	}
}

func TestLoadConfigFull(t *testing.T) {
	yamlContent := `
version: "1.0"
servers:
  - name: full-server
    enabled: false
    transport: http
    http:
      url: http://localhost:8080
      headers:
        Authorization: Bearer token123
    timeout: 60s
    connect_timeout: 5s
    cache_settings:
      enabled: true
      max_size: 100
      ttl: 5m
    summarize_settings:
      enabled: true
      max_tokens: 1000
      strategy: truncate
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	ctx := context.Background()
	cfg, err := LoadConfig(ctx, configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(cfg.Servers))
	}
	server := cfg.Servers[0]
	if server.Name != "full-server" {
		t.Errorf("Name = %v, want full-server", server.Name)
	}
	if server.Enabled != nil && *server.Enabled {
		t.Error("Enabled should be false")
	}
	if server.Transport != registry.TransportHTTP {
		t.Errorf("Transport = %v, want http", server.Transport)
	}
	if server.HTTP.URL != "http://localhost:8080" {
		t.Errorf("HTTP.URL = %v, want http://localhost:8080", server.HTTP.URL)
	}
	if server.HTTP.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("Authorization header = %v, want Bearer token123", server.HTTP.Headers["Authorization"])
	}
	if server.TimeoutValue != 60*1e9 {
		t.Errorf("TimeoutValue = %v, want 60s", server.TimeoutValue)
	}
	if server.ConnectTimeoutValue != 5*1e9 {
		t.Errorf("ConnectTimeoutValue = %v, want 5s", server.ConnectTimeoutValue)
	}
	if !server.CacheSettings.Enabled {
		t.Error("CacheSettings.Enabled should be true")
	}
	if server.CacheSettings.MaxSize != 100 {
		t.Errorf("CacheSettings.MaxSize = %v, want 100", server.CacheSettings.MaxSize)
	}
	if server.CacheSettings.TTLValue != 5*60*1e9 {
		t.Errorf("CacheSettings.TTLValue = %v, want 5m", server.CacheSettings.TTLValue)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	ctx := context.Background()
	cfg, err := LoadConfig(ctx, "/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("LoadConfig() should not fail for missing file, got error: %v", err)
	}
	if cfg != nil {
		t.Error("LoadConfig() should return nil for missing file")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	yamlContent := `
servers:
  - name: invalid
    transport: stdio
    stdio:
      command: /bin/server
      args:
        - key: value
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	ctx := context.Background()
	_, err := LoadConfig(ctx, configPath)
	if err == nil {
		t.Error("LoadConfig() should fail for invalid YAML structure")
	}
}

func TestLoadConfigDefaultValues(t *testing.T) {
	yamlContent := `
servers:
  - name: defaults-test
    transport: stdio
    stdio:
      command: /bin/server
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	ctx := context.Background()
	cfg, err := LoadConfig(ctx, configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	server := cfg.Servers[0]
	if server.Enabled == nil || !*server.Enabled {
		t.Errorf("Enabled default = %v, want true", server.Enabled)
	}
	if server.TimeoutValue != 30*1e9 {
		t.Errorf("TimeoutValue default = %v, want 30s", server.TimeoutValue)
	}
	if server.ConnectTimeoutValue != 10*1e9 {
		t.Errorf("ConnectTimeoutValue default = %v, want 10s", server.ConnectTimeoutValue)
	}
}

func TestServerConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		server  *ServerConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid stdio server",
			server: &ServerConfig{
				Name:      "test",
				Transport: registry.TransportStdio,
				Stdio:     &StdioConfig{Command: "/bin/server"},
			},
			wantErr: false,
		},
		{
			name: "valid http server",
			server: &ServerConfig{
				Name:      "test-http",
				Transport: registry.TransportHTTP,
				HTTP:      &HTTPConfig{URL: "http://localhost:8080"},
			},
			wantErr: false,
		},
		{
			name: "valid sse server",
			server: &ServerConfig{
				Name:      "test-sse",
				Transport: registry.TransportSSE,
				HTTP:      &HTTPConfig{URL: "http://localhost:8080/sse"},
			},
			wantErr: false,
		},
		{
			name:    "missing name",
			server:  &ServerConfig{Transport: registry.TransportStdio, Stdio: &StdioConfig{Command: "/bin/server"}},
			wantErr: true,
			errMsg:  "server name is required",
		},
		{
			name:    "missing transport",
			server:  &ServerConfig{Name: "test", Stdio: &StdioConfig{Command: "/bin/server"}},
			wantErr: true,
			errMsg:  "transport type is required",
		},
		{
			name: "stdio missing command",
			server: &ServerConfig{
				Name:      "test",
				Transport: registry.TransportStdio,
				Stdio:     &StdioConfig{},
			},
			wantErr: true,
			errMsg:  "command is required for stdio transport",
		},
		{
			name: "http missing url",
			server: &ServerConfig{
				Name:      "test",
				Transport: registry.TransportHTTP,
				HTTP:      &HTTPConfig{},
			},
			wantErr: true,
			errMsg:  "url is required for http transport",
		},
		{
			name: "sse missing url",
			server: &ServerConfig{
				Name:      "test",
				Transport: registry.TransportSSE,
				HTTP:      &HTTPConfig{},
			},
			wantErr: true,
			errMsg:  "url is required for sse transport",
		},
		{
			name:    "invalid transport type",
			server:  &ServerConfig{Name: "test", Transport: "websocket"},
			wantErr: true,
			errMsg:  "invalid transport type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.server.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error containing %q, got nil", tt.errMsg)
				} else if !contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() error = %v, want error containing %q", err, tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestConfigValidate(t *testing.T) {
	cfg := &Config{
		Servers: []*ServerConfig{
			{Name: "valid", Transport: registry.TransportStdio, Stdio: &StdioConfig{Command: "/bin/server"}},
			{Name: "invalid-stdio", Transport: registry.TransportStdio, Stdio: &StdioConfig{}},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Config.Validate() should fail when a server is invalid")
	}
}

func TestLoadConfigSSETypes(t *testing.T) {
	yamlContent := `
servers:
  - name: sse-server
    transport: sse
    http:
      url: http://localhost:8080/events
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	ctx := context.Background()
	cfg, err := LoadConfig(ctx, configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if cfg.Servers[0].Transport != registry.TransportSSE {
		t.Errorf("Transport = %v, want sse", cfg.Servers[0].Transport)
	}
}