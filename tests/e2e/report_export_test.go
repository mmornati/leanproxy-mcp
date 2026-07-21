package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 18-3: CSV/JSON export for cost data (Epic 18)
// US: As a finance lead, I want `leanproxy report --export {csv,json} --since
// YYYY-MM-DD [--output <file>]` so I can pipe the cost data into our BI tool
// without re-parsing the markdown report.
//
// Acceptance:
//  * --export csv -> header row: timestamp,team,project,server,tool,tokens,estimated_cost
//  * --export json -> valid JSON array (possibly empty)
//  * --output writes to the specified file (or stdout by default)
//  * No PII / prompts in the exported data (NFR4)

func TestStory_18_3_ExportCSV_HeaderAndNoPII(t *testing.T) {
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
	expectedCols := []string{"timestamp", "team", "project", "server", "tool", "tokens", "estimated_cost"}
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

func TestStory_18_3_ExportJSON_ValidArray(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("report", "--export", "json")
	t.Logf("report --export json: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("report --export json should exit 0, got %d", exitCode)
	}

	trimmed := strings.TrimSpace(stdout)
	var arr []map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		t.Fatalf("report --export json did not return a JSON array: %v\nraw=%s", err, trimmed)
	}
}

func TestStory_18_3_ExportToFile(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	out := filepath.Join(testDir, "costs.csv")

	stdout, _, exitCode := runBinary("report", "--export", "csv", "--output", out)
	t.Logf("report --export csv --output: exit=%d stdout=%q", exitCode, stdout)

	if exitCode != 0 {
		t.Fatalf("report --export csv --output should exit 0, got %d", exitCode)
	}

	contents, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("output file not written: %v", err)
	}

	if len(contents) == 0 {
		t.Errorf("output file is empty")
	}

	if !strings.Contains(string(contents), "timestamp") {
		t.Errorf("output file should contain csv header, got: %s", string(contents))
	}
}

func TestStory_18_3_ExportSinceFilter(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("report", "--export", "json", "--since", "2020-01-01")
	t.Logf("report --export json --since: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("report --export json --since should exit 0, got %d", exitCode)
	}
}
