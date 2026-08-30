package cmd

import (
	"errors"
	"fmt"

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
	RunE: func(cmd *cobra.Command, args []string) error {
		// Read the same leanproxy.yaml the server loads, so the `bouncer:`
		// block validated here is exactly what `serve` will apply.
		full, err := migrate.LoadConfig(cmd.Context(), bouncerConfigPath)
		if err != nil {
			if errors.Is(err, migrate.ErrConfigNotFound) {
				return fmt.Errorf(
					"config file %q not found. Pass --config to point at your leanproxy.yaml, "+
						"or create one before running validate-patterns",
					bouncerConfigPath)
			}
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg := &bouncer.Config{}
		if full != nil && full.Bouncer != nil {
			cfg = full.Bouncer
		}
		if !cfg.IsEnabled() {
			fmt.Fprintln(cmd.OutOrStdout(), "Warning: bouncer.enabled is false; secret redaction is OFF")
		}
		if cfg.ShouldAlwaysCallSidecar() {
			fmt.Fprintln(cmd.OutOrStdout(), "Note: bouncer.sidecar_always_call is true; the sidecar LLM will run on every request")
		}
		loaded, err := cfg.CompilePatterns()
		if err != nil {
			return fmt.Errorf("failed to compile patterns: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Valid patterns: %d (custom: %d, built-in: %d)\n",
			len(loaded.All), len(loaded.Custom), len(loaded.BuiltIn))
		return nil
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
