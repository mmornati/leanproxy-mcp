package migrate

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
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

func TestLoadConfigRateLimit(t *testing.T) {
	yamlContent := `
servers:
  - name: github
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
    rate_limit:
      requests_per_second: 20
      burst: 40
  - name: unlimited-server
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
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() failed: %v", err)
	}

	limited := cfg.Servers[0]
	if limited.RateLimit == nil {
		t.Fatal("expected rate_limit to be parsed")
	}
	if limited.RateLimit.RequestsPerSecond != 20 {
		t.Errorf("RequestsPerSecond = %v, want 20", limited.RateLimit.RequestsPerSecond)
	}
	if limited.RateLimit.Burst != 40 {
		t.Errorf("Burst = %v, want 40", limited.RateLimit.Burst)
	}

	unlimited := cfg.Servers[1]
	if unlimited.RateLimit != nil {
		t.Errorf("expected no rate_limit block for unlimited-server, got %+v", unlimited.RateLimit)
	}
}

func TestLoadConfigRateLimitNegativeRejected(t *testing.T) {
	yamlContent := `
servers:
  - name: bad
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
    rate_limit:
      requests_per_second: -5
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	ctx := context.Background()
	_, err := LoadConfig(ctx, configPath)
	if err == nil {
		t.Fatal("expected LoadConfig() to reject a negative requests_per_second")
	}
	if !contains(err.Error(), "requests_per_second must be >= 0") {
		t.Errorf("LoadConfig() error = %v, want it to mention requests_per_second", err)
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
	if err == nil {
		t.Fatal("LoadConfig() should fail for missing file")
	}
	if !errors.Is(err, ErrConfigNotFound) {
		t.Errorf("LoadConfig() missing-file error should wrap ErrConfigNotFound, got %v", err)
	}
	if cfg != nil {
		t.Error("LoadConfig() should return nil config for missing file")
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
		{
			name: "nil rate_limit is valid (unlimited)",
			server: &ServerConfig{
				Name:      "test",
				Transport: TransportStdio,
				Stdio:     &StdioConfig{Command: "/bin/server"},
			},
			wantErr: false,
		},
		{
			name: "valid rate_limit",
			server: &ServerConfig{
				Name:      "test",
				Transport: TransportStdio,
				Stdio:     &StdioConfig{Command: "/bin/server"},
				RateLimit: &RateLimitConfig{RequestsPerSecond: 20, Burst: 40},
			},
			wantErr: false,
		},
		{
			name: "negative requests_per_second",
			server: &ServerConfig{
				Name:      "test",
				Transport: TransportStdio,
				Stdio:     &StdioConfig{Command: "/bin/server"},
				RateLimit: &RateLimitConfig{RequestsPerSecond: -1},
			},
			wantErr: true,
			errMsg:  "requests_per_second must be >= 0",
		},
		{
			name: "negative burst",
			server: &ServerConfig{
				Name:      "test",
				Transport: TransportStdio,
				Stdio:     &StdioConfig{Command: "/bin/server"},
				RateLimit: &RateLimitConfig{RequestsPerSecond: 1, Burst: -1},
			},
			wantErr: true,
			errMsg:  "burst must be >= 0",
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

func TestLoadConfigReconnectBlock(t *testing.T) {
	yamlContent := `
servers:
  - name: test-server
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
reconnect:
  enabled: false
  health_check_interval: 15s
  health_check_failures: 2
  max_restart_attempts: 8
  restart_backoff: 500ms
  stable_window: 5m
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
	if cfg.Reconnect == nil {
		t.Fatal("Reconnect block should be parsed")
	}

	rc := cfg.Reconnect
	if rc.Enabled == nil || *rc.Enabled {
		t.Errorf("Enabled = %v, want false", rc.Enabled)
	}
	if rc.HealthIntervalValue != 15*time.Second {
		t.Errorf("HealthIntervalValue = %v, want 15s", rc.HealthIntervalValue)
	}
	if rc.MaxFailures != 2 {
		t.Errorf("MaxFailures = %d, want 2", rc.MaxFailures)
	}
	if rc.MaxRestartAttempts != 8 {
		t.Errorf("MaxRestartAttempts = %d, want 8", rc.MaxRestartAttempts)
	}
	if rc.RestartBackoffValue != 500*time.Millisecond {
		t.Errorf("RestartBackoffValue = %v, want 500ms", rc.RestartBackoffValue)
	}
	if rc.StableWindowValue != 5*time.Minute {
		t.Errorf("StableWindowValue = %v, want 5m", rc.StableWindowValue)
	}
}

func TestEffectiveReconnectDefaults(t *testing.T) {
	cfg := &Config{}
	got := cfg.EffectiveReconnect()

	want := ResolvedReconnect{
		Enabled:            true,
		HealthInterval:     30 * time.Second,
		MaxFailures:        3,
		MaxRestartAttempts: 5,
		RestartBackoff:     time.Second,
		StableWindow:       2 * time.Minute,
	}
	if got != want {
		t.Errorf("EffectiveReconnect() = %+v, want %+v", got, want)
	}

	var nilCfg *Config
	gotNil := nilCfg.EffectiveReconnect()
	if gotNil != want {
		t.Errorf("EffectiveReconnect() on nil config = %+v, want defaults %+v", gotNil, want)
	}
}

func TestEffectiveReconnectOverrides(t *testing.T) {
	disabled := false
	cfg := &Config{
		Reconnect: &ReconnectConfig{
			Enabled:             &disabled,
			HealthInterval:      "10s",
			HealthIntervalValue: 10 * time.Second,
			MaxFailures:         1,
			MaxRestartAttempts:  3,
			RestartBackoffValue: 2 * time.Second,
			StableWindowValue:   1 * time.Minute,
		},
	}
	got := cfg.EffectiveReconnect()

	want := ResolvedReconnect{
		Enabled:            false,
		HealthInterval:     10 * time.Second,
		MaxFailures:        1,
		MaxRestartAttempts: 3,
		RestartBackoff:     2 * time.Second,
		StableWindow:       1 * time.Minute,
	}
	if got != want {
		t.Errorf("EffectiveReconnect() = %+v, want %+v", got, want)
	}
}

func TestEffectiveReconnectHealthIntervalZeroDisables(t *testing.T) {
	// An explicit health_check_interval: "0" must be honored as "disabled"
	// (documented behavior), not silently rewritten to the 30s default.
	cfg := &Config{
		Reconnect: &ReconnectConfig{
			HealthInterval:      "0",
			HealthIntervalValue: 0,
		},
	}
	got := cfg.EffectiveReconnect()
	if got.HealthInterval != 0 {
		t.Errorf("HealthInterval = %v, want 0 (disabled)", got.HealthInterval)
	}
}

func TestConfigValidateBouncerSafePattern(t *testing.T) {
	cfg := &Config{
		Bouncer: &bouncer.Config{
			Patterns: []bouncer.PatternDef{
				{Name: "ok", Pattern: `sk-[A-Za-z0-9]{20,}`},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil for safe custom pattern", err)
	}
}

func TestConfigValidateBouncerRejectsDangerousPattern(t *testing.T) {
	// ReDoS-prone pattern: nested quantifier (.+)+. The bouncer SafeCompile
	// layer would skip this silently and ship built-ins only; Validate() must
	// surface the failure so the redactor is never quietly downgraded.
	cfg := &Config{
		Bouncer: &bouncer.Config{
			Enabled: ptr(true),
			Patterns: []bouncer.PatternDef{
				{Name: "redos", Pattern: `(.+)+secret`},
			},
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() should reject dangerous nested-quantifier pattern")
	}
	if !strings.Contains(err.Error(), "redos") {
		t.Errorf("Validate() error = %v, want it to mention the offending pattern name", err)
	}
}

func TestConfigValidateBouncerWalksBothLists(t *testing.T) {
	// CustomPatterns is the legacy alias for Patterns; both must be checked.
	cfg := &Config{
		Bouncer: &bouncer.Config{
			Patterns: []bouncer.PatternDef{
				{Name: "ok1", Pattern: `token-[A-Za-z0-9]+`},
			},
			CustomPatterns: []bouncer.PatternDef{
				{Name: "bad", Pattern: `(a+)+`},
			},
		},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate() should reject dangerous CustomPatterns entry")
	}
}

func TestConfigValidateBouncerNilIsOK(t *testing.T) {
	cfg := &Config{Servers: nil, Bouncer: nil}
	if err := cfg.Validate(); err != nil {
		t.Errorf("Validate() = %v, want nil when Bouncer is absent", err)
	}
}

func TestLoadConfigBouncerDangerousPatternFails(t *testing.T) {
	yamlContent := `
bouncer:
  enabled: true
  patterns:
    - name: redos
      pattern: "(.+)+secret"
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	_, err := LoadConfig(context.Background(), configPath)
	if err == nil {
		t.Fatal("LoadConfig() should reject dangerous bouncer pattern at load time")
	}
	if !strings.Contains(err.Error(), "redos") {
		t.Errorf("LoadConfig() error = %v, want it to mention the offending pattern name", err)
	}
}

// captureDefaultLogs redirects slog.Default() to a buffer for the duration
// of the test and restores the previous default logger on cleanup.
func captureDefaultLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

// Removed-key configs (issue #303): a leanproxy.yaml that still sets
// `federation:` or `optimization.lazy_loading:` must keep loading (never
// fail), with exactly one startup warning logged per deprecated key.

func TestLoadConfigFederationKeyStillLoadsWithOneWarning(t *testing.T) {
	yamlContent := `
servers:
  - name: test-server
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
federation:
  enabled: true
  peers:
    - name: peer-a
      url: https://peer-a.example.com
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	buf := captureDefaultLogs(t)

	cfg, err := LoadConfig(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig() should not fail on a config with a removed `federation:` key, got: %v", err)
	}
	if cfg == nil || len(cfg.Servers) != 1 {
		t.Fatal("LoadConfig() should still parse the rest of the config")
	}

	out := buf.String()
	want := `config key \"federation\" is no longer supported and is ignored`
	if n := strings.Count(out, want); n != 1 {
		t.Errorf("expected exactly one federation deprecation warning, got %d in log output: %s", n, out)
	}
	if strings.Contains(out, "optimization.lazy_loading") {
		t.Errorf("federation-only config should not also warn about lazy_loading: %s", out)
	}
}

func TestLoadConfigLazyLoadingKeyStillLoadsWithOneWarning(t *testing.T) {
	yamlContent := `
servers:
  - name: test-server
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
optimization:
  lazy_loading:
    enabled: true
    stub_tokens: 54
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	buf := captureDefaultLogs(t)

	cfg, err := LoadConfig(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig() should not fail on a config with a removed `optimization.lazy_loading:` key, got: %v", err)
	}
	if cfg == nil || len(cfg.Servers) != 1 {
		t.Fatal("LoadConfig() should still parse the rest of the config")
	}

	out := buf.String()
	want := `config key \"optimization.lazy_loading\" is no longer supported and is ignored`
	if n := strings.Count(out, want); n != 1 {
		t.Errorf("expected exactly one lazy_loading deprecation warning, got %d in log output: %s", n, out)
	}
	if strings.Contains(out, `"federation"`) {
		t.Errorf("lazy_loading-only config should not also warn about federation: %s", out)
	}
}

func TestLoadConfigWithoutRemovedKeysLogsNoDeprecationWarning(t *testing.T) {
	yamlContent := `
servers:
  - name: test-server
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
`
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "leanproxy.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}

	buf := captureDefaultLogs(t)

	if _, err := LoadConfig(context.Background(), configPath); err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	if out := buf.String(); strings.Contains(out, "no longer supported") {
		t.Errorf("config without removed keys should not log a deprecation warning: %s", out)
	}
}

func TestLoadConfigMaxInFlight(t *testing.T) {
	yamlContent := `
servers:
  - name: capped
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
    max_in_flight: 8
  - name: default
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
`
	configPath := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	cfg, err := LoadConfig(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if got := cfg.Servers[0].MaxInFlight; got != 8 {
		t.Errorf("MaxInFlight = %d, want 8", got)
	}
	if got := cfg.Servers[1].MaxInFlight; got != 0 {
		t.Errorf("MaxInFlight = %d, want 0 (pool default)", got)
	}

	bad := &ServerConfig{Name: "bad", Transport: TransportStdio, Stdio: &StdioConfig{Command: "x"}, MaxInFlight: -1}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "max_in_flight must be >= 0") {
		t.Errorf("expected negative max_in_flight to be rejected, got %v", err)
	}
}

func TestLoadConfigMaxResponseBytes(t *testing.T) {
	yamlContent := `
servers:
  - name: capped
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
    max_response_bytes: 1048576
  - name: default
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
`
	configPath := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	cfg, err := LoadConfig(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if got := cfg.Servers[0].MaxResponseBytes; got != 1048576 {
		t.Errorf("MaxResponseBytes = %d, want 1048576", got)
	}
	if got := cfg.Servers[1].MaxResponseBytes; got != 0 {
		t.Errorf("MaxResponseBytes = %d, want 0 (pool default)", got)
	}

	bad := &ServerConfig{Name: "bad", Transport: TransportStdio, Stdio: &StdioConfig{Command: "x"}, MaxResponseBytes: -1}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "max_response_bytes must be >= 0") {
		t.Errorf("expected negative max_response_bytes to be rejected, got %v", err)
	}
}

func TestLoadConfigResponseCache(t *testing.T) {
	yamlContent := `
servers:
  - name: github
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
response_cache:
  enabled: true
  ttl: 10m
  max_bytes: 1048576
  max_entry_bytes: 65536
  tools:
    - github.get_file_contents
  honor_annotations: true
`
	configPath := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	cfg, err := LoadConfig(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	rc := cfg.ResponseCache
	if rc == nil {
		t.Fatal("expected a response_cache block to be parsed")
	}
	if !rc.Enabled {
		t.Error("Enabled = false, want true")
	}
	if rc.TTLValue != 10*time.Minute {
		t.Errorf("TTLValue = %v, want 10m", rc.TTLValue)
	}
	if rc.MaxBytes != 1048576 {
		t.Errorf("MaxBytes = %d, want 1048576", rc.MaxBytes)
	}
	if rc.MaxEntryBytes != 65536 {
		t.Errorf("MaxEntryBytes = %d, want 65536", rc.MaxEntryBytes)
	}
	if len(rc.Tools) != 1 || rc.Tools[0] != "github.get_file_contents" {
		t.Errorf("Tools = %v, want [github.get_file_contents]", rc.Tools)
	}
	if !rc.HonorAnnotations {
		t.Error("HonorAnnotations = false, want true")
	}
}

func TestLoadConfigResponseCache_AbsentDefaultsToDisabled(t *testing.T) {
	yamlContent := `
servers:
  - name: github
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
`
	configPath := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	cfg, err := LoadConfig(context.Background(), configPath)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if cfg.ResponseCache != nil && cfg.ResponseCache.Enabled {
		t.Error("expected response_cache to be disabled when the block is absent")
	}
}

func TestLoadConfigResponseCache_InvalidTTLRejected(t *testing.T) {
	yamlContent := `
servers:
  - name: github
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
response_cache:
  enabled: true
  ttl: not-a-duration
`
	configPath := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	if _, err := LoadConfig(context.Background(), configPath); err == nil {
		t.Fatal("expected an invalid response_cache.ttl to fail LoadConfig")
	}
}

func TestLoadConfigMaxConcurrentRequests(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile() failed: %v", err)
		}
		return path
	}
	const servers = `
servers:
  - name: a
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
`
	ctx := context.Background()

	t.Run("default when absent", func(t *testing.T) {
		cfg, err := LoadConfig(ctx, write(t, servers))
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if got := cfg.EffectiveMaxConcurrentRequests(); got != DefaultMaxConcurrentRequests {
			t.Errorf("EffectiveMaxConcurrentRequests() = %d, want %d", got, DefaultMaxConcurrentRequests)
		}
	})

	t.Run("explicit value, legacy keys ignored", func(t *testing.T) {
		cfg, err := LoadConfig(ctx, write(t, servers+`
server:
  host: 127.0.0.1
  port: 8080
  max_concurrent_requests: 8
`))
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if got := cfg.EffectiveMaxConcurrentRequests(); got != 8 {
			t.Errorf("EffectiveMaxConcurrentRequests() = %d, want 8", got)
		}
	})

	t.Run("negative rejected", func(t *testing.T) {
		_, err := LoadConfig(ctx, write(t, servers+`
server:
  max_concurrent_requests: -1
`))
		if err == nil || !contains(err.Error(), "max_concurrent_requests must be >= 0") {
			t.Fatalf("LoadConfig() error = %v, want max_concurrent_requests validation error", err)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		var cfg *Config
		if got := cfg.EffectiveMaxConcurrentRequests(); got != DefaultMaxConcurrentRequests {
			t.Errorf("nil EffectiveMaxConcurrentRequests() = %d", got)
		}
		if got := cfg.EffectiveMaxLineBytes(); got != DefaultMaxLineBytes {
			t.Errorf("nil EffectiveMaxLineBytes() = %d", got)
		}
		if got := cfg.EffectiveMaxConnections(); got != DefaultMaxConnections {
			t.Errorf("nil EffectiveMaxConnections() = %d", got)
		}
	})

	t.Run("serve listener caps", func(t *testing.T) {
		cfg, err := LoadConfig(ctx, write(t, servers+`
server:
  max_line_bytes: 1024
  max_connections: 4
`))
		if err != nil {
			t.Fatalf("LoadConfig() error = %v", err)
		}
		if got := cfg.EffectiveMaxLineBytes(); got != 1024 {
			t.Errorf("EffectiveMaxLineBytes() = %d, want 1024", got)
		}
		if got := cfg.EffectiveMaxConnections(); got != 4 {
			t.Errorf("EffectiveMaxConnections() = %d, want 4", got)
		}
		for _, key := range []string{"max_line_bytes", "max_connections"} {
			_, err := LoadConfig(ctx, write(t, servers+"\nserver:\n  "+key+": -1\n"))
			if err == nil || !contains(err.Error(), key+" must be >= 0") {
				t.Fatalf("LoadConfig(%s: -1) error = %v, want validation error", key, err)
			}
		}
	})
}

func writeToolSearchConfig(t *testing.T, block string) string {
	t.Helper()
	yamlContent := `
servers:
  - name: github
    transport: stdio
    stdio:
      command: /usr/bin/mcp-server
` + block
	configPath := filepath.Join(t.TempDir(), "leanproxy_servers.yaml")
	if err := os.WriteFile(configPath, []byte(yamlContent), 0600); err != nil {
		t.Fatalf("WriteFile() failed: %v", err)
	}
	return configPath
}

func TestLoadConfigToolSearch(t *testing.T) {
	path := writeToolSearchConfig(t, `
tool_search:
  synonyms:
    k8s: kubernetes cluster
  hybrid:
    enabled: true
    embedder:
      provider: ollama
      ollama:
        url: http://localhost:11434
        model: nomic-embed-text
`)
	cfg, err := LoadConfig(context.Background(), path)
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	ts := cfg.ToolSearch
	if ts == nil || ts.Synonyms["k8s"] != "kubernetes cluster" {
		t.Fatalf("tool_search not parsed: %+v", ts)
	}
	if !ts.HybridEnabled() || ts.Hybrid.Embedder.Provider != "ollama" {
		t.Fatalf("tool_search.hybrid not parsed: %+v", ts.Hybrid)
	}
	syn := ts.Options().Synonyms
	if syn["k8s"] != "kubernetes cluster" || syn["pr"] != "pull request" {
		t.Errorf("synonyms = %v, want the custom entry plus the defaults", syn)
	}
}

func TestLoadConfigToolSearch_AbsentIsBM25Only(t *testing.T) {
	cfg, err := LoadConfig(context.Background(), writeToolSearchConfig(t, ""))
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}
	if cfg.ToolSearch.HybridEnabled() {
		t.Error("hybrid tool search must be off by default")
	}
}

func TestLoadConfigToolSearch_Invalid(t *testing.T) {
	for name, block := range map[string]string{
		"multi-word synonym key":  "tool_search:\n  synonyms:\n    \"two words\": x\n",
		"hybrid without provider": "tool_search:\n  hybrid:\n    enabled: true\n",
	} {
		if _, err := LoadConfig(context.Background(), writeToolSearchConfig(t, block)); err == nil || !strings.Contains(err.Error(), "tool_search") {
			t.Errorf("%s: LoadConfig() error = %v, want a tool_search error", name, err)
		}
	}
}

// #315: the injection block is validated at load time (policy actions per
// direction, bands, judge settings).
func TestConfigValidateInjection(t *testing.T) {
	dir := t.TempDir()
	load := func(block string) error {
		t.Helper()
		path := filepath.Join(dir, "leanproxy.yaml")
		if err := os.WriteFile(path, []byte("version: \"1.0\"\nservers: []\n"+block), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadConfig(context.Background(), path)
		return err
	}
	valid := `injection:
  enabled: true
  threshold: 70
  request_policies:
    - {min_risk: 80, max_risk: 100, action: block}
    - {min_risk: 50, max_risk: 79, action: quarantine}
  response_policies:
    - {min_risk: 70, max_risk: 100, action: annotate}
  judge:
    provider: ollama
    model: llama3.1:8b
    threshold: 50
    timeout: 2s
`
	if err := load(valid); err != nil {
		t.Fatalf("valid injection block rejected: %v", err)
	}
	for name, block := range map[string]string{
		"annotate on requests":    "injection:\n  enabled: true\n  request_policies:\n    - {min_risk: 1, max_risk: 100, action: annotate}\n",
		"quarantine on responses": "injection:\n  enabled: true\n  response_policies:\n    - {min_risk: 1, max_risk: 100, action: quarantine}\n",
		"unknown judge provider":  "injection:\n  enabled: true\n  judge: {provider: remote, model: m}\n",
	} {
		if err := load(block); err == nil || !strings.Contains(err.Error(), "injection") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}
