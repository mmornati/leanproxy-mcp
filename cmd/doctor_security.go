package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp/exposure"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/policy"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/toolpin"
)

// doctorSecuritySchema is the stable, versioned JSON schema of `doctor
// security --json` (issue #323). Bump the version suffix on any breaking
// change to the shape below.
const doctorSecuritySchema = "leanproxy.doctor.security/v1"

// SecurityStatus is one check's verdict.
type SecurityStatus string

const (
	StatusOK   SecurityStatus = "ok"
	StatusWarn SecurityStatus = "warn"
	StatusFail SecurityStatus = "fail"
	// StatusInfo marks a check that could not be evaluated statically
	// (e.g. it needs a running proxy) rather than a pass/fail verdict.
	StatusInfo SecurityStatus = "info"
)

// symbol renders the status the way the human report shows it.
func (s SecurityStatus) symbol() string {
	switch s {
	case StatusOK:
		return "✅" // ✅
	case StatusWarn:
		return "⚠️" // ⚠️
	case StatusFail:
		return "❌" // ❌
	default:
		return "ℹ️" // ℹ️
	}
}

// SecurityCheck is one evaluated check within an OWASP MCP Top 10 category.
type SecurityCheck struct {
	ID       string         `json:"id"`
	Title    string         `json:"title"`
	Status   SecurityStatus `json:"status"`
	Evidence string         `json:"evidence,omitempty"`
	Fix      string         `json:"fix,omitempty"`
}

// SecurityCategory is one OWASP MCP Top 10 category and its checks.
type SecurityCategory struct {
	ID     string          `json:"id"`
	Name   string          `json:"name"`
	Checks []SecurityCheck `json:"checks"`
}

// SecuritySummary counts checks by status across every category.
type SecuritySummary struct {
	OK   int `json:"ok"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Info int `json:"info"`
}

// SecurityReport is the full `doctor security` report: the versioned JSON
// schema documented in docs/security.md.
type SecurityReport struct {
	Schema      string             `json:"schema"`
	GeneratedAt time.Time          `json:"generated_at"`
	ConfigPath  string             `json:"config_path,omitempty"`
	Categories  []SecurityCategory `json:"categories"`
	Summary     SecuritySummary    `json:"summary"`
}

// add appends check to the named category, creating it if needed, and
// updates the running summary.
func (r *SecurityReport) add(id, name string, check SecurityCheck) {
	switch check.Status {
	case StatusOK:
		r.Summary.OK++
	case StatusWarn:
		r.Summary.Warn++
	case StatusFail:
		r.Summary.Fail++
	default:
		r.Summary.Info++
	}
	for i := range r.Categories {
		if r.Categories[i].ID == id {
			r.Categories[i].Checks = append(r.Categories[i].Checks, check)
			return
		}
	}
	r.Categories = append(r.Categories, SecurityCategory{ID: id, Name: name, Checks: []SecurityCheck{check}})
}

// ExitCode is 1 when any check failed (❌), 0 otherwise. Warnings and info
// items never fail the exit code, so the report can be used as a
// pre-commit or CI gate on hard findings only.
func (r *SecurityReport) ExitCode() int {
	if r.Summary.Fail > 0 {
		return 1
	}
	return 0
}

// buildSecurityReport evaluates every OWASP MCP Top 10 check against cfg
// (the loaded leanproxy_servers.yaml, possibly empty), the live status
// file (nil when no proxy is running) and the local, on-disk state
// (quarantine directory, pin file, token file). It never makes a network
// call and never reads or prints a secret value.
func buildSecurityReport(cfg *migrate.Config, running *statusfile.StatusInfo, home, cfgPath string) *SecurityReport {
	if cfg == nil {
		cfg = &migrate.Config{}
	}
	r := &SecurityReport{Schema: doctorSecuritySchema, GeneratedAt: time.Now().UTC(), ConfigPath: cfgPath}

	addMCP01SecretExposure(r, cfg)
	addMCP02PrivilegeScope(r, cfg)
	addMCP03ToolPoisoning(r, cfg)
	addMCP04SupplyChain(r, cfg)
	addMCP05CommandInjection(r, cfg)
	addMCP06IntentFlowSubversion(r, cfg)
	addMCP07AuthNAuthZ(r, cfg, running, home)
	addMCP08AuditTelemetry(r, cfg)
	addMCP09ShadowServers(r, cfg)
	addMCP10ContextOverSharing(r, cfg)

	return r
}

// ---- MCP01: secret exposure ----------------------------------------------

func addMCP01SecretExposure(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP01", "Secret Exposure"

	b := cfg.Bouncer
	if b.IsEnabled() {
		r.add(id, name, SecurityCheck{ID: "redaction_enabled", Title: "Response redaction (bouncer)",
			Status: StatusOK, Evidence: "bouncer.enabled: true (or unset, the default) — secrets in tool output are redacted before reaching the client."})
	} else {
		r.add(id, name, SecurityCheck{ID: "redaction_enabled", Title: "Response redaction (bouncer)",
			Status: StatusFail, Evidence: "bouncer.enabled: false — tool output is forwarded unredacted.",
			Fix: "Remove `bouncer.enabled: false` (or set it to true) in leanproxy_servers.yaml."})
	}

	if loaded, err := b.CompilePatterns(); err != nil {
		r.add(id, name, SecurityCheck{ID: "custom_patterns_valid", Title: "Custom redaction patterns",
			Status: StatusFail, Evidence: err.Error(),
			Fix: "Fix the invalid regex under bouncer.patterns in leanproxy_servers.yaml."})
	} else {
		n := len(b.AllPatternDefs())
		st := StatusOK
		if n > 0 && len(loaded.Custom) < n {
			st = StatusWarn
		}
		r.add(id, name, SecurityCheck{ID: "custom_patterns_valid", Title: "Custom redaction patterns",
			Status: st, Evidence: fmt.Sprintf("%d custom pattern(s) configured, %d compiled.", n, len(loaded.Custom))})
	}

	if b.EntropyEnabled() {
		r.add(id, name, SecurityCheck{ID: "entropy_detection", Title: "High-entropy secret detector",
			Status: StatusOK, Evidence: "bouncer.entropy_detection: true — unrecognized random-looking tokens next to a key-like word are also redacted."})
	} else {
		r.add(id, name, SecurityCheck{ID: "entropy_detection", Title: "High-entropy secret detector",
			Status: StatusWarn, Evidence: "bouncer.entropy_detection is off (the default): only the built-in and custom regex patterns are redacted.",
			Fix: "Set bouncer.entropy_detection: true to also catch secrets no pattern recognizes."})
	}

	if lvl := strings.ToLower(strings.TrimSpace(os.Getenv("LEANPROXY_LOG_LEVEL"))); lvl == "debug" {
		r.add(id, name, SecurityCheck{ID: "log_level_debug_file", Title: "Debug logging to a file",
			Status: StatusWarn, Evidence: "LEANPROXY_LOG_LEVEL=debug: debug logs can include payload fragments; check --log-file is not enabled in production.",
			Fix: "Use --log-level info (or warn/error) outside of local debugging, or omit --log-file so debug logs stay on stderr only."})
	} else {
		r.add(id, name, SecurityCheck{ID: "log_level_debug_file", Title: "Debug logging to a file",
			Status: StatusInfo, Evidence: "Log level is a per-invocation flag (--log-level / --log-file), not persisted in the config; not available without a running proxy."})
	}
}

// ---- MCP02: privilege / scope ---------------------------------------------

func addMCP02PrivilegeScope(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP02", "Excessive Privilege / Scope"

	e, err := policy.Compile(cfg.Policy)
	if err != nil {
		r.add(id, name, SecurityCheck{ID: "policy_default", Title: "Per-tool policy default",
			Status: StatusFail, Evidence: err.Error(), Fix: "Fix the policy: block in leanproxy_servers.yaml."})
	} else {
		def := e.Default()
		st := StatusOK
		if def == "allow" && cfg.Policy == nil {
			st = StatusWarn
		}
		r.add(id, name, SecurityCheck{ID: "policy_default", Title: "Per-tool policy default",
			Status: st, Evidence: fmt.Sprintf("policy.default: %s, unknown_tools: %s.", def, e.UnknownTools()),
			Fix: "Add a policy: block with rules for destructive tools (docs/configuration.md#per-tool-policy)."})

		var noConfirm []string
		for i, rule := range e.Rules() {
			if rule.Action == policy.ActionAllow && strings.Contains(strings.ToLower(rule.Match), "delete") {
				noConfirm = append(noConfirm, fmt.Sprintf("rules[%d] (%s)", i, rule.Match))
			}
		}
		if len(noConfirm) > 0 {
			r.add(id, name, SecurityCheck{ID: "destructive_no_confirm", Title: "Destructive tools without confirm",
				Status: StatusWarn, Evidence: strings.Join(noConfirm, ", "),
				Fix: "Set action: confirm (or deny) on rules matching destructive tools."})
		} else {
			r.add(id, name, SecurityCheck{ID: "destructive_no_confirm", Title: "Destructive tools without confirm",
				Status: StatusOK, Evidence: "No policy rule allows a destructively-named tool without confirmation."})
		}
	}

	var inherit, cloudEnv []string
	for _, s := range cfg.Servers {
		if s == nil || s.Transport != migrate.TransportStdio || s.Stdio == nil {
			continue
		}
		if s.Stdio.InheritEnv {
			inherit = append(inherit, s.Name)
		}
		for _, n := range s.Stdio.EnvPassthrough {
			if isCloudCredentialEnvName(n) {
				cloudEnv = append(cloudEnv, s.Name+":"+n)
			}
		}
	}
	if len(inherit) > 0 {
		r.add(id, name, SecurityCheck{ID: "inherit_env", Title: "Servers with inherit_env: true",
			Status: StatusFail, Evidence: strings.Join(inherit, ", "),
			Fix: "Remove inherit_env: true and list only the variables each server needs under env_passthrough (see `doctor env`)."})
	} else {
		r.add(id, name, SecurityCheck{ID: "inherit_env", Title: "Servers with inherit_env: true",
			Status: StatusOK, Evidence: "No stdio server inherits the full proxy environment."})
	}
	if len(cloudEnv) > 0 {
		r.add(id, name, SecurityCheck{ID: "cloud_credential_passthrough", Title: "Cloud credential env passthrough",
			Status: StatusWarn, Evidence: strings.Join(cloudEnv, ", "),
			Fix: "Only pass cloud credentials to servers that need them; prefer a scoped token over AWS/GCP/Azure default credential variables."})
	} else {
		r.add(id, name, SecurityCheck{ID: "cloud_credential_passthrough", Title: "Cloud credential env passthrough",
			Status: StatusOK, Evidence: "No stdio server passes through a well-known cloud credential variable."})
	}
}

// cloudCredentialEnvNames are parent environment variable names that grant
// a child process cloud provider credentials; passing them through to an
// untrusted MCP server hands it that scope too.
var cloudCredentialEnvNames = []string{
	"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_PROFILE",
	"GOOGLE_APPLICATION_CREDENTIALS", "GCP_SERVICE_ACCOUNT",
	"AZURE_CLIENT_SECRET", "AZURE_CLIENT_ID", "AZURE_TENANT_ID",
	"GITHUB_TOKEN", "DOCKER_AUTH_CONFIG", "KUBECONFIG",
}

func isCloudCredentialEnvName(name string) bool {
	upper := strings.ToUpper(name)
	for _, n := range cloudCredentialEnvNames {
		if upper == n {
			return true
		}
	}
	return false
}

// ---- MCP03: tool poisoning -------------------------------------------------

func addMCP03ToolPoisoning(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP03", "Tool Poisoning / Rug Pull"

	pcfg := cfg.ToolPinningConfig()
	mode := pcfg.EffectiveMode()
	r.add(id, name, SecurityCheck{ID: "pinning_mode", Title: "Tool pinning mode",
		Status: pinningModeStatus(mode), Evidence: fmt.Sprintf("security.tool_pinning.mode: %s", mode),
		Fix: "Set security.tool_pinning.mode: warn (or block) so tool identity/description drift is detected."})

	path, err := pcfg.ResolvePath()
	if err != nil || mode == toolpin.ModeOff {
		r.add(id, name, SecurityCheck{ID: "unapproved_drift", Title: "Unapproved tool drift",
			Status: StatusInfo, Evidence: "Tool pinning is off or its pin file cannot be resolved; drift is not tracked."})
		return
	}
	store, err := toolpin.OpenStore(path)
	if err != nil || !store.Existed() {
		r.add(id, name, SecurityCheck{ID: "unapproved_drift", Title: "Unapproved tool drift",
			Status: StatusInfo, Evidence: "No pin file yet at " + path + " (tools are pinned on first discovery)."})
		return
	}
	f := store.Current()
	var pending, flagged, invisible []string
	tools := 0
	for _, sname := range f.SortedServerNames() {
		sp := f.Servers[sname]
		for _, tname := range sp.SortedToolNames() {
			tp := sp.Tools[tname]
			tools++
			switch tp.Status() {
			case toolpin.StatusNew, toolpin.StatusChanged:
				pending = append(pending, sname+"/"+tname)
			}
			findings := tp.Findings
			if tp.Pending != nil {
				findings = tp.Pending.Findings
			}
			for _, f := range findings {
				if strings.Contains(strings.ToLower(f.Rule), "unicode") || strings.Contains(strings.ToLower(f.Rule), "invisible") {
					invisible = append(invisible, sname+"/"+tname)
				}
			}
			if tp.Status() == toolpin.StatusApproved && toolpin.MaxSeverity(tp.Findings).AtLeast(toolpin.SeverityMedium) {
				flagged = append(flagged, sname+"/"+tname)
			}
		}
	}
	st := StatusOK
	if len(pending) > 0 && mode == toolpin.ModeWarn {
		st = StatusWarn
	} else if len(pending) > 0 && mode == toolpin.ModeBlock {
		st = StatusOK // block mode already refuses the drifted tools
	}
	r.add(id, name, SecurityCheck{ID: "unapproved_drift", Title: "Unapproved tool drift",
		Status: st, Evidence: fmt.Sprintf("%d tool(s) pinned, %d awaiting approval.", tools, len(pending)),
		Fix: "Review with `leanproxy-mcp tools pins diff`, approve with `leanproxy-mcp tools pins approve <server> <tool>|--all`."})

	descSt := StatusOK
	if len(flagged) > 0 {
		descSt = StatusWarn
	}
	r.add(id, name, SecurityCheck{ID: "description_scanner", Title: "Description-scanner findings",
		Status: descSt, Evidence: fmt.Sprintf("%d approved tool(s) flagged medium+ severity.", len(flagged)),
		Fix: "Review flagged tools with `leanproxy-mcp tools pins list --all` before trusting them further."})

	invSt := StatusOK
	if len(invisible) > 0 {
		invSt = StatusFail
	}
	r.add(id, name, SecurityCheck{ID: "invisible_unicode", Title: "Invisible unicode in tool descriptions",
		Status: invSt, Evidence: fmt.Sprintf("%d tool(s) flagged for invisible/unusual unicode.", len(invisible)),
		Fix: "Treat the flagged tool's description as untrusted; do not approve it without inspecting the raw bytes."})
}

func pinningModeStatus(mode toolpin.Mode) SecurityStatus {
	switch mode {
	case toolpin.ModeBlock:
		return StatusOK
	case toolpin.ModeWarn:
		return StatusWarn
	default:
		return StatusFail
	}
}

// ---- MCP04: supply chain ---------------------------------------------------

func addMCP04SupplyChain(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP04", "Supply Chain"

	var unpinned, unverified, unsandboxedUnverified []string
	for _, s := range cfg.Servers {
		if s == nil || s.Transport != migrate.TransportStdio || s.Stdio == nil {
			continue
		}
		if !isPinnedPackageCommand(s.Stdio.Command, s.Stdio.Args) {
			unpinned = append(unpinned, s.Name)
		}
		isMarketplace := s.InstalledFrom != nil
		isVerified := isMarketplace && s.InstalledFrom.Version != ""
		if isMarketplace && !isVerified {
			unverified = append(unverified, s.Name)
		}
		sandboxed := s.Stdio.Sandbox != nil && s.Stdio.Sandbox.Runtime != "" && s.Stdio.Sandbox.Runtime != "none"
		if isMarketplace && !isVerified && !sandboxed {
			unsandboxedUnverified = append(unsandboxedUnverified, s.Name)
		}
	}

	st := StatusOK
	if len(unpinned) > 0 {
		st = StatusWarn
	}
	r.add(id, name, SecurityCheck{ID: "unpinned_version", Title: "Servers without a pinned version",
		Status: st, Evidence: joinOrNone(unpinned),
		Fix: "Pin an exact version, e.g. `npx -y pkg@1.2.3`, or run `leanproxy-mcp marketplace update` for marketplace-installed servers."})

	st = StatusOK
	if len(unverified) > 0 {
		st = StatusWarn
	}
	r.add(id, name, SecurityCheck{ID: "unverified_marketplace", Title: "Unverified marketplace installs",
		Status: st, Evidence: joinOrNone(unverified),
		Fix: "Re-run `leanproxy-mcp marketplace install <name>` to record the pinned version and provenance."})

	st = StatusOK
	if len(unsandboxedUnverified) > 0 {
		st = StatusFail
	}
	r.add(id, name, SecurityCheck{ID: "unsandboxed_unverified", Title: "Unsandboxed unverified servers",
		Status: st, Evidence: joinOrNone(unsandboxedUnverified),
		Fix: "Add a stdio.sandbox block (runtime: docker, network: none) for third-party servers you have not pinned a version for."})
}

// isPinnedPackageCommand reports whether a stdio command that runs a
// package manager (npx, uvx, pnpm dlx, bunx) names an exact version.
func isPinnedPackageCommand(command string, args []string) bool {
	base := filepath.Base(command)
	switch base {
	case "npx", "bunx", "uvx":
	default:
		return true // not a package-manager invocation; nothing to pin here
	}
	last := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		last = a
	}
	if last == "" {
		return true
	}
	if base == "uvx" {
		return strings.Contains(last, "==") || strings.Contains(last, "@")
	}
	// npx/bunx: a scoped package ("@org/name") with no version still needs
	// a second "@" for the version to count as pinned.
	trimmed := strings.TrimPrefix(last, "@")
	return strings.Contains(trimmed, "@")
}

// ---- MCP05: command injection ---------------------------------------------

var shellCommands = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true,
	"cmd": true, "cmd.exe": true, "powershell": true, "powershell.exe": true, "pwsh": true,
}

var shellExecFlags = map[string]bool{
	"-c": true, "/c": true, "-command": true, "-Command": true,
}

func addMCP05CommandInjection(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP05", "Command Injection"

	var flagged []string
	for _, s := range cfg.Servers {
		if s == nil || s.Transport != migrate.TransportStdio || s.Stdio == nil {
			continue
		}
		base := filepath.Base(s.Stdio.Command)
		if !shellCommands[strings.ToLower(base)] {
			continue
		}
		hasExecFlag := false
		for _, a := range s.Stdio.Args {
			if shellExecFlags[a] || shellExecFlags[strings.ToLower(a)] {
				hasExecFlag = true
				break
			}
		}
		if hasExecFlag {
			flagged = append(flagged, s.Name+" ("+s.Stdio.Command+")")
		}
	}
	st := StatusOK
	if len(flagged) > 0 {
		st = StatusFail
	}
	r.add(id, name, SecurityCheck{ID: "shell_invocation", Title: "Stdio commands that invoke a shell",
		Status: st, Evidence: joinOrNone(flagged),
		Fix: "Invoke the target program directly (argv, no shell) instead of `sh -c \"...\"`; any argument built from tool input becomes a command-injection vector under a shell."})
}

// ---- MCP06: intent-flow subversion (the injection guard) -----------------

func addMCP06IntentFlowSubversion(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP06", "Intent-Flow Subversion (Prompt Injection)"

	icfg := cfg.Injection
	if icfg == nil || !icfg.Enabled {
		r.add(id, name, SecurityCheck{ID: "injection_guard", Title: "Prompt-injection guard",
			Status: StatusFail, Evidence: "injection.enabled is false or the injection: block is absent.",
			Fix: "Add `injection: { enabled: true }` to leanproxy_servers.yaml."})
		return
	}
	r.add(id, name, SecurityCheck{ID: "injection_guard", Title: "Prompt-injection guard",
		Status: StatusOK, Evidence: fmt.Sprintf("injection.enabled: true, threshold: %d.", icfg.EffectiveThreshold())})

	reqRules := icfg.BuildDispatcher().Rules()
	r.add(id, name, SecurityCheck{ID: "request_policy", Title: "Request risk-band policy",
		Status: StatusOK, Evidence: describeRules(reqRules)})

	if icfg.ScansResponses() {
		respRules := icfg.BuildResponseDispatcher().Rules()
		r.add(id, name, SecurityCheck{ID: "response_policy", Title: "Response risk-band policy",
			Status: StatusOK, Evidence: describeRules(respRules)})
	} else {
		r.add(id, name, SecurityCheck{ID: "response_policy", Title: "Response risk-band policy",
			Status: StatusWarn, Evidence: "injection.scan_responses: false — tool/resource/prompt output is not classified for injected instructions.",
			Fix: "Remove `scan_responses: false` to classify upstream responses too."})
	}
}

func describeRules(rules []injection.Rule) string {
	parts := make([]string, 0, len(rules))
	for _, rl := range rules {
		parts = append(parts, fmt.Sprintf("%d-%d:%s", rl.MinRisk, rl.MaxRisk, rl.Action))
	}
	return strings.Join(parts, ", ")
}

// ---- MCP07: authN/authZ ---------------------------------------------------

func addMCP07AuthNAuthZ(r *SecurityReport, cfg *migrate.Config, running *statusfile.StatusInfo, home string) {
	const id, name = "MCP07", "Broken AuthN/AuthZ"

	h := cfg.EffectiveHTTPFrontend()
	if running != nil && running.HTTP != nil {
		st := running.HTTP
		status := StatusOK
		var evidence []string
		if !st.Loopback {
			status = StatusWarn
			evidence = append(evidence, "non-loopback bind: reachable from the network")
		}
		if !st.Auth {
			status = StatusFail
			evidence = append(evidence, "authentication disabled (--no-auth)")
		}
		if len(evidence) == 0 {
			evidence = append(evidence, "loopback-only, bearer token required")
		}
		r.add(id, name, SecurityCheck{ID: "http_frontend_exposure", Title: "HTTP front end exposure (server run --http)",
			Status: status, Evidence: fmt.Sprintf("%s: %s", st.URL, strings.Join(evidence, "; ")),
			Fix: "Bind to 127.0.0.1 and keep authentication on; use --http-token or the generated token file for a non-loopback bind."})
	} else {
		r.add(id, name, SecurityCheck{ID: "http_frontend_exposure", Title: "HTTP front end exposure (server run --http)",
			Status: StatusInfo, Evidence: fmt.Sprintf("Not running. Configured allowed_origins: %s, allowed_hosts: %s.", joinOrNone(h.AllowedOrigins), joinOrNone(h.AllowedHosts))})
	}

	if home != "" {
		path := serveTokenPath(home)
		if info, err := os.Stat(path); err == nil {
			mode := info.Mode().Perm()
			if mode&0o077 != 0 {
				r.add(id, name, SecurityCheck{ID: "serve_token_file_mode", Title: "Serve token file permissions",
					Status: StatusWarn, Evidence: fmt.Sprintf("%s is mode %s (readable by other users).", path, mode),
					Fix: "chmod 600 " + path + " (a fresh start also tightens it automatically)."})
			} else {
				r.add(id, name, SecurityCheck{ID: "serve_token_file_mode", Title: "Serve token file permissions",
					Status: StatusOK, Evidence: fmt.Sprintf("%s is mode %s.", path, mode)})
			}
		} else {
			r.add(id, name, SecurityCheck{ID: "serve_token_file_mode", Title: "Serve token file permissions",
				Status: StatusInfo, Evidence: "Token file not created yet; generated on first `serve`/`server run --http` start."})
		}
	}

	r.add(id, name, SecurityCheck{ID: "dashboard_metrics_exposure", Title: "Dashboard and metrics endpoint exposure",
		Status: StatusInfo, Evidence: "Bind address and token are `--dashboard-bind`/`--metrics-bind` flags of `serve` and `server run`, not persisted in the config; not available without a running proxy. Both refuse to start on a non-loopback bind without a token (#316)."})
}

// ---- MCP08: audit / telemetry ----------------------------------------------

func addMCP08AuditTelemetry(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP08", "Insufficient Audit / Telemetry"

	if cfg.Telemetry != nil && cfg.Telemetry.Enabled {
		r.add(id, name, SecurityCheck{ID: "otel_configured", Title: "OpenTelemetry export",
			Status: StatusOK, Evidence: "telemetry.enabled: true."})
	} else if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" {
		r.add(id, name, SecurityCheck{ID: "otel_configured", Title: "OpenTelemetry export",
			Status: StatusOK, Evidence: "OTEL_EXPORTER_OTLP_ENDPOINT is set."})
	} else {
		r.add(id, name, SecurityCheck{ID: "otel_configured", Title: "OpenTelemetry export",
			Status: StatusWarn, Evidence: "No telemetry: block and no OTEL_EXPORTER_OTLP_ENDPOINT.",
			Fix: "Set telemetry.enabled: true (and telemetry.otlp.endpoint) or export OTEL_EXPORTER_OTLP_ENDPOINT for traces/metrics."})
	}

	r.add(id, name, SecurityCheck{ID: "policy_audit_log", Title: "Per-tool policy audit log",
		Status: StatusOK, Evidence: "Every policy decision (allow/deny/confirm) is logged with outcome and args_sha256 whenever a policy: block is configured; always active, not configurable off."})
}

// ---- MCP09: shadow servers --------------------------------------------------

func addMCP09ShadowServers(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP09", "Shadow Servers (Unmediated MCP Access)"

	discovered, err := migrate.NewMigrator().Scan(context.Background())
	if err != nil {
		r.add(id, name, SecurityCheck{ID: "shadow_servers", Title: "Servers configured outside LeanProxy",
			Status: StatusInfo, Evidence: "Could not scan IDE configs: " + err.Error()})
		return
	}

	routed := map[string]bool{}
	for _, s := range cfg.Servers {
		if s != nil {
			routed[strings.ToLower(s.Name)] = true
		}
	}

	shadow := make([]string, 0, len(discovered.Servers))
	for _, d := range discovered.Servers {
		if routed[strings.ToLower(d.Name)] {
			continue
		}
		shadow = append(shadow, fmt.Sprintf("%s (%s)", d.Name, d.Source))
	}
	sort.Strings(shadow)

	st := StatusOK
	if len(shadow) > 0 {
		st = StatusFail
	}
	r.add(id, name, SecurityCheck{ID: "shadow_servers", Title: "Servers configured outside LeanProxy",
		Status: st, Evidence: joinOrNone(shadow),
		Fix: "Import them with `leanproxy-mcp migrate` so they run through LeanProxy's redaction, policy and injection guard instead of bypassing all of it."})
}

// ---- MCP10: context over-sharing (response governor) -----------------------

func addMCP10ContextOverSharing(r *SecurityReport, cfg *migrate.Config) {
	const id, name = "MCP10", "Excessive Context / Context Over-Sharing"

	gcfg := cfg.Response
	if gcfg != nil && gcfg.Enabled {
		r.add(id, name, SecurityCheck{ID: "response_governor", Title: "Response token governor",
			Status: StatusOK, Evidence: fmt.Sprintf("response.enabled: true, dedup: %s, default_projections: %v.", orDash(gcfg.Dedup), gcfg.DefaultProjections)})
	} else {
		r.add(id, name, SecurityCheck{ID: "response_governor", Title: "Response token governor",
			Status: StatusWarn, Evidence: "response.enabled is false or the response: block is absent (off by default).",
			Fix: "Set response.enabled: true to cap and project large tool results before they reach the client's context."})
	}

	exp := cfg.Exposure
	mode := string(exposure.ModeRouter)
	builtin := true
	if exp != nil {
		if exp.Mode != "" {
			mode = string(exp.Mode)
		}
		if exp.BuiltinClients != nil {
			builtin = *exp.BuiltinClients
		}
	}
	st := StatusOK
	if mode == string(exposure.ModePassthrough) {
		st = StatusInfo
	}
	r.add(id, name, SecurityCheck{ID: "exposure_mode", Title: "Tool exposure mode",
		Status: st, Evidence: fmt.Sprintf("exposure.mode (fallback for unmatched clients): %s. Built-in client table (Claude Code, Claude Desktop, Cursor, VS Code -> passthrough): %v.", mode, builtin),
		Fix: "router/hybrid keep every upstream tool behind search_tools/invoke_tool, so a client only ever sees the tools it asked for; passthrough lists every tool's full schema up front."})
}

func orDash(s string) string {
	if s == "" {
		return "off"
	}
	return s
}

// ---- rendering --------------------------------------------------------------

func writeSecurityReportHuman(w io.Writer, r *SecurityReport) {
	fmt.Fprintln(w, "# LeanProxy Security Report (OWASP MCP Top 10)")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Generated: %s (local-only, read-only — no network calls)\n", r.GeneratedAt.Format(time.RFC3339))
	if r.ConfigPath != "" {
		fmt.Fprintf(w, "Config: %s\n", r.ConfigPath)
	}
	fmt.Fprintln(w)

	for _, cat := range r.Categories {
		fmt.Fprintf(w, "## %s %s\n\n", cat.ID, cat.Name)
		for _, c := range cat.Checks {
			fmt.Fprintf(w, "  %s %s\n", c.Status.symbol(), c.Title)
			if c.Evidence != "" {
				fmt.Fprintf(w, "     %s\n", c.Evidence)
			}
			if c.Fix != "" && c.Status != StatusOK {
				fmt.Fprintf(w, "     Fix: %s\n", c.Fix)
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "Summary: %d ok, %d warn, %d fail, %d info\n", r.Summary.OK, r.Summary.Warn, r.Summary.Fail, r.Summary.Info)
}

func writeSecurityReportMarkdown(w io.Writer, r *SecurityReport) {
	fmt.Fprintln(w, "# LeanProxy Security Report (OWASP MCP Top 10)")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "_Generated %s — local-only, read-only, no network calls._\n\n", r.GeneratedAt.Format(time.RFC3339))
	if r.ConfigPath != "" {
		fmt.Fprintf(w, "Config: `%s`\n\n", r.ConfigPath)
	}
	for _, cat := range r.Categories {
		fmt.Fprintf(w, "## %s %s\n\n", cat.ID, cat.Name)
		fmt.Fprintln(w, "| Status | Check | Evidence | Fix |")
		fmt.Fprintln(w, "|---|---|---|---|")
		for _, c := range cat.Checks {
			fix := c.Fix
			if fix == "" {
				fix = "-"
			}
			fmt.Fprintf(w, "| %s | %s | %s | %s |\n", c.Status.symbol(), mdEscape(c.Title), mdEscape(c.Evidence), mdEscape(fix))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "**Summary:** %d ok, %d warn, %d fail, %d info\n", r.Summary.OK, r.Summary.Warn, r.Summary.Fail, r.Summary.Info)
}

func mdEscape(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func writeSecurityReportJSON(w io.Writer, r *SecurityReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}
