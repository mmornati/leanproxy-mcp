package cmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostic checks on the leanproxy installation",
	Run: func(cmd *cobra.Command, args []string) {
		if securityCheck {
			os.Exit(runSecurityDiagnostic(os.Stdout, doctorSecurityFlags.jsonOut, doctorSecurityFlags.markdownOut))
		}
		_ = cmd.Help()
	},
}

var securityCheck bool

var doctorSecurityFlags struct {
	jsonOut     bool
	markdownOut bool
}

var doctorSecurityCmd = &cobra.Command{
	Use:   "security",
	Short: "OWASP-MCP-mapped local security report (redaction, injection guard, policy, tool pinning, sandbox, exposure, ...)",
	Run: func(cmd *cobra.Command, args []string) {
		os.Exit(runSecurityDiagnostic(os.Stdout, doctorSecurityFlags.jsonOut, doctorSecurityFlags.markdownOut))
	},
}

var doctorEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Show, per configured stdio server, which environment variables are passed and which are dropped (#311)",
	Run: func(cmd *cobra.Command, args []string) {
		runEnvDiagnostic()
	},
}

var doctorSandboxCmd = &cobra.Command{
	Use:   "sandbox",
	Short: "Show, per configured stdio server, its sandbox (container isolation) status (#312)",
	Run: func(cmd *cobra.Command, args []string) {
		runSandboxDiagnostic()
	},
}

func init() {
	doctorCmd.AddCommand(doctorSecurityCmd)
	doctorCmd.AddCommand(doctorEnvCmd)
	doctorCmd.AddCommand(doctorSandboxCmd)
	doctorCmd.Flags().BoolVar(&securityCheck, "security", false, "Show security diagnostics")
	doctorCmd.PersistentFlags().BoolVar(&doctorSecurityFlags.jsonOut, "json", false, "Print the security report as JSON (leanproxy.doctor.security/v1)")
	doctorCmd.PersistentFlags().BoolVar(&doctorSecurityFlags.markdownOut, "markdown", false, "Print the security report as Markdown")
	RootCmd.AddCommand(doctorCmd)
}

func configDir() string {
	var configPath string
	if GlobalConfigPath != "" {
		configPath = GlobalConfigPath
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configPath = filepath.Join(home, ".config", "leanproxy_servers.yaml")
	}
	return configPath
}

// runSecurityDiagnostic is `doctor security` / `doctor --security` (issue
// #323): it builds the OWASP-MCP-mapped report (buildSecurityReport) from
// the local config and, when a proxy is running, the live status file —
// no network call is ever made — and prints it as human text (default),
// JSON (--json, the stable leanproxy.doctor.security/v1 schema) or
// Markdown (--markdown). It returns the process exit code: non-zero when
// any check failed (❌), so it can be used as a pre-commit or CI gate.
//
// Below the OWASP report (human/Markdown output only) it also prints the
// same detailed, per-feature sections `doctor security` has shown since
// #310/#311/#312/#314 (tool pinning, per-tool policy, HTTP front end,
// sandbox), unchanged, so existing tooling that grep's this output keeps
// working.
func runSecurityDiagnostic(w io.Writer, jsonOut, markdownOut bool) int {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("doctor: cannot determine home directory", "error", err)
		return 1
	}

	cfgPath := configDir()
	cfg, cfgErr := migrate.LoadConfig(context.Background(), cfgPath)
	if cfgErr != nil {
		cfg = &migrate.Config{}
	}

	running, _ := statusfile.ReadCurrentStatus()
	report := buildSecurityReport(cfg, running, home, cfgPath)

	switch {
	case jsonOut:
		if err := writeSecurityReportJSON(w, report); err != nil {
			slog.Error("doctor security: encode JSON report", "error", err)
			return 1
		}
		return report.ExitCode()
	case markdownOut:
		writeSecurityReportMarkdown(w, report)
	default:
		writeSecurityReportHuman(w, report)
	}

	if jsonOut {
		return report.ExitCode()
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "---")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Detail")
	fmt.Fprintln(w)

	fmt.Fprintln(w, "## Policy Configuration")
	fmt.Fprintln(w)
	rules := injectionRequestRules(cfg)
	if cfg.Injection == nil {
		fmt.Fprintln(w, "  (No injection: block configured; showing default rules)")
		fmt.Fprintln(w)
	}
	for _, rl := range rules {
		fmt.Fprintf(w, "  Risk %3d-%3d -> %s\n", rl.MinRisk, rl.MaxRisk, rl.Action)
	}
	fmt.Fprintln(w)

	leanproxyDir := filepath.Join(home, ".leanproxy")
	qDir := filepath.Join(leanproxyDir, "quarantine")
	fmt.Fprintln(w, "## Quarantine Status")
	fmt.Fprintln(w)
	qFiles, globErr := filepath.Glob(filepath.Join(qDir, "*.json"))
	if globErr != nil || qFiles == nil {
		qFiles = []string{}
	}
	if len(qFiles) > 0 {
		fmt.Fprintf(w, "  Quarantined payloads: %d\n", len(qFiles))
		for _, f := range qFiles {
			fmt.Fprintf(w, "    - %s\n", f)
		}
	} else {
		fmt.Fprintln(w, "  No quarantined payloads found.")
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Total quarantined payloads: %d\n", len(qFiles))
	fmt.Fprintln(w)

	printToolPinningStatus(w)
	fmt.Fprintln(w)

	printPolicyStatus(w)
	fmt.Fprintln(w)

	printHTTPFrontendStatusFor(w, cfg, running, home)
	fmt.Fprintln(w)

	printSandboxStatus(w)

	return report.ExitCode()
}

// injectionRequestRules returns the configured injection.request_policies
// (or the historical injection.policies / injection.action), falling back
// to the built-in default rules when injection is not configured. Fixes
// the carry-over bug where `doctor security` decoded the whole config file
// as if it were the injection block (reported in #315's PR): it always
// showed the default rules, never the configured ones, because the
// top-level "injection:" key was never unwrapped.
func injectionRequestRules(cfg *migrate.Config) []injection.Rule {
	if cfg != nil && cfg.Injection != nil {
		return cfg.Injection.BuildDispatcher().Rules()
	}
	return injection.DefaultRules()
}

// printHTTPFrontendStatusFor reports the exposure of the Streamable HTTP
// front end (#309): the configured browser/Host allowlists and limits, the
// token file, and — when a `server run --http` instance is running — its
// URL, whether it is loopback-only and whether it requires the token.
func printHTTPFrontendStatusFor(w io.Writer, cfg *migrate.Config, running *statusfile.StatusInfo, home string) {
	fmt.Fprintln(w, "## HTTP Front End (server run --http)")
	fmt.Fprintln(w)
	h := cfg.EffectiveHTTPFrontend()
	if running != nil && running.HTTP != nil {
		st := running.HTTP
		exposure := "loopback only"
		if !st.Loopback {
			exposure = "NON-LOOPBACK: reachable from the network, traffic unencrypted"
		}
		auth := "bearer token required"
		if !st.Auth {
			auth = "DISABLED (--no-auth): any local process can drive the upstream servers"
		}
		fmt.Fprintf(w, "  Running: %s (pid %d)\n", st.URL, running.PID)
		fmt.Fprintf(w, "  Exposure: %s\n", exposure)
		fmt.Fprintf(w, "  Authentication: %s\n", auth)
		fmt.Fprintf(w, "  Allowed browser origins: %s\n", joinOrNone(st.AllowedOrigins))
		fmt.Fprintf(w, "  Extra allowed Host names: %s\n", joinOrNone(st.AllowedHosts))
		fmt.Fprintf(w, "  Max sessions: %d\n", st.MaxSessions)
	} else {
		fmt.Fprintln(w, "  Not running (start it with: leanproxy-mcp server run --http 127.0.0.1:8765)")
		fmt.Fprintf(w, "  Configured browser origins (server.http.allowed_origins): %s\n", joinOrNone(h.AllowedOrigins))
		fmt.Fprintf(w, "  Configured extra Host names (server.http.allowed_hosts): %s\n", joinOrNone(h.AllowedHosts))
		fmt.Fprintf(w, "  Max sessions: %d, idle timeout: %s, max body: %d bytes\n", h.MaxSessions, h.SessionIdleTimeout, h.MaxBodyBytes)
	}
	if home != "" {
		path := serveTokenPath(home)
		if info, err := os.Stat(path); err == nil {
			mode := info.Mode().Perm()
			note := "ok"
			if mode&0o077 != 0 {
				note = "WARNING: readable by other users (tightened to 0600 on next start)"
			}
			fmt.Fprintf(w, "  Token file: %s (mode %s, %s)\n", path, mode, note)
		} else {
			fmt.Fprintf(w, "  Token file: %s (not created yet; generated on first start)\n", path)
		}
	}
	fmt.Fprintln(w, "  Host and Origin headers are always validated; non-loopback binds require a token.")
}

// runEnvDiagnostic prints, per configured stdio server, the environment
// variable names that would be passed to its child process and the parent
// variable names that would be dropped (#311). It never prints values.
func runEnvDiagnostic() {
	cfgPath := configDir()
	if cfgPath == "" {
		fmt.Println("doctor env: cannot determine config path")
		os.Exit(1)
	}

	cfg, err := migrate.LoadConfig(context.Background(), cfgPath)
	if err != nil {
		fmt.Printf("doctor env: cannot load config %q: %v\n", cfgPath, err)
		os.Exit(1)
	}

	fmt.Println("# Child Environment Report (#311)")
	fmt.Println()
	fmt.Println("Names only — values are never shown.")
	fmt.Println()

	parentEnv := os.Environ()
	found := false
	for _, server := range cfg.Servers {
		if server.Transport != migrate.TransportStdio || server.Stdio == nil {
			continue
		}
		found = true

		fmt.Printf("## %s\n\n", server.Name)
		if server.Stdio.InheritEnv {
			fmt.Println("  inherit_env: true — the full proxy environment is passed through.")
		}

		serverCfg := pool.StdioServerConfig{
			Name:           server.Name,
			Command:        server.Stdio.Command,
			Args:           server.Stdio.Args,
			Env:            server.Stdio.Env,
			EnvPassthrough: server.Stdio.EnvPassthrough,
			InheritEnv:     server.Stdio.InheritEnv,
		}

		passed, dropped, err := pool.ChildEnvReport(parentEnv, serverCfg)
		if err != nil {
			fmt.Printf("  ERROR: %v\n\n", err)
			continue
		}

		fmt.Printf("  Passed (%d): %s\n", len(passed), joinOrNone(passed))
		fmt.Printf("  Dropped (%d): %s\n", len(dropped), joinOrNone(dropped))
		fmt.Println()
	}

	if !found {
		fmt.Println("(No stdio servers configured.)")
	}
}

// runSandboxDiagnostic prints, per configured stdio server, whether it runs
// sandboxed (container runtime, image, network) and whether the configured
// runtime binary is actually available on PATH (#312).
func runSandboxDiagnostic() {
	cfgPath := configDir()
	if cfgPath == "" {
		fmt.Println("doctor sandbox: cannot determine config path")
		os.Exit(1)
	}

	cfg, err := migrate.LoadConfig(context.Background(), cfgPath)
	if err != nil {
		fmt.Printf("doctor sandbox: cannot load config %q: %v\n", cfgPath, err)
		os.Exit(1)
	}

	fmt.Println("# Sandbox Status (#312)")
	fmt.Println()

	printSandboxStatusForConfig(os.Stdout, cfg)
}

// printSandboxStatus is the `doctor security` summary: same content as
// `doctor sandbox`, folded into the combined diagnostic.
func printSandboxStatus(w io.Writer) {
	fmt.Fprintln(w, "## Sandbox Status")
	fmt.Fprintln(w)

	cfgPath := configDir()
	if cfgPath == "" {
		fmt.Fprintln(w, "  (cannot determine config path)")
		return
	}
	cfg, err := migrate.LoadConfig(context.Background(), cfgPath)
	if err != nil {
		fmt.Fprintf(w, "  (cannot load config %q: %v)\n", cfgPath, err)
		return
	}
	printSandboxStatusForConfig(w, cfg)
}

func printSandboxStatusForConfig(w io.Writer, cfg *migrate.Config) {
	if cfg == nil || len(cfg.Servers) == 0 {
		fmt.Fprintln(w, "  (no servers configured)")
		return
	}

	found := false
	checked := map[string]error{}
	for _, server := range cfg.Servers {
		if server == nil || server.Transport != migrate.TransportStdio || server.Stdio == nil {
			continue
		}
		found = true

		sb := server.Stdio.Sandbox
		if sb == nil || sb.Runtime == "" || sb.Runtime == "none" {
			fmt.Fprintf(w, "  %-24s unsandboxed\n", server.Name)
			continue
		}

		if _, ok := checked[sb.Runtime]; !ok {
			checked[sb.Runtime] = pool.CheckSandboxRuntime(sb.Runtime)
		}
		status := "available"
		if err := checked[sb.Runtime]; err != nil {
			status = "MISSING (" + err.Error() + ")"
		}
		image := sb.Image
		if image == "" {
			image, _ = migrate.InferSandboxImage(server.Stdio.Command)
		}
		network := sb.Network
		if network == "" {
			network = "none"
		}
		fmt.Fprintf(w, "  %-24s runtime=%s (%s) image=%s network=%s\n", server.Name, sb.Runtime, status, image, network)
	}

	if !found {
		fmt.Fprintln(w, "  (no stdio servers configured)")
	}
}

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
