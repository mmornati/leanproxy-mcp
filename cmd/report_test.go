package cmd

import (
	"path/filepath"
	"testing"
)

func TestReportCmd_Flags(t *testing.T) {
	boolFlags := []struct {
		name string
		flag string
	}{
		{"json", "json"},
		{"no-security", "no-security"},
	}

	for _, tt := range boolFlags {
		t.Run(tt.name, func(t *testing.T) {
			if err := reportCmd.Flags().Set(tt.flag, "true"); err != nil {
				t.Fatalf("set flag %s: %v", tt.flag, err)
			}

			got, err := reportCmd.Flags().GetBool(tt.flag)
			if err != nil {
				t.Fatalf("get flag %s: %v", tt.flag, err)
			}
			if !got {
				t.Errorf("flag %s = %v, want true", tt.flag, got)
			}
		})
	}

	t.Run("session-id", func(t *testing.T) {
		if err := reportCmd.Flags().Set("session-id", "test-session"); err != nil {
			t.Fatalf("set flag session-id: %v", err)
		}

		got, err := reportCmd.Flags().GetString("session-id")
		if err != nil {
			t.Fatalf("get flag session-id: %v", err)
		}
		if got != "test-session" {
			t.Errorf("flag session-id = %v, want test-session", got)
		}
	})

	t.Run("output", func(t *testing.T) {
		tmpDir := t.TempDir()
		outputPath := filepath.Join(tmpDir, "report.md")

		if err := reportCmd.Flags().Set("output", outputPath); err != nil {
			t.Fatalf("set flag output: %v", err)
		}

		got, err := reportCmd.Flags().GetString("output")
		if err != nil {
			t.Fatalf("get flag output: %v", err)
		}
		if got != outputPath {
			t.Errorf("flag output = %v, want %v", got, outputPath)
		}
	})
}

func TestReportCmd_HelpOutput(t *testing.T) {
	cmd := reportCmd
	cmd.SetArgs([]string{"--help"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestReportCmd_JsonFlag(t *testing.T) {
	cmd := reportCmd
	cmd.SetArgs([]string{"--json"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("json flag should not error: %v", err)
	}
}

func TestReportCmd_NoSecurityFlag(t *testing.T) {
	cmd := reportCmd
	cmd.SetArgs([]string{"--no-security"})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("no-security flag should not error: %v", err)
	}
}

func TestReportCmd_OutputToFile(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "report.md")

	cmd := reportCmd
	cmd.SetArgs([]string{"--output", outputPath})

	err := cmd.Execute()
	if err != nil {
		t.Errorf("output flag should not error: %v", err)
	}
}

func TestBuildSessionMetrics(t *testing.T) {
	result := buildSessionMetrics()
	if result.SessionID == "" {
		t.Error("expected non-empty session ID")
	}
	if result.TotalRequests < 0 {
		t.Error("expected non-negative total requests")
	}
}

func TestGlobalReportGenerator(t *testing.T) {
	if globalReportGenerator == nil {
		t.Error("expected non-nil report generator")
	}
}