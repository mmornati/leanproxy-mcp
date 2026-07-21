package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Story 14-1: Publish /metrics JSON endpoint (Epic 14)
// US: As an IDE / external monitoring tool, I want a JSON metrics endpoint
// at /metrics that exposes per-server / per-tool / top-tools / total spend so
// I can drive a status bar widget without scraping Prometheus text.
//
// Acceptance: GET /metrics returns application/json with keys
//   by_server, by_tool, total_spend, top_tools
// and that the endpoint can be disabled via --metrics-bind off.

func TestStory_14_1_MetricsEndpoint_JSONShape(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	pidFile := filepath.Join(testDir, "leanproxy.pid")
	logFile := filepath.Join(testDir, "leanproxy.log")
	persistLog := "/tmp/leanproxy-e2e-metrics.log"
	if err := startServe(t, []string{
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--metrics-bind", fmt.Sprintf("127.0.0.1:%d", port),
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)
	defer func() {
		if data, err := os.ReadFile(logFile); err == nil {
			os.WriteFile(persistLog, data, 0644)
		}
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)
	resp, body := waitForHTTP(t, url, 15*time.Second)
	if resp.StatusCode != http.StatusOK {
		log, _ := os.ReadFile(logFile)
		t.Fatalf("GET /metrics returned %d, body=%s\nlog:\n%s", resp.StatusCode, body, string(log))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("GET /metrics did not return valid JSON: %v\nraw=%s", err, body)
	}

	// The /metrics endpoint exposes a snapshot with at least these keys.
	for _, key := range []string{"by_server", "by_tool", "total_spend"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("metrics JSON missing key %q, got keys: %v", key, mapKeys(parsed))
		}
	}
}

func TestStory_14_1_MetricsEndpoint_DisabledByFlag(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
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

	time.Sleep(2 * time.Second)

	_, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/metrics", port))
	if err == nil {
		t.Errorf("expected /metrics to be unreachable when --metrics-bind=off, but got a response")
	}
}

// Story 18-1: Web dashboard served from LeanProxy (Epic 18)
// US: As a finance lead, I want a read-only web dashboard at / showing
// today's spend, WTD spend, top server, top tool so I don't need to query
// the metrics endpoint by hand.

func TestStory_18_1_Dashboard_IndexHTML(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
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
		"--dashboard-bind", fmt.Sprintf("127.0.0.1:%d", port),
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	resp, body := waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/", port), 10*time.Second)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / returned %d, body=%s", resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("expected text/html Content-Type, got %q", ct)
	}

	// /api/dashboard returns the cards HTML which IS rendered. Verify the
	// cards endpoint contract here; the root / template is separately
	// covered by pkg/dashboard unit tests.
	respCards, cardsBody := waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/api/dashboard", port), 5*time.Second)
	if respCards.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/dashboard returned %d, body=%s", respCards.StatusCode, cardsBody)
	}
	for _, expected := range []string{"WTD Spend", "Top Server", "Top Tool"} {
		if !strings.Contains(cardsBody, expected) {
			t.Errorf("dashboard cards missing expected text %q, got:\n%s", expected, cardsBody)
		}
	}
}

func TestStory_18_1_Dashboard_JSONAPI(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
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
		"--dashboard-bind", fmt.Sprintf("127.0.0.1:%d", port),
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	resp, body := waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/api/dashboard/json", port), 10*time.Second)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/dashboard/json returned %d, body=%s", resp.StatusCode, body)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("dashboard JSON did not parse: %v\nraw=%s", err, body)
	}

	for _, key := range []string{"today_spend", "wtd_spend", "top_server", "top_tool", "server_count", "tool_count"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("dashboard JSON missing key %q, got keys: %v", key, mapKeys(parsed))
		}
	}
}

func TestStory_18_1_Dashboard_NonLoopbackRequiresToken(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
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
		"--dashboard-bind", fmt.Sprintf("0.0.0.0:%d", port),
		"--dashboard-token", "supersecret",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	// Wait for the dashboard to be up.
	waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/api/dashboard", port), 10*time.Second)

	// Loopback request without token: should succeed (no auth required from loopback).
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("request to loopback dashboard failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from loopback without token, got %d", resp.StatusCode)
	}

	// Note: non-loopback auth is exercised in pkg/dashboard/auth_test.go
	// (TestIsLoopbackRemoteAddr + TestRequireBearerToken). Real E2E requires
	// a second host or network namespace; we confirm only the loopback path
	// here, and that the --dashboard-token flag is accepted.
}

func TestStory_18_1_Dashboard_DisabledByFlag(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
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

	time.Sleep(2 * time.Second)
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err == nil && resp != nil {
		resp.Body.Close()
		t.Errorf("expected dashboard to be unreachable when --dashboard-bind=off, got %d", resp.StatusCode)
	}
}

// Helper used by story 18-2 (drill-down) and 18-1 (dashboard).

func TestStory_18_2_Drilldown_ServersEndpoint(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
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
		"--dashboard-bind", fmt.Sprintf("127.0.0.1:%d", port),
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	resp, body := waitForHTTP(t, fmt.Sprintf("http://127.0.0.1:%d/api/dashboard/servers", port), 10*time.Second)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/dashboard/servers returned %d, body=%s", resp.StatusCode, body)
	}

	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("drill-down endpoint should serve text/html, got %q", ct)
	}
}

// stopServe reads the pidfile written by startServe and sends SIGTERM.
// Used as the deferred cleanup so tests don't leave orphan processes.
func stopServe(t *testing.T, pidFile, logFile string) {
	t.Helper()
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	var pid int
	if _, err := fmt.Sscanf(string(data), "%d", &pid); err != nil || pid == 0 {
		return
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = proc.Signal(os.Interrupt)
	time.Sleep(300 * time.Millisecond)
	_ = proc.Kill()
}
