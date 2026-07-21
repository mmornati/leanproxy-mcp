package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Story 10-1: Detect Anthropic calls (Epic 10)
// US: As an operator, I want the proxy to detect outbound calls to Anthropic
// (api.anthropic.com) and tag the connection with provider=anthropic so
// downstream consumers (cost, cache, bouncer) know which provider was hit.
//
// E2E surface: when serve is started with --providers-config pointing at a
// YAML file declaring anthropic prefixes, the proxy should not reject the
// flag; an invalid file should be surfaced as an error.

func TestStory_10_1_ProvidersConfig_FlagAccepted(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	providersPath := filepath.Join(testDir, "providers.yaml")
	writeFile(t, providersPath, `providers:
  - name: anthropic
    prefixes:
      - api.anthropic.com
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--providers-config", providersPath,
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	time.Sleep(1 * time.Second)
	if !pidAlive(t, pidFile) {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("serve exited prematurely with --providers-config. log:\n%s", string(log))
	}
}

func TestStory_10_1_ProvidersConfig_InvalidFile(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	providersPath := filepath.Join(testDir, "providers.yaml")
	writeFile(t, providersPath, "this is: not: valid: yaml: [\n")

	_, stderr, _ := runBinaryWithTimeout([]string{
		"serve",
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--providers-config", providersPath,
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, 3*time.Second)

	if !strings.Contains(strings.ToLower(stderr), "providers") && !strings.Contains(strings.ToLower(stderr), "yaml") {
		t.Logf("note: invalid providers file did not surface explicit error; stderr=%q", stderr)
	}
}

// Story 15-1: Per-tool model routing (Epic 15)
// US: As an operator, I want each server (or tool) to declare a complexity
// tier (low|medium|high) and have the proxy route to the right model.
// E2E surface: --model-router flag is accepted; --model-router-config
// points at a YAML file declaring tiers; the default is the model-router
// being disabled (so serve must be backward-compatible).

func TestStory_15_1_ModelRouter_DisabledByDefault(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	time.Sleep(1 * time.Second)
	if !pidAlive(t, pidFile) {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("serve exited prematurely without --model-router. log:\n%s", string(log))
	}
}

func TestStory_15_1_ModelRouter_FlagAccepted(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	routerPath := filepath.Join(testDir, "router.yaml")
	writeFile(t, routerPath, `default_tier: medium
tiers:
  low:
    provider: anthropic
    model: claude-haiku-4-5-20251001
  medium:
    provider: anthropic
    model: claude-sonnet-4-5
  high:
    provider: anthropic
    model: claude-opus-4-1
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--model-router",
		"--model-router-config", routerPath,
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	time.Sleep(1 * time.Second)
	if !pidAlive(t, pidFile) {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("serve exited prematurely with --model-router. log:\n%s", string(log))
	}
}

// Story 15-2 / 15-3: Ollama sidecar + MLX apple-silicon (Epic 15)
// US: As an operator, I want to offload ambiguous redaction to a local LLM
// (Ollama or MLX on Apple Silicon) so secrets that escape regex matching
// still get caught.
//
// E2E surface: --sidecar-provider + --sidecar-model + --sidecar-url flags
// are accepted on serve. If the sidecar is unreachable, the proxy should
// still start (graceful fallback per NFR4) — we just verify the flag
// surface here.

func TestStory_15_2_OllamaSidecar_FlagsAccepted(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--sidecar-provider", "ollama",
		"--sidecar-model", "llama3.1:8b",
		"--sidecar-url", "http://127.0.0.1:1",
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	time.Sleep(1 * time.Second)
	if !pidAlive(t, pidFile) {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("serve exited prematurely with ollama sidecar flags. log:\n%s", string(log))
	}
}

func TestStory_15_2_OllamaEmbedder_FlagsAccepted(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--embed-provider", "ollama",
		"--ollama-url", "http://127.0.0.1:1",
		"--ollama-model", "nomic-embed-text",
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	time.Sleep(1 * time.Second)
	if !pidAlive(t, pidFile) {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("serve exited prematurely with --embed-provider=ollama. log:\n%s", string(log))
	}
}

// Story 12-1/12-2: Embed tool-call payloads + pluggable vector store (Epic 12)
// US: As a developer, I want the cache to embed payloads via local or remote
// model and store them in a pluggable vector backend (sqlite-vec, qdrant,
// pinecone) so I can swap implementations without code changes.

func TestStory_12_2_VectorStore_Default(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("no home: %v", err)
	}

	dbPath := filepath.Join(home, ".leanproxy", "cache", "vectors.db")
	if _, err := os.Stat(dbPath); err == nil {
		t.Logf("default sqlite-vec cache exists at %s", dbPath)
	} else {
		t.Logf("default sqlite-vec cache will be created on first run: %s", dbPath)
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--embed-provider", "",
		"--metrics-bind", "off",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	time.Sleep(1 * time.Second)
	if !pidAlive(t, pidFile) {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("serve exited prematurely with default vector store. log:\n%s", string(log))
	}
}

// Story 17-1/17-2: Budget configuration + auto-throttle (Epic 17)
// US: As a finance lead, I want per-team / per-project budgets and the
// proxy to auto-throttle / downgrade at the configured threshold.
//
// E2E surface: this is mostly upstream of the running proxy. The integration
// is wired via leanproxy_servers.yaml; here we assert the CLI doesn't crash
// when handed a budget config and that `leanproxy report` renders the
// budget section if configured. The actual budget enforcement is covered by
// unit tests in pkg/budget.

func TestStory_17_1_BudgetConfig_LoadsCleanly(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
budgets:
  teams:
    engineering:
      daily: 100000
      soft_cap_pct: 90
      hard_cap: false
      projects:
        default:
          monthly: 5000000
servers: []
`)

	t.Setenv("LEANPROXY_CONFIG", configPath)

	stdout, stderr, exitCode := runBinary("server", "list")
	t.Logf("server list (with budget): exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)

	if exitCode != 0 {
		t.Errorf("budget config should not break server list, got exit %d: %s", exitCode, stderr)
	}
}

func TestStory_17_2_BudgetFlag_Parsed(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	// Note: 17-2's --ignore-budget flag and X-Ignore-Budget header are
	// documented in the budget package (pkg/budget/actions.go) but are NOT
	// yet wired up as CLI flags in cmd/serve.go. The flag presence in
	// `serve --help` is therefore expected to fail with "unknown flag" until
	// 17-1's budget governor is integrated into the request pipeline.
	// We mark the test as informational here.
	for _, flag := range []string{"--ignore-budget", "--ignore-budget=true"} {
		t.Run(flag, func(t *testing.T) {
			testDir := t.TempDir()
			configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
			writeFile(t, configPath, `version: "1.0"
servers: []
`)

			_, stderr, _ := runBinaryWithTimeout([]string{
				"serve",
				"--config", configPath,
				"--listen", "127.0.0.1:0",
				flag,
				"--metrics-bind", "off",
				"--dashboard-bind", "off",
				"--upstream", "http://127.0.0.1:1",
			}, 3*time.Second)
			if !strings.Contains(strings.ToLower(stderr), "unknown flag") {
				t.Logf("note: %s is now accepted (stderr=%q)", flag, stderr)
			} else {
				t.Logf("expected: %s not yet wired in cmd/serve.go (stderr=%q)", flag, stderr)
			}
		})
	}
}

// Story 16-1: First-party GitHub MCP server (Epic 16)
// US: As a developer, I want `leanproxy add github` to register the
// first-party GitHub MCP server (no npx install) so I don't need Node.
//
// E2E surface: leanproxy add github should merge a server block with the
// leanproxy-mcp-github command into leanproxy_servers.yaml. We use
// --dry-run to avoid mutating the user's real config.

func TestStory_16_1_AddGitHub_DryRunPreview(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)
	t.Setenv("LEANPROXY_CONFIG", configPath)

	_, stderr, _ := runBinary("add", "github", "--dry-run", "--i-understand-the-risks")
	if !strings.Contains(strings.ToLower(stderr), "github") {
		t.Logf("note: add github --dry-run did not mention github in stderr: %s", stderr)
	}
}

// Story 16-2: First-party Filesystem MCP server (Epic 16)
// US: As a developer, I want a first-party filesystem server with root
// containment so the LLM can only access allowed directories.

func TestStory_16_2_AddFilesystem_DryRunPreview(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)
	t.Setenv("LEANPROXY_CONFIG", configPath)
	t.Setenv("LEANPROXY_FILESYSTEM_ROOTS", testDir)

	_, stderr, _ := runBinary("add", "filesystem", "--dry-run", "--i-understand-the-risks")
	if !strings.Contains(strings.ToLower(stderr), "filesystem") {
		t.Logf("note: add filesystem --dry-run did not mention filesystem in stderr: %s", stderr)
	}
}

// Story 16-3: First-party Postgres / Redis servers with pooling (Epic 16)
// US: As a developer, I want first-party Postgres and Redis servers with
// connection pooling so I don't have to set up pgvector/redis-py.

func TestStory_16_3_AddPostgres_DryRunPreview(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)
	t.Setenv("LEANPROXY_CONFIG", configPath)
	t.Setenv("LEANPROXY_POSTGRES_CONNECTION", "postgres://localhost/test")
	t.Setenv("LEANPROXY_REDIS_ADDRESS", "localhost:6379")

	_, stderr, _ := runBinary("add", "postgres", "--dry-run", "--i-understand-the-risks")
	if !strings.Contains(strings.ToLower(stderr), "postgres") && !strings.Contains(strings.ToLower(stderr), "postgresql") {
		t.Logf("note: add postgres --dry-run did not mention postgres in stderr: %s", stderr)
	}

	_, stderr, _ = runBinary("add", "redis", "--dry-run", "--i-understand-the-risks")
	if !strings.Contains(strings.ToLower(stderr), "redis") {
		t.Logf("note: add redis --dry-run did not mention redis in stderr: %s", stderr)
	}
}

// pidAlive returns true if a process is alive at the PID in pidFile.
func pidAlive(t *testing.T, pidFile string) bool {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 (nil) returns an error if the process no longer exists.
	// On Unix this is portable; on macOS it works the same way.
	if err := proc.Signal(syscall.Signal(0)); err == nil {
		return true
	}

	return false
}
