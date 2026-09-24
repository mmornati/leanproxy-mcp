package e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// End-to-end coverage for `doctor security`'s OWASP-MCP-mapped report
// (issue #323): the real binary, against fixture configs, in every output
// format (human, --json, --markdown).

func runDoctorSecurity(t *testing.T, home, cfgPath string, extraArgs ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"--config", cfgPath, "doctor", "security"}, extraArgs...)
	cmd := exec.Command(filepath.Join(wd, "leanproxy-mcp"), args...)
	cmd.Dir = wd
	cmd.Env = append(os.Environ(), "HOME="+home)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err = cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run doctor security: %v", err)
		}
	}
	return out.String(), errOut.String(), code
}

func writeFixtureConfig(t *testing.T, dir, contents string) string {
	t.Helper()
	path := filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const e2eHardenedFixture = `version: "1.0"
servers:
  - name: demo
    transport: stdio
    enabled: true
    stdio:
      command: npx
      args: ["-y", "demo-pkg@1.2.3"]
      sandbox:
        runtime: docker
        network: none
injection:
  enabled: true
  threshold: 70
  action: block
bouncer:
  enabled: true
  entropy_detection: true
policy:
  default: deny
  unknown_tools: deny
security:
  tool_pinning:
    mode: block
response:
  enabled: true
telemetry:
  enabled: true
  otlp:
    endpoint: "http://localhost:4318"
exposure:
  mode: router
`

const e2eInsecureFixture = `version: "1.0"
servers:
  - name: demo
    transport: stdio
    enabled: true
    stdio:
      command: sh
      args: ["-c", "npx -y demo-pkg"]
      inherit_env: true
bouncer:
  enabled: false
`

func TestE2E_DoctorSecurity_HardenedFixture(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}
	home := t.TempDir()
	cfgPath := writeFixtureConfig(t, t.TempDir(), e2eHardenedFixture)

	out, stderr, code := runDoctorSecurity(t, home, cfgPath)
	if code != 0 {
		t.Fatalf("hardened fixture: exit=%d stdout=%s stderr=%s", code, out, stderr)
	}
	if strings.Contains(out, "\u274c") {
		t.Fatalf("hardened fixture must report no \u274c:\n%s", out)
	}
	for _, want := range []string{"MCP01", "MCP06", "MCP09", "MCP10", "Summary:"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestE2E_DoctorSecurity_InsecureFixture(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}
	home := t.TempDir()
	cfgPath := writeFixtureConfig(t, t.TempDir(), e2eInsecureFixture)

	out, stderr, code := runDoctorSecurity(t, home, cfgPath)
	if code != 1 {
		t.Fatalf("insecure fixture: exit=%d, want 1\nstdout=%s\nstderr=%s", code, out, stderr)
	}
	for _, want := range []string{
		"\u274c Response redaction (bouncer)",
		"\u274c Servers with inherit_env: true",
		"\u274c Stdio commands that invoke a shell",
		"\u274c Prompt-injection guard",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestE2E_DoctorSecurity_JSON(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}
	home := t.TempDir()
	cfgPath := writeFixtureConfig(t, t.TempDir(), e2eInsecureFixture)

	out, stderr, code := runDoctorSecurity(t, home, cfgPath, "--json")
	if code != 1 {
		t.Fatalf("exit=%d, want 1\nstdout=%s\nstderr=%s", code, out, stderr)
	}
	var report struct {
		Schema     string `json:"schema"`
		Categories []struct {
			ID     string `json:"id"`
			Checks []struct {
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"categories"`
		Summary struct {
			OK, Warn, Fail, Info int
		} `json:"summary"`
	}
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if report.Schema != "leanproxy.doctor.security/v1" {
		t.Fatalf("schema = %q", report.Schema)
	}
	if report.Summary.Fail == 0 {
		t.Fatalf("summary.fail = 0, want > 0 for the insecure fixture: %+v", report.Summary)
	}
	if len(report.Categories) != 10 {
		t.Fatalf("categories = %d, want 10", len(report.Categories))
	}
}

func TestE2E_DoctorSecurity_Markdown(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}
	home := t.TempDir()
	cfgPath := writeFixtureConfig(t, t.TempDir(), e2eHardenedFixture)

	out, stderr, code := runDoctorSecurity(t, home, cfgPath, "--markdown")
	if code != 0 {
		t.Fatalf("exit=%d\nstdout=%s\nstderr=%s", code, out, stderr)
	}
	for _, want := range []string{"| Status | Check | Evidence | Fix |", "## MCP01", "**Summary:**"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown output lacks %q:\n%s", want, out)
		}
	}
}

// TestE2E_DoctorSecurity_NeverPrintsSecrets: a server with a literal secret
// value in its env: block must never see that value echoed anywhere in
// doctor security's output, in any format.
func TestE2E_DoctorSecurity_NeverPrintsSecrets(t *testing.T) {
	if !binaryAvailable() {
		t.Skip("Binary not in tests/e2e/")
	}
	secret := "sk-" + "e2e-ghost-token-do-not-print-98765"
	home := t.TempDir()
	cfgPath := writeFixtureConfig(t, t.TempDir(), `version: "1.0"
servers:
  - name: demo
    transport: stdio
    enabled: true
    stdio:
      command: echo
      args: ["hi"]
      env:
        - "API_KEY=`+secret+`"
`)
	for _, args := range [][]string{{}, {"--json"}, {"--markdown"}} {
		out, _, _ := runDoctorSecurity(t, home, cfgPath, args...)
		if strings.Contains(out, secret) {
			t.Fatalf("args=%v: output must never contain the secret value:\n%s", args, out)
		}
	}
}
