package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDebugFullReport(t *testing.T) {
	resetReportFlags()
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "export.csv")

	t.Logf("RunE is nil: %v", reportCmd.RunE == nil)
	t.Logf("Runnable: %v", reportCmd.Runnable())

	RootCmd.SetArgs([]string{"report", "--export", "csv", "--output", outputPath})
	err := RootCmd.Execute()
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		t.Errorf("output file NOT created")
	} else {
		data, _ := os.ReadFile(outputPath)
		t.Logf("CSV content: %s", string(data))
	}
}
