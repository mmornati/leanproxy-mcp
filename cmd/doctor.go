package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostic checks on the leanproxy installation",
	Run: func(cmd *cobra.Command, args []string) {
		if securityCheck {
			runSecurityDiagnostic()
			return
		}
		_ = cmd.Help()
	},
}

var securityCheck bool

var doctorSecurityCmd = &cobra.Command{
	Use:   "security",
	Short: "Show injection security policy, quarantine and tool pinning status",
	Run: func(cmd *cobra.Command, args []string) {
		runSecurityDiagnostic()
	},
}

var doctorEnvCmd = &cobra.Command{
	Use:   "env",
	Short: "Show, per configured stdio server, which environment variables are passed and which are dropped (#311)",
	Run: func(cmd *cobra.Command, args []string) {
		runEnvDiagnostic()
	},
}

func init() {
	doctorCmd.AddCommand(doctorSecurityCmd)
	doctorCmd.AddCommand(doctorEnvCmd)
	doctorCmd.Flags().BoolVar(&securityCheck, "security", false, "Show security diagnostics")
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

func runSecurityDiagnostic() {
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Error("doctor: cannot determine home directory", "error", err)
		os.Exit(1)
	}

	leanproxyDir := filepath.Join(home, ".leanproxy")
	qDir := filepath.Join(leanproxyDir, "quarantine")

	fmt.Println("# Injection Security Diagnostic")
	fmt.Println()

	fmt.Println("## Policy Configuration")
	fmt.Println()

	cfgPath := configDir()
	var rules []injection.Rule

	if cfgPath != "" {
		cfg, loadErr := injection.LoadConfigFile(cfgPath)
		if loadErr == nil && cfg != nil {
			d := cfg.BuildDispatcher()
			rules = d.Rules()
		}
	}

	if len(rules) == 0 {
		rules = injection.DefaultRules()
		fmt.Println("  (No config file loaded; showing default rules)")
		fmt.Println()
	}

	for _, r := range rules {
		fmt.Printf("  Risk %3d-%3d -> %s\n", r.MinRisk, r.MaxRisk, r.Action)
	}
	fmt.Println()

	fmt.Println("## Quarantine Status")
	fmt.Println()
	qFiles, err := filepath.Glob(filepath.Join(qDir, "*.json"))
	if err != nil || qFiles == nil {
		qFiles = []string{}
	}
	if len(qFiles) > 0 {
		fmt.Printf("  Quarantined payloads: %d\n", len(qFiles))
		for _, f := range qFiles {
			fmt.Printf("    - %s\n", f)
		}
	} else {
		fmt.Println("  No quarantined payloads found.")
	}
	fmt.Println()

	fmt.Printf("Total quarantined payloads: %d\n", len(qFiles))
	fmt.Println()

	printToolPinningStatus(os.Stdout)
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

func joinOrNone(names []string) string {
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
