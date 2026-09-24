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
// Acceptance: GET /metrics returns application/json with this process's
// live counters (telemetry) and the usage store's today / week-to-date
// windows (usage), and the endpoint can be disabled via --metrics-bind off.
// The old by_server / by_tool / total_spend / top_5_expensive_tools keys,
// fed by a cost tracker nothing called, are gone.

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
	for _, key := range []string{"telemetry", "usage"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("metrics JSON missing key %q, got keys: %v", key, mapKeys(parsed))
		}
	}
	for _, key := range []string{"by_server", "by_tool", "total_spend", "top_5_expensive_tools"} {
		if _, ok := parsed[key]; ok {
			t.Errorf("metrics JSON still has the removed key %q", key)
		}
	}
	usage, _ := parsed["usage"].(map[string]interface{})
	for _, key := range []string{"estimator", "today", "week"} {
		if _, ok := usage[key]; !ok {
			t.Errorf("metrics usage section missing key %q, got keys: %v", key, mapKeys(usage))
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
	for _, expected := range []string{"Saved today", "Saved this week", "Top server today", "Top tool today"} {
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

	for _, key := range []string{"estimator", "today", "week"} {
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

	// Wait for the dashboard to be up (using the token: issue #316 removed
	// the loopback bypass, so an unauthenticated request no longer works
	// even from 127.0.0.1 once a token is configured).
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/dashboard", port), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer supersecret")
	waitForAuthedHTTP(t, req, 10*time.Second)

	// Loopback request without a token: issue #316 requires the token from
	// every client once one is configured, loopback included.
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("request to loopback dashboard failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 from loopback without token, got %d", resp.StatusCode)
	}

	// Loopback request with the token in the Authorization header succeeds.
	req2, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req2.Header.Set("Authorization", "Bearer supersecret")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("authenticated request to loopback dashboard failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 from loopback with token, got %d", resp2.StatusCode)
	}

	// A forged Host header is rejected even with a valid token.
	req3, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/", port), nil)
	req3.Header.Set("Authorization", "Bearer supersecret")
	req3.Host = "evil.example"
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatalf("request with forged Host header failed: %v", err)
	}
	defer resp3.Body.Close()
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for Host: evil.example, got %d", resp3.StatusCode)
	}
}

// waitForAuthedHTTP polls req (cloned per attempt) until it returns 2xx or
// timeout elapses. Like waitForHTTP, but for requests that need headers
// (e.g. Authorization) that http.Get cannot set.
func waitForAuthedHTTP(t *testing.T, req *http.Request, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		attempt := req.Clone(req.Context())
		resp, err := http.DefaultClient.Do(attempt)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s after %s: %v", req.URL, timeout, lastErr)
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

// Story 20.12 (issue #316): dashboard & metrics hardening. A non-loopback
// bind without a token refuses to start the whole `serve` process, rather
// than just skipping the endpoint with a warning.

func TestStory_20_12_Dashboard_NonLoopbackBindRefusesWithoutToken(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	stdout, stderr, exitCode := runBinaryWithTimeout([]string{
		"serve",
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--metrics-bind", "off",
		"--dashboard-bind", fmt.Sprintf("0.0.0.0:%d", port),
	}, 10*time.Second)

	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code, got 0. stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "without a token") {
		t.Errorf("expected a message about the missing token, got stdout=%s stderr=%s", stdout, stderr)
	}
}

func TestStory_20_12_Metrics_NonLoopbackBindRefusesWithoutToken(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}

	port := freePort(t)
	testDir := t.TempDir()
	configPath := filepath.Join(testDir, "leanproxy_servers.yaml")
	writeFile(t, configPath, `version: "1.0"
servers: []
`)

	stdout, stderr, exitCode := runBinaryWithTimeout([]string{
		"serve",
		"--config", configPath,
		"--listen", "127.0.0.1:0",
		"--dashboard-bind", "off",
		"--metrics-bind", fmt.Sprintf("0.0.0.0:%d", port),
	}, 10*time.Second)

	if exitCode == 0 {
		t.Fatalf("expected non-zero exit code, got 0. stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, "without a token") {
		t.Errorf("expected a message about the missing token, got stdout=%s stderr=%s", stdout, stderr)
	}
}

func TestStory_20_12_Metrics_TokenRequired(t *testing.T) {
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
		"--metrics-bind", fmt.Sprintf("127.0.0.1:%d", port),
		"--metrics-token", "metricssecret",
		"--dashboard-bind", "off",
		"--upstream", "http://127.0.0.1:1",
	}, pidFile, logFile); err != nil {
		t.Fatalf("failed to start serve: %v", err)
	}
	defer stopServe(t, pidFile, logFile)

	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", port)

	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer metricssecret")
	waitForAuthedHTTP(t, req, 10*time.Second)

	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /metrics failed: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status without token = %d, want 401", resp.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodGet, url, nil)
	req2.Header.Set("Authorization", "Bearer metricssecret")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /metrics with token failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("status with token = %d, want 200", resp2.StatusCode)
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
