package migrate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if server.Transport != TransportStdio {
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
      auth:
        type: oauth2
        client_id: my-client-id
        client_secret: my-secret
        scopes:
          - mcp:read
          - mcp:write
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
	if server.Transport != TransportHTTP {
		t.Errorf("Transport = %v, want http", server.Transport)
	}
	if server.HTTP.URL != "http://localhost:8080" {
		t.Errorf("HTTP.URL = %v, want http://localhost:8080", server.HTTP.URL)
	}
	if server.HTTP.Headers["Authorization"] != "Bearer token123" {
		t.Errorf("Authorization header = %v, want Bearer token123", server.HTTP.Headers["Authorization"])
	}
	if server.HTTP.Auth == nil {
		t.Fatal("HTTP.Auth should not be nil")
	}
	if server.HTTP.Auth.Type != "oauth2" {
		t.Errorf("Auth.Type = %v, want oauth2", server.HTTP.Auth.Type)
	}
	if server.HTTP.Auth.ClientID != "my-client-id" {
		t.Errorf("Auth.ClientID = %v, want my-client-id", server.HTTP.Auth.ClientID)
	}
	if server.HTTP.Auth.ClientSecret != "my-secret" {
		t.Errorf("Auth.ClientSecret = %v, want my-secret", server.HTTP.Auth.ClientSecret)
	}
	if len(server.HTTP.Auth.Scopes) != 2 || server.HTTP.Auth.Scopes[0] != "mcp:read" {
		t.Errorf("Auth.Scopes = %v, want [mcp:read mcp:write]", server.HTTP.Auth.Scopes)
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
				Transport: TransportStdio,
				Stdio:     &StdioConfig{Command: "/bin/server"},
			},
			wantErr: false,
		},
		{
			name: "valid http server",
			server: &ServerConfig{
				Name:      "test-http",
				Transport: TransportHTTP,
				HTTP:      &HTTPConfig{URL: "http://localhost:8080"},
			},
			wantErr: false,
		},
		{
			name: "valid sse server",
			server: &ServerConfig{
				Name:      "test-sse",
				Transport: TransportSSE,
				HTTP:      &HTTPConfig{URL: "http://localhost:8080/sse"},
			},
			wantErr: false,
		},
		{
			name:    "missing name",
			server:  &ServerConfig{Transport: TransportStdio, Stdio: &StdioConfig{Command: "/bin/server"}},
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
				Transport: TransportStdio,
				Stdio:     &StdioConfig{},
			},
			wantErr: true,
			errMsg:  "command is required for stdio transport",
		},
		{
			name: "http missing url",
			server: &ServerConfig{
				Name:      "test",
				Transport: TransportHTTP,
				HTTP:      &HTTPConfig{},
			},
			wantErr: true,
			errMsg:  "url is required for http transport",
		},
		{
			name: "sse missing url",
			server: &ServerConfig{
				Name:      "test",
				Transport: TransportSSE,
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
			{Name: "valid", Transport: TransportStdio, Stdio: &StdioConfig{Command: "/bin/server"}},
			{Name: "invalid-stdio", Transport: TransportStdio, Stdio: &StdioConfig{}},
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
	if cfg.Servers[0].Transport != TransportSSE {
		t.Errorf("Transport = %v, want sse", cfg.Servers[0].Transport)
	}
}

func TestLoadConfigBearerAuth(t *testing.T) {
	yamlContent := `
servers:
  - name: bearer-server
    transport: http
    http:
      url: http://localhost:8080
      auth:
        type: bearer
        client_secret: my-api-key
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
	if server.HTTP.Auth == nil {
		t.Fatal("HTTP.Auth should not be nil")
	}
	if server.HTTP.Auth.Type != "bearer" {
		t.Errorf("Auth.Type = %v, want bearer", server.HTTP.Auth.Type)
	}
	if server.HTTP.Auth.ClientSecret != "my-api-key" {
		t.Errorf("Auth.ClientSecret = %v, want my-api-key", server.HTTP.Auth.ClientSecret)
	}
}

func TestLoadConfigPathTraversal(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		wantErr string
	}{
		{
			name:    "path with .. sequences",
			path:    "../../../etc/passwd",
			wantErr: "path traversal",
		},
		{
			name:    "URL encoded traversal",
			path:    "..%2F..%2F..%2Fetc%2Fpasswd",
			wantErr: "path traversal",
		},
		{
			name:    "null byte injection",
			path:    "/tmp/config.yaml\x00",
			wantErr: "null byte",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			_, err := LoadConfig(ctx, tt.path)
			if err == nil {
				t.Errorf("LoadConfig() expected error containing %q, got nil", tt.wantErr)
			} else if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("LoadConfig() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}
