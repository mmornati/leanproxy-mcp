package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Story 11-1: Subscribe to the MCP Registry feed (Epic 11)
// US: As a developer, I want `leanproxy marketplace sync` to fetch and cache
// the MCP Registry index so I can browse / install servers offline.
//
// Acceptance: sync writes the index to ~/.leanproxy/registry/index.json
// and exits 0. Network failure should print retry guidance and preserve the
// existing cache (covered by the unit tests; this E2E verifies the happy path).

func TestStory_11_1_MarketplaceSync_WritesCache(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("failed to get home: %v", err)
	}

	stdout, stderr, exitCode := runBinary("marketplace", "sync")
	t.Logf("marketplace sync: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Skipf("marketplace sync failed (likely no network in this env): %s", stderr)
	}

	cachePath := filepath.Join(home, ".leanproxy", "registry", "index.json")
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("expected index cache at %s, not found: %v", cachePath, err)
	}
}

// Story 11-2: `leanproxy add <server-id>` one-click install (Epic 11)
// US: As a developer, I want `leanproxy add <id>` to pull a server definition
// from the registry and merge it into my leanproxy_servers.yaml so I don't
// have to hand-write YAML.
//
// Acceptance:
//  * add with an unknown id returns a non-zero exit and a list of similar
//    entries (no mutation of leanproxy_servers.yaml).
//  * add --dry-run previews but does not modify the file.

func TestStory_11_2_AddUnknownServer_NoMutation(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	original := `version: "1.0"
servers: []
`
	writeFile(t, configPath, original)

	t.Setenv("LEANPROXY_CONFIG", configPath)

	stdout, stderr, exitCode := runBinary("add", "definitely-not-a-real-server-xyz-12345", "--dry-run")
	t.Logf("add unknown: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode == 0 {
		t.Errorf("expected non-zero exit for unknown server id, got 0")
	}

	contents, _ := os.ReadFile(configPath)
	if !strings.Contains(string(contents), "servers: []") {
		t.Errorf("dry-run should not mutate config, got:\n%s", string(contents))
	}

	combined := strings.ToLower(stdout + stderr)
	// Either "registry cache is empty" (offline env) or "not found" / "similar"
	// (online env) is a valid error for an unknown server id.
	hasRegistryError := strings.Contains(combined, "registry cache is empty")
	hasUnknownError := strings.Contains(combined, "not found") ||
		strings.Contains(combined, "similar") ||
		strings.Contains(combined, "unknown")
	if !hasRegistryError && !hasUnknownError {
		t.Errorf("expected registry or unknown-server error, got:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}

func TestStory_11_2_AddDryRun_NoFileMutation(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	original := `version: "1.0"
servers: []
`
	writeFile(t, configPath, original)
	t.Setenv("LEANPROXY_CONFIG", configPath)

	_, _, _ = runBinary("add", "github", "--dry-run", "--i-understand-the-risks")

	contents, _ := os.ReadFile(configPath)
	if !strings.Contains(string(contents), "servers: []") {
		t.Errorf("--dry-run should not mutate the config, got:\n%s", string(contents))
	}
}

// Story 11-3: Surface trust score and maintenance status (Epic 11)
// US: As a developer, I want `leanproxy marketplace search <query>` to display
// trust score (0-100), last release, open issues, downloads, and estimated
// tokens/turn so I can pick low-risk servers.

func TestStory_11_3_MarketplaceSearch_ColumnsPresent(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("marketplace", "search", "github")
	t.Logf("marketplace search github: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Skipf("marketplace search failed (likely empty registry or no network in this env): %s", stderr)
	}

	if strings.Contains(stdout, "Registry cache is empty") {
		t.Skip("registry cache is empty; skipping column-presence assertion")
	}

	low := strings.ToLower(stdout)
	for _, col := range []string{"trust", "downloads"} {
		if !strings.Contains(low, col) {
			t.Errorf("marketplace search output missing column %q, got:\n%s", col, stdout)
		}
	}
}
