package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Story 10-3: Cache Hit Rate Report (Epic 10)
// US: As a finance lead, I want to see a Markdown table with per-model cache
// hit rate (total requests, cache hits, hit rate %, tokens saved, estimated $)
// so I can attribute the savings the proxy is delivering.

func TestStory_10_3_CacheStats_DefaultMarkdown(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("cache", "stats")
	t.Logf("cache stats: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("cache stats should exit 0, got %d (stderr=%s)", exitCode, stderr)
	}

	// Without any traffic, the command prints an empty-state message.
	// With traffic, it prints a markdown table. Both are valid.
	if strings.Contains(stdout, "No Anthropic traffic") {
		t.Logf("cache stats reports no traffic yet — empty-state path exercised")
		return
	}

	for _, expected := range []string{"Total Requests", "Cache Hits", "Hit Rate"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("cache stats markdown is missing expected header %q, got:\n%s", expected, stdout)
		}
	}

	for _, expected := range []string{"Tokens Saved", "Estimated Savings"} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("cache stats should mention %q, got:\n%s", expected, stdout)
		}
	}
}

func TestStory_10_3_CacheStats_JSON(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	stdout, stderr, exitCode := runBinary("cache", "stats", "--json")
	t.Logf("cache stats --json: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("cache stats --json should exit 0, got %d", exitCode)
	}

	if strings.Contains(stdout, "No Anthropic traffic") {
		t.Logf("cache stats --json reports no traffic yet — empty-state path exercised")
		return
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("cache stats --json did not return valid JSON: %v\nraw=%s", err, stdout)
	}

	for _, key := range []string{"total_requests", "cache_hits", "hit_rate", "tokens_saved", "estimated_savings"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("cache stats JSON missing expected key %q, got keys: %v", key, mapKeys(parsed))
		}
	}
}

func TestStory_10_3_CacheStats_NoTraffic(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	emptyCache := testDir + "/cache-stats-empty.json"
	t.Setenv("LEANPROXY_CACHE_STATS_PATH", emptyCache)

	stdout, stderr, exitCode := runBinary("cache", "stats")
	t.Logf("cache stats (no traffic): exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("cache stats with no traffic should exit 0 (graceful empty), got %d", exitCode)
	}

	if !strings.Contains(strings.ToLower(stdout+stderr), "no anthropic traffic") {
		t.Errorf("expected empty-state message about no Anthropic traffic, got:\nstdout=%s\nstderr=%s", stdout, stderr)
	}
}

// Story 12-3: Semantic cache TTL, invalidation, and hit/miss dashboard (Epic 12)
// US: As a developer, I want a single command that shows semantic cache
// effectiveness (exact hits vs semantic hits vs misses, hit rate %, avg
// similarity, evicted entries) so I can tune the threshold.

func TestStory_12_3_SemanticCache_DefaultEmpty(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	statsPath := testDir + "/semantic-stats.json"
	t.Setenv("LEANPROXY_SEMANTIC_STATS_PATH", statsPath)

	stdout, stderr, exitCode := runBinary("cache", "--semantic")
	t.Logf("cache --semantic (no stats): exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("cache --semantic with no stats should still exit 0, got %d (stderr=%s)", exitCode, stderr)
	}

	output := strings.ToLower(stdout + stderr)
	// Either the empty-state message or the populated table is valid.
	if strings.Contains(output, "no semantic cache activity") {
		t.Logf("semantic cache empty-state path exercised")
		return
	}
	for _, expected := range []string{"total", "exact", "semantic", "misses", "hit rate"} {
		if !strings.Contains(output, expected) {
			t.Errorf("semantic dashboard missing label %q, got:\nstdout=%s\nstderr=%s", expected, stdout, stderr)
		}
	}
}

func TestStory_12_3_SemanticCache_JSON(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	statsPath := testDir + "/semantic-stats.json"
	t.Setenv("LEANPROXY_SEMANTIC_STATS_PATH", statsPath)

	stdout, stderr, exitCode := runBinary("cache", "--semantic", "--json")
	t.Logf("cache --semantic --json: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Fatalf("cache --semantic --json should exit 0, got %d", exitCode)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &parsed); err != nil {
		t.Fatalf("cache --semantic --json did not return valid JSON: %v\nraw=%s", err, stdout)
	}
}

// Story 10-2: Inject cache breakpoints (Epic 10)
// US: As an operator, I want the proxy to inject Anthropic cache_control
// breakpoints (`--cache-strategy {off,balanced,aggressive}`) so I can
// trade-off cache hit rate vs token cost without code changes.
//
// Acceptance: serve --cache-strategy balanced/aggressive mutates the upstream
// body; default is now `off`. This test only verifies the flag is accepted and
// the serve binary starts.

func TestStory_10_2_CacheStrategyFlagAccepted(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	for _, strategy := range []string{"off", "balanced", "aggressive"} {
		t.Run(strategy, func(t *testing.T) {
			testDir := t.TempDir()
			configPath := writeSimpleConfig(t, testDir)

			t.Setenv("LEANPROXY_CONFIG", configPath)

			stdout, stderr, _ := runBinaryWithTimeout([]string{
				"serve",
				"--config", configPath,
				"--listen", "127.0.0.1:0",
				"--cache-strategy", strategy,
				"--dashboard-bind", "off",
				"--metrics-bind", "off",
			}, 3*time.Second)

			t.Logf("serve --cache-strategy=%s: stdout=%q stderr=%q", strategy, stdout, stderr)

			if strings.Contains(strings.ToLower(stderr), "invalid cache strategy") ||
				strings.Contains(strings.ToLower(stderr), "unknown flag") {
				t.Errorf("strategy %q rejected: %s", strategy, stderr)
			}
		})
	}
}

func mapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
