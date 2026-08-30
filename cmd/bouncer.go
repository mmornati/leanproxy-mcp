package cmd

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/spf13/cobra"
)

var bouncerConfigPath string

var bouncerCmd = &cobra.Command{
	Use:   "bouncer",
	Short: "Manage Bouncer redaction settings",
}

var validatePatternsCmd = &cobra.Command{
	Use:   "validate-patterns",
	Short: "Validate custom redaction patterns from config",
	Run: func(cmd *cobra.Command, args []string) {
		// Read the same leanproxy.yaml the server loads, so the `bouncer:`
		// block validated here is exactly what `serve` will apply.
		full, err := migrate.LoadConfig(cmd.Context(), bouncerConfigPath)
		if err != nil {
			slog.Error("failed to load config", "error", err)
			os.Exit(1)
		}
		cfg := &bouncer.Config{}
		if full != nil && full.Bouncer != nil {
			cfg = full.Bouncer
		}
		if !cfg.IsEnabled() {
			fmt.Println("Warning: bouncer.enabled is false; secret redaction is OFF")
		}
		loaded, err := cfg.CompilePatterns()
		if err != nil {
			slog.Error("failed to compile patterns", "error", err)
			os.Exit(1)
		}
		fmt.Printf("Valid patterns: %d (custom: %d, built-in: %d)\n",
			len(loaded.All), len(loaded.Custom), len(loaded.BuiltIn))
	},
}

var listPatternsCmd = &cobra.Command{
	Use:   "list-patterns",
	Short: "List all active redaction patterns",
	Run: func(cmd *cobra.Command, args []string) {
		loaded := bouncer.GetBuiltInPatterns()
		fmt.Println("# Built-in Patterns")
		for _, p := range loaded {
			fmt.Printf("  - %s: %s\n", p.Name, p.Description)
		}
	},
}

func init() {
	bouncerCmd.PersistentFlags().StringVar(&bouncerConfigPath, "config", "leanproxy.yaml", "path to config file")

	bouncerCmd.AddCommand(validatePatternsCmd)
	bouncerCmd.AddCommand(listPatternsCmd)
	RootCmd.AddCommand(bouncerCmd)
}
