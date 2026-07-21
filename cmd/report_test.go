package cmd

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportCmd_Flags(t *testing.T) {
	defer func() {
		reportFlags.sessionID = ""
		reportFlags.outputPath = ""
		reportFlags.jsonOutput = false
		reportFlags.noSecurity = false
		reportCmd.Flags().Lookup("session-id").Changed = false
		reportCmd.Flags().Lookup("output").Changed = false
	}()
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

func resetReportFlags() {
	reportFlags.export = ""
	reportFlags.since = ""
	reportFlags.sessionID = ""
	reportFlags.outputPath = ""
	reportFlags.jsonOutput = false
	reportFlags.noSecurity = false
	flagValues := map[string]string{
		"export":      "",
		"since":       "",
		"session-id":  "",
		"output":      "",
		"json":        "false",
		"no-security": "false",
		"help":        "false",
	}
	for _, name := range []string{"export", "since", "session-id", "output", "json", "no-security", "help"} {
		if f := reportCmd.Flags().Lookup(name); f != nil {
			if def, ok := flagValues[name]; ok {
				_ = f.Value.Set(def)
			}
			f.Changed = false
		}
	}
}

func TestReportCmd_HelpOutput(t *testing.T) {
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--help"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestReportCmd_JsonFlag(t *testing.T) {
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--json"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err != nil {
		t.Errorf("json flag should not error: %v", err)
	}
}

func TestReportCmd_NoSecurityFlag(t *testing.T) {
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--no-security"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err != nil {
		t.Errorf("no-security flag should not error: %v", err)
	}
}

func TestReportCmd_OutputToFile(t *testing.T) {
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "report.md")

	RootCmd.SetArgs([]string{"report", "--output", outputPath})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
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

func TestReportCmd_ExportFlag(t *testing.T) {
	if err := reportCmd.Flags().Set("export", "csv"); err != nil {
		t.Fatalf("set flag export: %v", err)
	}

	got, err := reportCmd.Flags().GetString("export")
	if err != nil {
		t.Fatalf("get flag export: %v", err)
	}
	if got != "csv" {
		t.Errorf("flag export = %v, want csv", got)
	}

	reportFlags.export = ""
	reportCmd.Flags().Lookup("export").Changed = false
}

func TestReportCmd_SinceFlag(t *testing.T) {
	if err := reportCmd.Flags().Set("since", "2026-01-01"); err != nil {
		t.Fatalf("set flag since: %v", err)
	}

	got, err := reportCmd.Flags().GetString("since")
	if err != nil {
		t.Fatalf("get flag since: %v", err)
	}
	if got != "2026-01-01" {
		t.Errorf("flag since = %v, want 2026-01-01", got)
	}

	reportFlags.since = ""
	reportCmd.Flags().Lookup("since").Changed = false
}

func TestReportCmd_ExportCSV(t *testing.T) {
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "export.csv")

	RootCmd.SetArgs([]string{"report", "--export", "csv", "--output", outputPath})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err != nil {
		t.Fatalf("export csv should not error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	r := csv.NewReader(strings.NewReader(string(data)))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parsing csv: %v", err)
	}
	if len(records) < 1 {
		t.Fatal("expected at least header row")
	}
	if records[0][0] != "timestamp" {
		t.Errorf("header[0] = %q, want %q", records[0][0], "timestamp")
	}
}

func TestReportCmd_ExportJSON(t *testing.T) {
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "export.json")

	RootCmd.SetArgs([]string{"report", "--export", "json", "--output", outputPath})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err != nil {
		t.Fatalf("export json should not error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	if len(data) < 2 || data[0] != '[' || data[len(data)-1] != ']' {
		t.Errorf("expected JSON array, got: %s", string(data))
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(data, &rows); err != nil {
		t.Fatalf("unmarshaling json: %v", err)
	}
}

func TestReportCmd_ExportInvalidFormat(t *testing.T) {
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--export", "xml"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid export format")
	}
	if !strings.Contains(err.Error(), "xml") {
		t.Errorf("error should mention invalid format, got: %v", err)
	}
}

func TestReportCmd_ExportSinceInvalid(t *testing.T) {
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--export", "csv", "--since", "not-a-date"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid date")
	}
	if !strings.Contains(err.Error(), "not-a-date") {
		t.Errorf("error should mention the invalid date, got: %v", err)
	}
}

func TestReportCmd_SinceWithoutExport(t *testing.T) {
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--since", "2026-01-01"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("expected error when --since used without --export")
	}
	if !strings.Contains(err.Error(), "--since") {
		t.Errorf("error should mention --since, got: %v", err)
	}
}
