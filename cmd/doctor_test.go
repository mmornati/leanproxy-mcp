package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	_ = w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// TestRunEnvDiagnosticNamesOnly verifies `doctor env` lists, per stdio
// server, the passed and dropped environment variable names, and never a
// value (#311).
func TestRunEnvDiagnosticNamesOnly(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	cfg := `version: "1.0"
servers:
  - name: demo
    transport: stdio
    enabled: true
    stdio:
      command: echo
      args: ["hi"]
      env_passthrough: ["DOCTOR_ENV_TEST_311"]
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	origPath := GlobalConfigPath
	GlobalConfigPath = cfgPath
	defer func() { GlobalConfigPath = origPath }()

	t.Setenv("DOCTOR_ENV_TEST_311", "a-secret-value-311")

	out := captureStdout(t, runEnvDiagnostic)

	if !strings.Contains(out, "demo") {
		t.Errorf("expected server name in output, got: %s", out)
	}
	if !strings.Contains(out, "DOCTOR_ENV_TEST_311") {
		t.Errorf("expected passed variable name in output, got: %s", out)
	}
	if strings.Contains(out, "a-secret-value-311") {
		t.Errorf("doctor env must never print variable values, got: %s", out)
	}
	if !strings.Contains(out, "Dropped") || !strings.Contains(out, "Passed") {
		t.Errorf("expected Passed/Dropped sections, got: %s", out)
	}
}

// TestRunSandboxDiagnostic verifies `doctor sandbox` reports, per stdio
// server, whether it is sandboxed and, when it is, whether the configured
// runtime binary is available (#312).
func TestRunSandboxDiagnostic(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "leanproxy_servers.yaml")
	cfg := `version: "1.0"
servers:
  - name: plain
    transport: stdio
    enabled: true
    stdio:
      command: echo
      args: ["hi"]
  - name: sandboxed
    transport: stdio
    enabled: true
    stdio:
      command: npx
      args: ["-y", "some-pkg"]
      sandbox:
        runtime: docker
        network: none
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	origPath := GlobalConfigPath
	GlobalConfigPath = cfgPath
	defer func() { GlobalConfigPath = origPath }()

	out := captureStdout(t, runSandboxDiagnostic)

	if !strings.Contains(out, "plain") || !strings.Contains(out, "unsandboxed") {
		t.Errorf("expected plain server reported as unsandboxed, got: %s", out)
	}
	if !strings.Contains(out, "sandboxed") || !strings.Contains(out, "runtime=docker") {
		t.Errorf("expected sandboxed server with runtime=docker, got: %s", out)
	}
	if !strings.Contains(out, "image=node:22-alpine") {
		t.Errorf("expected inferred image for npx, got: %s", out)
	}
	if !strings.Contains(out, "network=none") {
		t.Errorf("expected network=none, got: %s", out)
	}
}
