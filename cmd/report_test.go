package cmd

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func resetReportFlags() {
	reportFlags.since = ""
	reportFlags.by = "tool"
	reportFlags.export = ""
	reportFlags.outputPath = ""
	reportFlags.jsonOutput = false
	reportFlags.pricePerMTok = ""
	defaults := map[string]string{
		"since": "", "by": "tool", "export": "", "output": "",
		"json": "false", "price-per-mtok": "", "help": "false",
	}
	for name, def := range defaults {
		if f := reportCmd.Flags().Lookup(name); f != nil {
			_ = f.Value.Set(def)
			f.Changed = false
		}
	}
}

// isolateUsageHome points $HOME at a fresh temp dir so tests never touch
// (or depend on) the real ~/.leanproxy/usage.
func isolateUsageHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestReportCmd_Flags(t *testing.T) {
	defer resetReportFlags()

	if err := reportCmd.Flags().Set("by", "server"); err != nil {
		t.Fatalf("set flag by: %v", err)
	}
	got, err := reportCmd.Flags().GetString("by")
	if err != nil {
		t.Fatalf("get flag by: %v", err)
	}
	if got != "server" {
		t.Errorf("flag by = %v, want server", got)
	}

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
	isolateUsageHome(t)
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--help"})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err != nil {
		t.Errorf("help should not error: %v", err)
	}
}

func TestReportCmd_JsonFlag(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--json"})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err != nil {
		t.Errorf("json flag should not error: %v", err)
	}
}

func TestReportCmd_OutputToFile(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "report.md")

	RootCmd.SetArgs([]string{"report", "--output", outputPath})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err != nil {
		t.Errorf("output flag should not error: %v", err)
	}

	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("stat output: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("report output mode = %o, want 0600", perm)
	}
}

func TestReportCmd_ExportFlag(t *testing.T) {
	defer resetReportFlags()
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
}

func TestReportCmd_SinceFlag(t *testing.T) {
	defer resetReportFlags()
	if err := reportCmd.Flags().Set("since", "7d"); err != nil {
		t.Fatalf("set flag since: %v", err)
	}
	got, err := reportCmd.Flags().GetString("since")
	if err != nil {
		t.Fatalf("get flag since: %v", err)
	}
	if got != "7d" {
		t.Errorf("flag since = %v, want 7d", got)
	}
}

func TestReportCmd_ExportCSV(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "export.csv")

	RootCmd.SetArgs([]string{"report", "--export", "csv", "--output", outputPath})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err != nil {
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
	if records[0][0] != "mechanism" {
		t.Errorf("header[0] = %q, want %q", records[0][0], "mechanism")
	}
}

func TestReportCmd_ExportJSON(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "export.json")

	RootCmd.SetArgs([]string{"report", "--export", "json", "--output", outputPath})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("export json should not error: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}

	var rep map[string]interface{}
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("unmarshaling json: %v", err)
	}
	for _, field := range []string{"generated_at", "estimator", "mechanisms", "total_saved_tokens", "methodology"} {
		if _, ok := rep[field]; !ok {
			t.Errorf("json export missing field %q", field)
		}
	}
}

func TestReportCmd_ExportMarkdown(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--export", "md"})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err != nil {
		t.Fatalf("export md should not error: %v", err)
	}
}

func TestReportCmd_ExportInvalidFormat(t *testing.T) {
	isolateUsageHome(t)
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

func TestReportCmd_InvalidBy(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--by", "team"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --by")
	}
	if !strings.Contains(err.Error(), "team") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestReportCmd_SinceInvalid(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--since", "not-a-date"})
	defer RootCmd.SetArgs(nil)

	err := RootCmd.Execute()
	if err == nil {
		t.Fatal("expected error for invalid --since")
	}
	if !strings.Contains(err.Error(), "not-a-date") {
		t.Errorf("error should mention the invalid value, got: %v", err)
	}
}

func TestReportCmd_InvalidPrice(t *testing.T) {
	isolateUsageHome(t)
	resetReportFlags()
	RootCmd.SetArgs([]string{"report", "--price-per-mtok", "not-a-number"})
	defer RootCmd.SetArgs(nil)

	if err := RootCmd.Execute(); err == nil {
		t.Fatal("expected error for invalid --price-per-mtok")
	}
}

func TestParseSinceFlag(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"", false},
		{"7d", false},
		{"24h", false},
		{"1d12h", false},
		{"2026-01-01", false},
		{"not-a-date", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := parseSinceFlag(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseSinceFlag(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if tt.in == "" && !got.IsZero() {
				t.Errorf("parseSinceFlag(\"\") = %v, want zero time", got)
			}
			if tt.in == "7d" {
				want := time.Now().Add(-7 * 24 * time.Hour)
				if got.Sub(want).Abs() > time.Minute {
					t.Errorf("parseSinceFlag(7d) = %v, want ~%v", got, want)
				}
			}
		})
	}
}
