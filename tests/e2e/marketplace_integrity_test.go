package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Issue #313 — Marketplace supply-chain integrity: official registry,
// version pinning, honest trust score, confirm-before-enable.
//
// These E2E tests drive the real built binary against a local fake
// "official MCP Registry" (httptest.Server), never the real network. The
// binary is pointed at it via LEANPROXY_MCP_REGISTRY_URL.

// fakeOfficialRegistry serves one npm-backed server ("weather") on
// GET /v0/servers, matching the official registry's v0 schema.
func fakeOfficialRegistry(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v0/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"servers": [
				{
					"name": "weather",
					"description": "Weather lookup MCP server",
					"version": "1.4.0",
					"license": "MIT",
					"packages": [
						{
							"registryType": "npm",
							"identifier": "@example/weather-mcp",
							"version": "1.4.0",
							"environmentVariables": [
								{"name": "WEATHER_API_KEY", "isRequired": true, "isSecret": true}
							]
						}
					],
					"_meta": {
						"io.modelcontextprotocol.registry/official": {"isLatest": true, "isVerified": true, "status": "active"}
					}
				}
			],
			"metadata": {}
		}`))
	})
	return httptest.NewServer(mux)
}

func TestStory_313_MarketplaceSync_UsesConfiguredOfficialRegistry(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	registry := fakeOfficialRegistry(t)
	defer registry.Close()

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LEANPROXY_MCP_REGISTRY_URL", registry.URL)

	stdout, stderr, exitCode := runBinary("marketplace", "sync")
	t.Logf("marketplace sync: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("marketplace sync against fake registry failed: %s", stderr)
	}

	cachePath := filepath.Join(home, ".leanproxy", "registry", "index.json")
	data, err := os.ReadFile(cachePath) // #nosec G304 -- fixed path under a t.TempDir() HOME
	if err != nil {
		t.Fatalf("expected registry cache at %s: %v", cachePath, err)
	}
	if !strings.Contains(string(data), "weather") {
		t.Errorf("cache should contain the fake registry's entry, got: %s", data)
	}
}

func TestStory_313_Add_PinsVersionAndDefaultsDisabled(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	registry := fakeOfficialRegistry(t)
	defer registry.Close()

	home := t.TempDir()
	configPath := filepath.Join(home, "leanproxy_servers.yaml")
	writeFile(t, configPath, "version: \"1.0\"\nservers: []\n")

	t.Setenv("HOME", home)
	t.Setenv("LEANPROXY_CONFIG", configPath)
	t.Setenv("LEANPROXY_MCP_REGISTRY_URL", registry.URL)

	if _, stderr, exitCode := runBinary("marketplace", "sync"); exitCode != 0 {
		t.Fatalf("marketplace sync failed: %s", stderr)
	}

	// Without --yes and with non-interactive stdin (the default for a
	// binary run under `go test`), the install must proceed but leave the
	// server disabled.
	stdout, stderr, exitCode := runBinary("add", "weather")
	t.Logf("add weather: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("add weather failed: %s", stderr)
	}

	data, err := os.ReadFile(configPath) // #nosec G304 -- fixed path under a t.TempDir() HOME
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)

	if !strings.Contains(config, "@example/weather-mcp@1.4.0") {
		t.Errorf("expected the exact pinned version in the written command, got:\n%s", config)
	}
	if !strings.Contains(config, "enabled: false") {
		t.Errorf("expected the unconfirmed install to be written disabled, got:\n%s", config)
	}
	if !strings.Contains(config, "installed_from:") || !strings.Contains(config, "registry: official") {
		t.Errorf("expected installed_from provenance recording the official registry, got:\n%s", config)
	}
	// The registry only publishes the variable *name*; a value must never
	// be synthesized or leaked into the written config — the written
	// entry must be exactly "WEATHER_API_KEY=" (empty value), never
	// "WEATHER_API_KEY=<anything>".
	if !strings.Contains(config, "WEATHER_API_KEY=") {
		t.Errorf("expected the env var name to be present, got:\n%s", config)
	}
	for _, line := range strings.Split(config, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "WEATHER_API_KEY=") && trimmed != "- WEATHER_API_KEY=" {
			t.Errorf("expected an empty value for WEATHER_API_KEY, got line: %q", trimmed)
		}
	}

	// The preview must show the command and env var *names*, never any
	// value (there is none to leak here, but the wording itself must
	// promise that — this guards the acceptance criterion at the text
	// level too).
	if !strings.Contains(stdout, "About to install") {
		t.Errorf("expected an install preview, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "WEATHER_API_KEY") {
		t.Errorf("expected the env var name in the preview, got stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, "values are never printed") {
		t.Errorf("expected the preview to say values are never printed, got stdout:\n%s", stdout)
	}
}

func TestStory_313_Add_YesFlagEnablesServer(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	registry := fakeOfficialRegistry(t)
	defer registry.Close()

	home := t.TempDir()
	configPath := filepath.Join(home, "leanproxy_servers.yaml")
	writeFile(t, configPath, "version: \"1.0\"\nservers: []\n")

	t.Setenv("HOME", home)
	t.Setenv("LEANPROXY_CONFIG", configPath)
	t.Setenv("LEANPROXY_MCP_REGISTRY_URL", registry.URL)

	if _, stderr, exitCode := runBinary("marketplace", "sync"); exitCode != 0 {
		t.Fatalf("marketplace sync failed: %s", stderr)
	}

	if _, stderr, exitCode := runBinary("add", "weather", "--yes"); exitCode != 0 {
		t.Fatalf("add --yes failed: %s", stderr)
	}

	data, err := os.ReadFile(configPath) // #nosec G304 -- fixed path under a t.TempDir() HOME
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), "enabled: true") {
		t.Errorf("expected --yes install to be enabled, got:\n%s", data)
	}
}

func TestStory_313_MarketplaceOutdatedAndUpdate(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	registry := fakeOfficialRegistry(t)
	defer registry.Close()

	home := t.TempDir()
	configPath := filepath.Join(home, "leanproxy_servers.yaml")
	writeFile(t, configPath, "version: \"1.0\"\nservers: []\n")

	t.Setenv("HOME", home)
	t.Setenv("LEANPROXY_CONFIG", configPath)
	t.Setenv("LEANPROXY_MCP_REGISTRY_URL", registry.URL)

	if _, stderr, exitCode := runBinary("marketplace", "sync"); exitCode != 0 {
		t.Fatalf("marketplace sync failed: %s", stderr)
	}
	if _, stderr, exitCode := runBinary("add", "weather", "--yes"); exitCode != 0 {
		t.Fatalf("add --yes failed: %s", stderr)
	}

	// Hand-edit the installed_from version down, to simulate the registry
	// having moved on since this server was installed.
	data, err := os.ReadFile(configPath) // #nosec G304 -- fixed path under a t.TempDir() HOME
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(data), "version: 1.4.0", "version: 1.0.0", 1)
	writeFile(t, configPath, edited)

	stdout, stderr, exitCode := runBinary("marketplace", "outdated")
	t.Logf("marketplace outdated: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("marketplace outdated failed: %s", stderr)
	}
	if !strings.Contains(stdout, "weather") || !strings.Contains(stdout, "1.0.0") || !strings.Contains(stdout, "1.4.0") {
		t.Errorf("expected outdated to show the version drift, got:\n%s", stdout)
	}

	stdout, stderr, exitCode = runBinary("marketplace", "update", "weather", "--yes")
	t.Logf("marketplace update: exit=%d stdout=%q stderr=%q", exitCode, stdout, stderr)
	if exitCode != 0 {
		t.Fatalf("marketplace update failed: %s", stderr)
	}

	data, err = os.ReadFile(configPath) // #nosec G304 -- fixed path under a t.TempDir() HOME
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "@example/weather-mcp@1.4.0") {
		t.Errorf("expected the config to be updated to the current version, got:\n%s", data)
	}
	if !strings.Contains(string(data), "enabled: true") {
		t.Errorf("update should preserve the previous enabled state, got:\n%s", data)
	}
}
