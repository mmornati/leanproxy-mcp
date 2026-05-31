package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
)

func TestSaveConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "servers.yaml")

	cfg := &migrate.Config{
		Version: "1.0",
		Servers: []*migrate.ServerConfig{},
	}

	err := saveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("expected config file to be created")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	if len(data) == 0 {
		t.Error("expected non-empty config file")
	}
}

func TestSaveConfig_CreatesDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "nested", "servers.yaml")

	cfg := &migrate.Config{
		Version: "1.0",
		Servers: []*migrate.ServerConfig{},
	}

	err := saveConfig(configPath, cfg)
	if err != nil {
		t.Fatalf("saveConfig() error = %v", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("expected nested config file to be created")
	}
}

func TestJoinStringsFromServer(t *testing.T) {
	tests := []struct {
		name     string
		input    []string
		expected string
	}{
		{"multiple items", []string{"a", "b", "c"}, "a b c "},
		{"single item", []string{"a"}, "a "},
		{"empty", []string{}, ""},
		{"nil slice", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := joinStrings(tt.input)
			if result != tt.expected {
				t.Errorf("joinStrings() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestTrimStdioNewline(t *testing.T) {
	tests := []struct {
		name     string
		input    []byte
		expected []byte
	}{
		{"newline", []byte("hello\n"), []byte("hello")},
		{"crlf", []byte("hello\r\n"), []byte("hello")},
		{"crlf then newline", []byte("hello\n\r"), []byte("hello\n")},
		{"no newline", []byte("hello"), []byte("hello")},
		{"empty", []byte(""), []byte("")},
		{"just newline", []byte("\n"), []byte("")},
		{"just crlf", []byte("\r\n"), []byte("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := trimStdioNewline(tt.input)
			if string(result) != string(tt.expected) {
				t.Errorf("trimStdioNewline(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestUserConfigPath_EnvVarTakesPrecedence(t *testing.T) {
	tmpDir := t.TempDir()
	expectedPath := filepath.Join(tmpDir, "custom.yaml")
	t.Setenv("LEANPROXY_CONFIG", expectedPath)

	result := userConfigPath()

	if result != expectedPath {
		t.Errorf("userConfigPath() = %v, want %v", result, expectedPath)
	}
}

func TestUserConfigPath_FallsBackToHome(t *testing.T) {
	t.Setenv("LEANPROXY_CONFIG", "")

	home := os.Getenv("HOME")
	if home == "" {
		t.Skip("HOME not set")
	}

	result := userConfigPath()
	expected := filepath.Join(home, ".config", "leanproxy_servers.yaml")

	if result != expected {
		t.Errorf("userConfigPath() = %v, want %v", result, expected)
	}
}

func TestUserConfigPath_FallsBackToUserProfile(t *testing.T) {
	t.Setenv("LEANPROXY_CONFIG", "")
	t.Setenv("HOME", "")

	userprofile := os.Getenv("USERPROFILE")
	if userprofile == "" {
		t.Skip("USERPROFILE not set")
	}

	result := userConfigPath()
	expected := filepath.Join(userprofile, ".config", "leanproxy_servers.yaml")

	if result != expected {
		t.Errorf("userConfigPath() = %v, want %v", result, expected)
	}
}

func TestAddCmd_Flags(t *testing.T) {
	if err := addCmd.Flags().Set("transport", "stdio"); err != nil {
		t.Fatalf("set transport flag: %v", err)
	}

	if err := addCmd.Flags().Set("cwd", "/tmp"); err != nil {
		t.Fatalf("set cwd flag: %v", err)
	}

	transport, err := addCmd.Flags().GetString("transport")
	if err != nil {
		t.Fatalf("get transport: %v", err)
	}
	if transport != "stdio" {
		t.Errorf("transport = %v, want stdio", transport)
	}

	cwd, err := addCmd.Flags().GetString("cwd")
	if err != nil {
		t.Fatalf("get cwd: %v", err)
	}
	if cwd != "/tmp" {
		t.Errorf("cwd = %v, want /tmp", cwd)
	}
}

func TestAddCmd_FlagsDefaultTransport(t *testing.T) {
	transport, err := addCmd.Flags().GetString("transport")
	if err != nil {
		t.Fatalf("get transport: %v", err)
	}
	if transport != "stdio" {
		t.Errorf("default transport = %v, want stdio", transport)
	}
}

func TestListCmd_Flags(t *testing.T) {
	if err := listCmd.Flags().Set("source", "opencode"); err != nil {
		t.Fatalf("set source flag: %v", err)
	}

	source, err := listCmd.Flags().GetString("source")
	if err != nil {
		t.Fatalf("get source: %v", err)
	}
	if source != "opencode" {
		t.Errorf("source = %v, want opencode", source)
	}
}

func TestRunCmd_Flags(t *testing.T) {
	if err := runCmd.Flags().Set("stdio", "true"); err != nil {
		t.Fatalf("set stdio flag: %v", err)
	}

	if err := runCmd.Flags().Set("log-level", "debug"); err != nil {
		t.Fatalf("set log-level flag: %v", err)
	}

	if err := runCmd.Flags().Set("verbose", "true"); err != nil {
		t.Fatalf("set verbose flag: %v", err)
	}

	stdio, err := runCmd.Flags().GetBool("stdio")
	if err != nil {
		t.Fatalf("get stdio: %v", err)
	}
	if !stdio {
		t.Error("stdio should be true")
	}

	logLevel, err := runCmd.Flags().GetString("log-level")
	if err != nil {
		t.Fatalf("get log-level: %v", err)
	}
	if logLevel != "debug" {
		t.Errorf("log-level = %v, want debug", logLevel)
	}

	verbose, err := runCmd.Flags().GetBool("verbose")
	if err != nil {
		t.Fatalf("get verbose: %v", err)
	}
	if !verbose {
		t.Error("verbose should be true")
	}
}

func TestRunServerList_EmptyConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", configPath)

	cfg := `version: "1.0"
servers: []
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := listCmd
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Fatalf("list should not error for empty config: %v", err)
	}
}

func TestServerListCmd_HelpOutput(t *testing.T) {
	cmd := listCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServerAddCmd_HelpOutput(t *testing.T) {
	cmd := addCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServerRemoveCmd_HelpOutput(t *testing.T) {
	cmd := removeCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServerEnableCmd_HelpOutput(t *testing.T) {
	cmd := enableCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServerDisableCmd_HelpOutput(t *testing.T) {
	cmd := disableCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServerRunCmd_HelpOutput(t *testing.T) {
	cmd := runCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServerCmd_HelpOutput(t *testing.T) {
	cmd := serverCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestServeConfig_GetSet(t *testing.T) {
	original := GetConfig()
	defer SetConfig(original)

	cfg := &ServeConfig{
		RequestTimeout: 60 * time.Second,
		MaxBatchSize:   200,
	}

	SetConfig(cfg)

	result := GetConfig()
	if result.RequestTimeout != 60*time.Second {
		t.Errorf("RequestTimeout = %v, want 60s", result.RequestTimeout)
	}
	if result.MaxBatchSize != 200 {
		t.Errorf("MaxBatchSize = %v, want 200", result.MaxBatchSize)
	}
}

func TestServeConfig_SetInvalid(t *testing.T) {
	original := GetConfig()
	defer SetConfig(original)

	SetConfig(nil)

	result := GetConfig()
	if result != original {
		t.Error("nil config should not change serveConfig")
	}

	SetConfig(&ServeConfig{
		RequestTimeout: 0,
		MaxBatchSize:   0,
	})

	result = GetConfig()
	if result != original {
		t.Error("zero values should not change serveConfig")
	}
}

func TestUpdateStdioServerStatusOnce_NilInputs(t *testing.T) {
	updateStdioServerStatusOnce(nil, nil)
	updateStdioServerStatusOnce(&statusfile.FileStatusStore{}, nil)
	updateStdioServerStatusOnce(nil, &pool.StdioPool{})
}

func TestUpdateServerStatus_NilInputs(t *testing.T) {
	done := make(chan struct{})
	go func() {
		updateServerStatus(nil, nil, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
}

func TestCalculateErrorRate(t *testing.T) {
	tests := []struct {
		name     string
		errors   int64
		total    int64
		expected float64
	}{
		{"no errors", 0, 100, 0},
		{"no requests", 0, 0, 0},
		{"50% error rate", 50, 100, 50},
		{"10% error rate", 10, 100, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateErrorRate(tt.errors, tt.total)
			if result != tt.expected {
				t.Errorf("calculateErrorRate(%d, %d) = %v, want %v", tt.errors, tt.total, result, tt.expected)
			}
		})
	}
}

func TestGetLastError(t *testing.T) {
	tests := []struct {
		name     string
		state    pool.ServerState
		stats    pool.ServerStats
		expected string
	}{
		{"error state", pool.StateError, pool.ServerStats{}, "server in error state"},
		{"stopped state", pool.StateStopped, pool.ServerStats{}, "server stopped"},
		{"stopping state", pool.StateStopping, pool.ServerStats{}, "server stopping"},
		{"running with backoff", pool.StateRunning, pool.ServerStats{RestartCount: 1, CurrentBackoff: time.Second}, "restart count: 1, backoff: 1s"},
		{"running no backoff", pool.StateRunning, pool.ServerStats{}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getLastError(tt.state, tt.stats)
			if result != tt.expected {
				t.Errorf("getLastError() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFilterByServer(t *testing.T) {
	statusList := proxy.ServerStatusList{
		Servers: []proxy.ServerStatus{
			{Name: "server1", Status: proxy.StatusRunning},
			{Name: "server2", Status: proxy.StatusRunning},
			{Name: "server3", Status: proxy.StatusRunning},
		},
	}

	result := filterByServer(statusList, "server2")

	if len(result.Servers) != 1 {
		t.Errorf("expected 1 server, got %d", len(result.Servers))
	}
	if len(result.Servers) > 0 && result.Servers[0].Name != "server2" {
		t.Errorf("expected server2, got %s", result.Servers[0].Name)
	}
}

func TestFilterByServer_NotFound(t *testing.T) {
	statusList := proxy.ServerStatusList{
		Servers: []proxy.ServerStatus{
			{Name: "server1", Status: proxy.StatusRunning},
		},
	}

	result := filterByServer(statusList, "nonexistent")

	if len(result.Servers) != 0 {
		t.Errorf("expected 0 servers, got %d", len(result.Servers))
	}
}

func TestHandleStdio_EmptyInput(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", configPath)

	cfg := `version: "1.0"
servers: []
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	tmpFile := filepath.Join(tmpDir, "empty_input")
	if err := os.WriteFile(tmpFile, []byte{}, 0644); err != nil {
		t.Fatalf("write empty input: %v", err)
	}
}
