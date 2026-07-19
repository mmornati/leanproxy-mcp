package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
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
	Short: "Show injection security policy and quarantine status",
	Run: func(cmd *cobra.Command, args []string) {
		runSecurityDiagnostic()
	},
}

func init() {
	doctorCmd.AddCommand(doctorSecurityCmd)
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
}
