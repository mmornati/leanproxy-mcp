package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// End-to-end coverage for issue #299 (response cache): off by default (two
// identical calls reach the upstream twice), allowlisted read tools served
// from cache on a repeat call (upstream counter stays at 1), never-cache
// tools/call errors, and no vector DB file created under $HOME with the
// default config.

// startStdioProxyWithEnv is startStdioProxy with an explicit process
// environment, so a test can point $HOME at a scratch directory and assert
// nothing gets written under it.
func startStdioProxyWithEnv(t *testing.T, proxyBin, configPath string, env []string) *stdioSession {
	t.Helper()
	cmd := exec.Command(proxyBin, "server", "run", "--stdio", "--config", configPath)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr := &syncBuffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start server run --stdio: %v", err)
	}

	s := &stdioSession{t: t, cmd: cmd, stdin: stdin, lines: make(chan []byte, 16), stderr: stderr}
	go func() {
		defer close(s.lines)
		r := bufio.NewReader(stdout)
		for {
			line, err := r.ReadBytes('\n')
			if len(line) > 0 {
				s.lines <- line
			}
			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		_ = s.stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("server run --stdio stderr:\n%s", stderr.String())
		}
	})
	return s
}

func mustInitialize(t *testing.T, s *stdioSession) {
	t.Helper()
	init := s.call(1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
		"clientInfo": map[string]string{"name": "e2e", "version": "1"},
	})
	if init.Error != nil {
		t.Fatalf("initialize failed: %s", init.raw)
	}
}

// countUpstreamCalls counts how many raw lines in the fakemcp log carry
// marker, i.e. how many times the upstream actually saw that tools/call.
func countUpstreamCalls(t *testing.T, upstreamLog, marker string) int {
	t.Helper()
	data, err := os.ReadFile(upstreamLog) // #nosec G304 -- test-owned temp file
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.Contains(line, `"method":"tools/call"`) && strings.Contains(line, marker) {
			count++
		}
	}
	return count
}

func TestResponseCache_DefaultOff_SideEffectingCallReachesUpstreamTwice(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "off")
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, "")
	s := startStdioProxy(t, proxyBin, cfg)
	mustInitialize(t, s)

	marker := "marker-default-off"
	resp := s.toolCall(2, srv+"_echo", map[string]string{"marker": marker})
	if resp.Error != nil {
		t.Fatalf("first echo call failed: %s", resp.raw)
	}
	resp = s.toolCall(3, srv+"_echo", map[string]string{"marker": marker})
	if resp.Error != nil {
		t.Fatalf("second echo call failed: %s", resp.raw)
	}

	if got := countUpstreamCalls(t, upstreamLog, marker); got != 2 {
		t.Fatalf("upstream call count = %d, want 2 (response cache must be off by default)", got)
	}
}

func TestResponseCache_AllowlistedReadTool_SecondCallServedFromCache(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "on")
	toolName := srv + "_echo"
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, fmt.Sprintf(`response_cache:
  enabled: true
  tools:
    - %s
`, toolName))
	s := startStdioProxy(t, proxyBin, cfg)
	mustInitialize(t, s)

	marker := "marker-cache-on"
	first := s.toolCall(2, toolName, map[string]string{"marker": marker})
	if first.Error != nil {
		t.Fatalf("first echo call failed: %s", first.raw)
	}
	second := s.toolCall(3, toolName, map[string]string{"marker": marker})
	if second.Error != nil {
		t.Fatalf("second echo call failed: %s", second.raw)
	}
	var f, sd map[string]interface{}
	if err := json.Unmarshal([]byte(first.raw), &f); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(second.raw), &sd); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(f["result"]) != fmt.Sprint(sd["result"]) {
		t.Fatalf("cached result differs from original: %s vs %s", first.raw, second.raw)
	}

	if got := countUpstreamCalls(t, upstreamLog, marker); got != 1 {
		t.Fatalf("upstream call count = %d, want 1 (second call must be served from cache)", got)
	}

	// A different tool (not allowlisted) must still be forwarded every time.
	failMarker := "marker-fail-not-allowlisted"
	third := s.toolCall(4, srv+"_fail", map[string]string{"marker": failMarker})
	if third.Error == nil {
		t.Fatalf("expected fail tool to still error: %s", third.raw)
	}
	fourth := s.toolCall(5, srv+"_fail", map[string]string{"marker": failMarker})
	if fourth.Error == nil {
		t.Fatalf("expected fail tool to still error: %s", fourth.raw)
	}
	if got := countUpstreamCalls(t, upstreamLog, failMarker); got != 2 {
		t.Fatalf("non-allowlisted tool upstream call count = %d, want 2", got)
	}
}

func TestResponseCache_ErrorResultNeverCached(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "err")
	toolName := srv + "_fail"
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, fmt.Sprintf(`response_cache:
  enabled: true
  tools:
    - %s
`, toolName))
	s := startStdioProxy(t, proxyBin, cfg)
	mustInitialize(t, s)

	marker := "marker-error-never-cached"
	first := s.toolCall(2, toolName, map[string]string{"marker": marker})
	if first.Error == nil {
		t.Fatalf("expected the fail tool to return a JSON-RPC error: %s", first.raw)
	}
	second := s.toolCall(3, toolName, map[string]string{"marker": marker})
	if second.Error == nil {
		t.Fatalf("expected the fail tool to return a JSON-RPC error: %s", second.raw)
	}

	if got := countUpstreamCalls(t, upstreamLog, marker); got != 2 {
		t.Fatalf("upstream call count = %d, want 2 (an error response must never be cached, even for an allowlisted tool)", got)
	}
}

// TestResponseCache_NoVectorDBFileWithDefaultConfig covers issue #299's
// vector-store requirement alongside the response cache tests: with default
// config (no `cache.vector_store` block, no --embed-provider), no vector DB
// file is ever created under $HOME.
func TestResponseCache_NoVectorDBFileWithDefaultConfig(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildFirewallBinaries(t, binDir)

	dir := t.TempDir()
	tmpHome := t.TempDir()
	upstreamLog := filepath.Join(dir, "upstream.log")
	srv := uniqueServerName(t, "vec")
	cfg := writeFirewallConfig(t, dir, srv, fakeBin, upstreamLog, "")

	env := append(os.Environ(), "HOME="+tmpHome)
	s := startStdioProxyWithEnv(t, proxyBin, cfg, env)
	mustInitialize(t, s)
	resp := s.toolCall(2, srv+"_echo", map[string]string{"k": "v"})
	if resp.Error != nil {
		t.Fatalf("echo call failed: %s", resp.raw)
	}

	dbPath := filepath.Join(tmpHome, ".leanproxy", "cache", "vectors.db")
	if _, err := os.Stat(dbPath); err == nil {
		t.Fatalf("expected no vector DB file to be created at %s", dbPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("unexpected error checking %s: %v", dbPath, err)
	}
}
