package migrate

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewMigrator(t *testing.T) {
	m := NewMigrator()
	if m == nil {
		t.Fatal("NewMigrator() returned nil")
	}
	if len(m.scanners) != 5 {
		t.Errorf("NewMigrator() has %d scanners, want 5", len(m.scanners))
	}
}

func TestMigrator_Scan_NoServers(t *testing.T) {
	m := &Migrator{
		scanners: []Scanner{},
	}

	result, err := m.Scan(context.Background())
	if err != nil {
		t.Errorf("Scan() error = %v", err)
	}
	if len(result.Servers) != 0 {
		t.Errorf("Scan() got %d servers, want 0", len(result.Servers))
	}
	if len(result.Scanners) != 0 {
		t.Errorf("Scan() got %d scanners, want 0", len(result.Scanners))
	}
}

func TestMigrator_Summarize(t *testing.T) {
	m := &Migrator{}

	servers := []DiscoveredServer{
		{Name: "srv1", Source: "opencode"},
		{Name: "srv2", Source: "opencode"},
		{Name: "srv3", Source: "claude"},
		{Name: "srv4", Source: "vscode"},
		{Name: "srv5", Source: "cursor"},
		{Name: "srv6", Source: "generic"},
	}

	summary := m.Summarize(servers)

	if summary.TotalServers != 6 {
		t.Errorf("Summarize() total = %d, want 6", summary.TotalServers)
	}
	if summary.OpenCodeCount != 2 {
		t.Errorf("Summarize() opencode = %d, want 2", summary.OpenCodeCount)
	}
	if summary.ClaudeCount != 1 {
		t.Errorf("Summarize() claude = %d, want 1", summary.ClaudeCount)
	}
	if summary.VSCodeCount != 1 {
		t.Errorf("Summarize() vscode = %d, want 1", summary.VSCodeCount)
	}
	if summary.CursorCount != 1 {
		t.Errorf("Summarize() cursor = %d, want 1", summary.CursorCount)
	}
	if summary.GenericCount != 1 {
		t.Errorf("Summarize() generic = %d, want 1", summary.GenericCount)
	}
}

func TestMigrator_Summarize_Empty(t *testing.T) {
	m := &Migrator{}

	summary := m.Summarize([]DiscoveredServer{})

	if summary.TotalServers != 0 {
		t.Errorf("Summarize() total = %d, want 0", summary.TotalServers)
	}
}

func TestMigrator_Import_NoServers(t *testing.T) {
	m := &Migrator{}

	_, err := m.Import(context.Background(), []DiscoveredServer{}, "/tmp/test.yaml", false)
	if err == nil {
		t.Error("Import() expected error for empty servers")
	}
}

func TestMigrator_Import_SingleServer(t *testing.T) {
	tmpDir := os.TempDir()
	targetPath := filepath.Join(tmpDir, "test_import_single.yaml")
	defer os.Remove(targetPath)

	m := &Migrator{}

	servers := []DiscoveredServer{
		{
			Name:      "test-server",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "/usr/bin/test",
				Args:    []string{"--flag"},
			},
		},
	}

	result, err := m.Import(context.Background(), servers, targetPath, true)
	if err != nil {
		t.Errorf("Import() error = %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Import() imported = %d, want 1", result.Imported)
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Errorf("Failed to read target file: %v", err)
	}
	if len(data) == 0 {
		t.Error("Target file is empty")
	}
}

func TestMigrator_Import_DuplicateNames(t *testing.T) {
	tmpDir := os.TempDir()
	targetPath := filepath.Join(tmpDir, "test_import_dup.yaml")
	defer os.Remove(targetPath)

	initialCfg := `version: "1.0"
servers:
  - name: existing-server
    transport: stdio
    stdio:
      command: /usr/bin/existing
`
	if err := os.WriteFile(targetPath, []byte(initialCfg), 0644); err != nil {
		t.Fatalf("Failed to write initial config: %v", err)
	}

	m := &Migrator{}

	servers := []DiscoveredServer{
		{
			Name:      "existing-server",
			Source:    "opencode",
			Transport: "stdio",
			Stdio: &StdioConfig{
				Command: "/usr/bin/new",
			},
		},
	}

	result, err := m.Import(context.Background(), servers, targetPath, true)
	if err != nil {
		t.Errorf("Import() error = %v", err)
	}
	if result.Imported != 1 {
		t.Errorf("Import() imported = %d, want 1", result.Imported)
	}
}

func TestMigrationSummary_Total(t *testing.T) {
	s := &MigrationSummary{
		OpenCodeCount: 2,
		ClaudeCount:   1,
		VSCodeCount:   3,
		CursorCount:   0,
		GenericCount:  1,
	}

	if got := s.Total(); got != 7 {
		t.Errorf("Total() = %d, want 7", got)
	}
}