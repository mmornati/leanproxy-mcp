package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDoctorSecurityConfig writes cfg to a fresh leanproxy_servers.yaml
// under a fresh isolated $HOME and returns its path. GlobalConfigPath is
// reset to "" so runSecurityDiagnostic falls back to
// ~/.config/leanproxy_servers.yaml, exactly as a real invocation does.
func writeDoctorSecurityConfig(t *testing.T, cfg string) (home, cfgPath string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	origPath := GlobalConfigPath
	GlobalConfigPath = ""
	t.Cleanup(func() { GlobalConfigPath = origPath })

	dir := filepath.Join(home, ".config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath = filepath.Join(dir, "leanproxy_servers.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, cfgPath
}

// insecureFixture has every category's ❌-worthy setting: redaction off, a
// server that inherits the full environment and invokes a shell, no
// injection guard, and a non-default injection config (regression for the
// carry-over bug below).
const insecureFixture = `version: "1.0"
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
injection:
  enabled: false
  threshold: 33
  request_policies:
    - min_risk: 1
      max_risk: 100
      action: log
`

// hardenedFixture sets every category to its safest configuration.
const hardenedFixture = `version: "1.0"
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
  rules:
    - match: "demo.delete_*"
      action: deny
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

// TestDoctorSecurity_HardenedFixture_AllOK is the golden-file-style
// acceptance criterion: a hardened fixture produces no ❌, exit code 0.
func TestDoctorSecurity_HardenedFixture_AllOK(t *testing.T) {
	writeDoctorSecurityConfig(t, hardenedFixture)

	var buf bytes.Buffer
	code := runSecurityDiagnostic(&buf, false, false)
	out := buf.String()

	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (hardened fixture):\n%s", code, out)
	}
	if strings.Contains(out, "\u274c") {
		t.Fatalf("hardened fixture must have no \u274c:\n%s", out)
	}
}

// TestDoctorSecurity_InsecureFixture_Fails is the golden-file-style
// acceptance criterion: an insecure fixture produces the expected ❌
// items and a non-zero exit code.
func TestDoctorSecurity_InsecureFixture_Fails(t *testing.T) {
	writeDoctorSecurityConfig(t, insecureFixture)

	var buf bytes.Buffer
	code := runSecurityDiagnostic(&buf, false, false)
	out := buf.String()

	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (insecure fixture):\n%s", code, out)
	}
	for _, want := range []string{
		"\u274c Response redaction (bouncer)",
		"\u274c Servers with inherit_env: true",
		"\u274c Stdio commands that invoke a shell",
		"\u274c Prompt-injection guard",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("insecure fixture output lacks %q:\n%s", want, out)
		}
	}
}

// TestDoctorSecurity_InjectionConfig_CarryOverBugFix regression-tests the
// carry-over bug (reported in #315's PR): `doctor security` used to decode
// the WHOLE leanproxy_servers.yaml file as if it were the injection block
// (injection.LoadConfigFile(cfgPath) on the proxy config path), so it
// always showed the default risk bands (80-100 block, 50-79 quarantine,
// 1-49 log) even when injection.request_policies configured something
// else entirely. With a non-default injection config, the report must
// show the configured bands, not the defaults.
func TestDoctorSecurity_InjectionConfig_CarryOverBugFix(t *testing.T) {
	writeDoctorSecurityConfig(t, `version: "1.0"
injection:
  enabled: true
  threshold: 90
  request_policies:
    - min_risk: 1
      max_risk: 100
      action: log
`)

	var buf bytes.Buffer
	runSecurityDiagnostic(&buf, false, false)
	out := buf.String()

	if strings.Contains(out, "No injection: block configured") {
		t.Fatalf("an injection: block was configured; must not report it as absent:\n%s", out)
	}
	if !strings.Contains(out, "Risk   1-100 -> log") {
		t.Fatalf("must show the configured request_policies (1-100 -> log), not the defaults:\n%s", out)
	}
	if strings.Contains(out, "Risk  80-100 -> block") {
		t.Fatalf("must not fall back to the default rules when request_policies is configured:\n%s", out)
	}
}

// TestDoctorSecurity_NeverPrintsSecrets: with a live secret-looking value
// in the environment a pass-through server would read, and a custom
// bouncer pattern, `doctor security`'s output (human, JSON and Markdown)
// must never contain the value — only names, counts and states.
func TestDoctorSecurity_NeverPrintsSecrets(t *testing.T) {
	const secretValue = "sk-" + "ghost-token-do-not-print-1234567890"
	writeDoctorSecurityConfig(t, `version: "1.0"
servers:
  - name: demo
    transport: stdio
    enabled: true
    stdio:
      command: echo
      args: ["hi"]
      env:
        - "API_KEY=`+secretValue+`"
`)
	t.Setenv("SOME_UPSTREAM_SECRET", secretValue)

	for _, mode := range []struct {
		name        string
		jsonOut     bool
		markdownOut bool
	}{
		{"human", false, false},
		{"json", true, false},
		{"markdown", false, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			var buf bytes.Buffer
			runSecurityDiagnostic(&buf, mode.jsonOut, mode.markdownOut)
			out := buf.String()
			if strings.Contains(out, secretValue) {
				t.Fatalf("doctor security (%s) must never print a secret value, got:\n%s", mode.name, out)
			}
			if mode.jsonOut {
				var parsed map[string]interface{}
				if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
					t.Fatalf("invalid JSON: %v\n%s", err, out)
				}
			}
		})
	}
}

// TestDoctorSecurity_JSONSchema checks the documented, versioned JSON
// schema (leanproxy.doctor.security/v1): top-level shape, category/check
// shape and the summary counts.
func TestDoctorSecurity_JSONSchema(t *testing.T) {
	writeDoctorSecurityConfig(t, hardenedFixture)

	var buf bytes.Buffer
	code := runSecurityDiagnostic(&buf, true, false)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0:\n%s", code, buf.String())
	}

	var report SecurityReport
	if err := json.Unmarshal(buf.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, buf.String())
	}
	if report.Schema != doctorSecuritySchema {
		t.Fatalf("schema = %q, want %q", report.Schema, doctorSecuritySchema)
	}
	if len(report.Categories) != 10 {
		t.Fatalf("categories = %d, want 10 (MCP01-MCP10)", len(report.Categories))
	}
	wantIDs := []string{"MCP01", "MCP02", "MCP03", "MCP04", "MCP05", "MCP06", "MCP07", "MCP08", "MCP09", "MCP10"}
	for i, want := range wantIDs {
		if report.Categories[i].ID != want {
			t.Errorf("categories[%d].ID = %q, want %q", i, report.Categories[i].ID, want)
		}
		if len(report.Categories[i].Checks) == 0 {
			t.Errorf("categories[%d] (%s) has no checks", i, want)
		}
	}
	total := report.Summary.OK + report.Summary.Warn + report.Summary.Fail + report.Summary.Info
	var checkCount int
	for _, c := range report.Categories {
		checkCount += len(c.Checks)
	}
	if total != checkCount {
		t.Fatalf("summary total %d != check count %d", total, checkCount)
	}
}

// TestDoctorSecurity_ShadowServer: a server configured directly in a
// fixture Claude Desktop config, and never imported into LeanProxy, is
// reported by the MCP09 shadow-server check.
func TestDoctorSecurity_ShadowServer(t *testing.T) {
	home, _ := writeDoctorSecurityConfig(t, `version: "1.0"
servers: []
`)
	cfg := `{"mcpServers":{"shadow-fs":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	runSecurityDiagnostic(&buf, false, false)
	out := buf.String()
	if !strings.Contains(out, "shadow-fs") {
		t.Fatalf("expected the shadow server to be reported:\n%s", out)
	}
}
