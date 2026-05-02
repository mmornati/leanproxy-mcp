package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenCodeScanner_Name(t *testing.T) {
	s := &OpenCodeScanner{}
	if got := s.Name(); got != "opencode" {
		t.Errorf("Name() = %v, want opencode", got)
	}
}

func TestOpenCodeScanner_Scan_NotFound(t *testing.T) {
	tmpDir := os.TempDir()
	emptyDir := filepath.Join(tmpDir, "empty_home")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(emptyDir)

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", emptyDir)
	defer os.Setenv("HOME", originalHome)

	s := &OpenCodeScanner{}
	servers, err := s.Scan(context.Background())
	if err != nil {
		t.Errorf("Scan() error = %v", err)
	}
	if servers != nil && len(servers) > 0 {
		t.Errorf("Scan() = %v, want nil/empty for non-existent file", servers)
	}
}

func TestOpenCodeScanner_Scan_Found(t *testing.T) {
	tmpDir := os.TempDir()
	cfgDir := filepath.Join(tmpDir, ".config", "opencode")
	cfgPath := filepath.Join(cfgDir, "opencode.json")

	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}
	defer os.RemoveAll(filepath.Join(tmpDir, ".config"))

	cfg := `{
		"mcp": {
			"test-server": {
				"type": "local",
				"command": ["/usr/bin/test-server", "--flag"],
				"enabled": true
			}
		}
	}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("Failed to write temp config: %v", err)
	}

	originalHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	s := &OpenCodeScanner{}
	servers, err := s.Scan(context.Background())
	if err != nil {
		t.Errorf("Scan() error = %v", err)
	}
	if len(servers) != 1 {
		t.Errorf("Scan() got %d servers, want 1", len(servers))
		return
	}
	if servers[0].Name != "test-server" {
		t.Errorf("Scan() got name %q, want test-server", servers[0].Name)
	}
	if servers[0].Source != "opencode" {
		t.Errorf("Scan() got source %q, want opencode", servers[0].Source)
	}
	if servers[0].Stdio.Command != "/usr/bin/test-server" {
		t.Errorf("Scan() got command %q, want /usr/bin/test-server", servers[0].Stdio.Command)
	}
	if len(servers[0].Stdio.Args) != 1 || servers[0].Stdio.Args[0] != "--flag" {
		t.Errorf("Scan() got args %v, want [--flag]", servers[0].Stdio.Args)
	}
	if servers[0].Enabled == nil || !*servers[0].Enabled {
		t.Errorf("Scan() got enabled %v, want true", servers[0].Enabled)
	}
}

func TestClaudeScanner_Name(t *testing.T) {
	s := &ClaudeScanner{}
	if got := s.Name(); got != "claude" {
		t.Errorf("Name() = %v, want claude", got)
	}
}

func TestClaudeScanner_Scan_NotFound(t *testing.T) {
	s := &ClaudeScanner{}
	servers, err := s.Scan(context.Background())
	if err != nil {
		t.Errorf("Scan() error = %v", err)
	}
	if len(servers) != 0 {
		t.Errorf("Scan() = %v, want empty for non-existent files", servers)
	}
}

func TestCursorScanner_Name(t *testing.T) {
	s := &CursorScanner{}
	if got := s.Name(); got != "cursor" {
		t.Errorf("Name() = %v, want cursor", got)
	}
}

func TestCursorScanner_Scan_NotFound(t *testing.T) {
	s := &CursorScanner{}
	servers, err := s.Scan(context.Background())
	if err != nil {
		t.Errorf("Scan() error = %v", err)
	}
	if servers != nil {
		t.Errorf("Scan() = %v, want nil for non-existent file", servers)
	}
}

func TestGenericScanner_Name(t *testing.T) {
	s := &GenericScanner{}
	if got := s.Name(); got != "generic" {
		t.Errorf("Name() = %v, want generic", got)
	}
}

func TestGenericScanner_Scan_NotFound(t *testing.T) {
	s := &GenericScanner{}
	servers, err := s.Scan(context.Background())
	if err != nil {
		t.Errorf("Scan() error = %v", err)
	}
	if servers != nil {
		t.Errorf("Scan() = %v, want nil for non-existent file", servers)
	}
}

func TestVSCodeScanner_Name(t *testing.T) {
	s := &VSCodeScanner{}
	if got := s.Name(); got != "vscode" {
		t.Errorf("Name() = %v, want vscode", got)
	}
}

func TestExecutableExists(t *testing.T) {
	if !ExecutableExists("ls") {
		t.Error("ExecutableExists(ls) = false, want true")
	}
	if ExecutableExists("nonexistent_command_12345") {
		t.Error("ExecutableExists(nonexistent) = true, want false")
	}
}

func TestExpandPath(t *testing.T) {
	tmpDir := os.TempDir()

	if got := expandPath("~"); got != tmpDir && got != os.Getenv("HOME") {
		// On systems without HOME set, this may differ
	}
	if got := expandPath("~/test"); got != filepath.Join(os.Getenv("HOME"), "test") {
		t.Errorf("expandPath(~/test) = %v", got)
	}
	if expandPath("/absolute/path") != "/absolute/path" {
		t.Errorf("expandPath(/absolute/path) = %v", expandPath("/absolute/path"))
	}
}

func TestFileExists(t *testing.T) {
	tmpFile := filepath.Join(os.TempDir(), "test_migrate_file_exists.txt")
	os.WriteFile(tmpFile, []byte("test"), 0644)
	defer os.Remove(tmpFile)

	if !fileExists(tmpFile) {
		t.Errorf("fileExists(%s) = false, want true", tmpFile)
	}
	if fileExists("/nonexistent/path") {
		t.Error("fileExists(/nonexistent/path) = true, want false")
	}
}