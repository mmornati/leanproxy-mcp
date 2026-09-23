package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage for issue #311: `server run --stdio` builds each
// stdio child's environment from a minimal allowlist by default, never the
// proxy's full environment, unless a server opts in via env_passthrough,
// env or inherit_env.
//
// It reuses the fakemcp fixture and proxy binary building helpers from
// stdio_firewall_test.go (same package), and startStdioProxyWithEnv from
// response_cache_test.go.

// envToolResult is the tools/call result shape for the fakemcp "env" tool
// (content[0].text is the child's os.Environ(), one KEY=VALUE per line).
type envToolResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// runFakeEnvTool calls "<srv>_env" and returns the child's reported
// environment as a name->value map.
func runFakeEnvTool(t *testing.T, s *stdioSession, id int, srv string) map[string]string {
	t.Helper()
	resp := s.toolCall(id, srv+"_env", map[string]interface{}{})
	if resp.Error != nil {
		t.Fatalf("env tool call failed: %s", resp.raw)
	}
	var result envToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("decode env tool result: %v: %s", err, resp.raw)
	}
	if len(result.Content) == 0 {
		t.Fatalf("env tool returned no content: %s", resp.raw)
	}
	out := map[string]string{}
	for _, line := range strings.Split(result.Content[0].Text, "\n") {
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		out[name] = value
	}
	return out
}

func TestChildEnv_DefaultExcludesArbitraryParentVar(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "envd")
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, "")
	s := startStdioProxyWithEnv(t, proxyBin, cfg, append(os.Environ(), "LEANPROXY_TEST_VAR_311=super-secret-value"))

	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	resp := s.toolCall(2, "list_tools", map[string]string{"server_name": srv})
	if resp.Error != nil {
		t.Fatalf("list_tools: %s", resp.raw)
	}

	env := runFakeEnvTool(t, s, 3, srv)
	if _, ok := env["LEANPROXY_TEST_VAR_311"]; ok {
		t.Errorf("LEANPROXY_TEST_VAR_311 must not reach the child by default, got env: %v", env)
	}
	if env["PATH"] == "" {
		t.Error("PATH must always be passed to the child")
	}
}

func TestChildEnv_PassthroughAndExplicitExpansion(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "enve")
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, `      env_passthrough: ["LEANPROXY_TEST_VAR_311"]
      env: ["BAR=${LEANPROXY_TEST_VAR_311}"]
`)
	s := startStdioProxyWithEnv(t, proxyBin, cfg, append(os.Environ(), "LEANPROXY_TEST_VAR_311=super-secret-value"))

	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	resp := s.toolCall(2, "list_tools", map[string]string{"server_name": srv})
	if resp.Error != nil {
		t.Fatalf("list_tools: %s", resp.raw)
	}

	env := runFakeEnvTool(t, s, 3, srv)
	if env["LEANPROXY_TEST_VAR_311"] != "super-secret-value" {
		t.Errorf("env_passthrough did not pass LEANPROXY_TEST_VAR_311, got env: %v", env)
	}
	if env["BAR"] != "super-secret-value" {
		t.Errorf("env \"${VAR}\" expansion did not set BAR, got env: %v", env)
	}
}

// TestChildEnv_InheritEnvTrueRestoresFullInheritance verifies that
// inherit_env: true restores the pre-#311 full-inheritance behavior and
// logs a warning at start.
func TestChildEnv_InheritEnvTrueRestoresFullInheritance(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "envi")
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, `      inherit_env: true
`)
	s := startStdioProxyWithEnv(t, proxyBin, cfg, append(os.Environ(), "LEANPROXY_TEST_VAR_311=super-secret-value"))

	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	resp := s.toolCall(2, "list_tools", map[string]string{"server_name": srv})
	if resp.Error != nil {
		t.Fatalf("list_tools: %s", resp.raw)
	}

	env := runFakeEnvTool(t, s, 3, srv)
	if env["LEANPROXY_TEST_VAR_311"] != "super-secret-value" {
		t.Errorf("inherit_env: true should pass through the full parent env, got: %v", env)
	}
	if !strings.Contains(s.stderr.String(), "inherit_env") {
		t.Errorf("expected a startup warning mentioning inherit_env, got stderr: %s", s.stderr.String())
	}
}

// TestChildEnv_MissingExpansionVarFailsStart verifies that an explicit env
// entry referencing an unset parent variable fails the server's start with
// a clear error, rather than silently spawning without it.
func TestChildEnv_MissingExpansionVarFailsStart(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "envm")
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, `      env: ["BAR=${DEFINITELY_NOT_SET_311}"]
`)
	s := startStdioProxy(t, proxyBin, cfg)

	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
	// The server is started eagerly at proxy boot, before `initialize` is
	// even answered, and fails clearly because of the unresolved
	// reference; list_tools then reports the server as unavailable rather
	// than the proxy itself failing.
	resp := s.toolCall(2, "list_tools", map[string]string{"server_name": srv})
	if resp.Error != nil {
		t.Fatalf("list_tools: %s", resp.raw)
	}
	if !strings.Contains(resp.raw, "not found") {
		t.Errorf("expected list_tools to report the server as unavailable, got: %s", resp.raw)
	}
	stderrText := s.stderr.String()
	if !strings.Contains(stderrText, srv) || !strings.Contains(stderrText, "DEFINITELY_NOT_SET_311") {
		t.Errorf("expected startup error naming the server and the missing variable, got stderr: %s", stderrText)
	}
}
