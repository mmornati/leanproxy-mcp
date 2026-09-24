package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #324: `leanproxy-mcp report --export {csv,json,md}` is the
// auditable savings report built from real counters (pkg/usage), not the
// old finance cost-entry export. These tests replace the pre-#324
// Story 18-3 assertions (raw CallLogEntry rows), which no longer match
// report's output shape.
//
// Acceptance (issue #324):
//  * --export csv -> header row starts with mechanism,measured,is_cost,...
//  * --export json -> a valid JSON object with the documented schema
//    (mechanisms, total_saved_tokens, methodology, ...)
//  * --output writes to the specified file (mode 0600)
//  * No payload / argument / secret ever appears in the export

func TestReport_ExportCSV_HeaderAndNoPII(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("report", "--export", "csv")
	t.Logf("report --export csv: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("report --export csv should exit 0, got %d", exitCode)
	}

	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) == 0 {
		t.Fatalf("csv export produced no output")
	}

	header := lines[0]
	expectedCols := []string{"mechanism", "measured", "is_cost", "original_tokens", "resulting_tokens", "saved_tokens", "calls", "notes"}
	for _, col := range expectedCols {
		if !strings.Contains(header, col) {
			t.Errorf("csv header missing column %q, got: %q", col, header)
		}
	}

	for _, col := range []string{"prompt", "payload", "secret", "password", "api_key"} {
		if strings.Contains(strings.ToLower(stdout), col) {
			t.Errorf("csv export must not contain PII / prompt data (found %q)", col)
		}
	}
}

func TestReport_ExportJSON_ValidObject(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("report", "--export", "json")
	t.Logf("report --export json: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("report --export json should exit 0, got %d", exitCode)
	}

	trimmed := strings.TrimSpace(stdout)
	var rep map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &rep); err != nil {
		t.Fatalf("report --export json did not return a JSON object: %v\nraw=%s", err, trimmed)
	}
	for _, field := range []string{"generated_at", "estimator", "mechanisms", "total_saved_tokens", "total_saved_percent", "extra_turns_estimate", "methodology"} {
		if _, ok := rep[field]; !ok {
			t.Errorf("report json missing documented field %q", field)
		}
	}
}

func TestReport_ExportToFile(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	out := filepath.Join(testDir, "report.csv")

	stdout, _, exitCode := runBinary("report", "--export", "csv", "--output", out)
	t.Logf("report --export csv --output: exit=%d stdout=%q", exitCode, stdout)

	if exitCode != 0 {
		t.Fatalf("report --export csv --output should exit 0, got %d", exitCode)
	}

	info, err := os.Stat(out)
	if err != nil {
		t.Fatalf("output file not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("report output file mode = %o, want 0600", perm)
	}

	contents, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if !strings.Contains(string(contents), "mechanism") {
		t.Errorf("output file should contain the csv header, got: %s", string(contents))
	}
}

func TestReport_ExportSinceFilter(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("report", "--export", "json", "--since", "7d")
	t.Logf("report --export json --since: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("report --export json --since should exit 0, got %d", exitCode)
	}
}

func TestReport_ExportMarkdown(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("report", "--export", "md")
	t.Logf("report --export md: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("report --export md should exit 0, got %d", exitCode)
	}
	if !strings.Contains(stdout, "# LeanProxy Savings Report") {
		t.Errorf("markdown export missing the report heading, got: %q", stdout)
	}
}
