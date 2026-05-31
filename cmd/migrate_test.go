package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateCmd_Flags(t *testing.T) {
	boolFlags := []string{"dry-run", "validate-only", "yes"}
	stringFlags := map[string]string{
		"target": "/tmp/test.yaml",
	}

	for _, flag := range boolFlags {
		t.Run(flag, func(t *testing.T) {
			if err := migrateCmd.Flags().Set(flag, "true"); err != nil {
				t.Fatalf("set flag %s: %v", flag, err)
			}

			got, err := migrateCmd.Flags().GetBool(flag)
			if err != nil {
				t.Fatalf("get flag %s: %v", flag, err)
			}
			if !got {
				t.Errorf("flag %s = %v, want true", flag, got)
			}
		})
	}

	for flag, want := range stringFlags {
		t.Run(flag, func(t *testing.T) {
			if err := migrateCmd.Flags().Set(flag, want); err != nil {
				t.Fatalf("set flag %s: %v", flag, err)
			}

			got, err := migrateCmd.Flags().GetString(flag)
			if err != nil {
				t.Fatalf("get flag %s: %v", flag, err)
			}
			if got != want {
				t.Errorf("flag %s = %v, want %v", flag, got, want)
			}
		})
	}
}

func TestMigrateCmd_HelpOutput(t *testing.T) {
	cmd := migrateCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestMigrateCmd_DryRunFlag(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", configPath)

	cfg := `version: "1.0"
servers: []
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := migrateCmd
	cmd.SetArgs([]string{"--dry-run"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("dry-run should not error: %v", err)
	}
}

func TestMigrateCmd_ValidateOnlyFlag(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", configPath)

	cfg := `version: "1.0"
servers: []
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := migrateCmd
	cmd.SetArgs([]string{"--validate-only"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("validate-only should not error: %v", err)
	}
}

func TestMigrateCmd_TargetFlag(t *testing.T) {
	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "target.yaml")
	t.Setenv("LEANPROXY_CONFIG", "")

	cmd := migrateCmd
	cmd.SetArgs([]string{"--target", targetPath, "--dry-run"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("target flag should not error: %v", err)
	}
}

func TestMigrateCmd_DryRunEnabled(t *testing.T) {
	DryRunEnabled = true
	defer func() { DryRunEnabled = false }()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "servers.yaml")
	t.Setenv("LEANPROXY_CONFIG", configPath)

	cfg := `version: "1.0"
servers: []
`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := migrateCmd
	cmd.SetArgs([]string{})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("dry-run enabled should not error: %v", err)
	}
}
