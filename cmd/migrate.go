package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/utils/dryrun"
	"github.com/spf13/cobra"
)

var (
	migrateYes          bool
	migrateDryRun       bool
	migrateTarget       string
	migrateValidateOnly bool
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Auto-detect and import MCP server configurations from other tools",
	Long: `Scan for existing MCP configurations from OpenCode, Claude Code, Claude Desktop,
VS Code, Cursor and ~/.config/mcp.json. Import discovered servers (stdio, http and sse)
into leanproxy_servers.yaml with proper conflict resolution. Entries that run
leanproxy-mcp itself are skipped.`,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateYes, "yes", false, "Skip confirmation prompt")
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Preview scan results without importing")
	migrateCmd.Flags().StringVar(&migrateTarget, "target", "", "Target config file path (default: ~/.config/leanproxy_servers.yaml)")
	migrateCmd.Flags().BoolVar(&migrateValidateOnly, "validate-only", false, "Only validate servers without importing")
	RootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	migrator := migrate.NewMigrator()

	result, err := migrator.Scan(ctx)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	for _, warn := range result.Warnings {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", warn)
	}
	for _, srv := range result.Skipped {
		fmt.Printf("Skipping %s (%s): it runs leanproxy-mcp itself\n", srv.Name, srv.Source)
	}

	if len(result.Servers) == 0 {
		fmt.Println("No MCP configurations found on this system.")
		fmt.Println("To add servers manually, use: leanproxy-mcp server add")
		return nil
	}

	summary := migrator.Summarize(result.Servers)

	fmt.Printf("Found %d MCP server(s) from %d source(s):\n\n", summary.TotalServers, len(result.Scanners))
	for _, row := range []struct {
		label string
		count int
	}{
		{"OpenCode", summary.OpenCodeCount},
		{"Claude Code", summary.ClaudeCount},
		{"Claude Desktop", summary.ClaudeDesktopCount},
		{"VS Code", summary.VSCodeCount},
		{"Cursor", summary.CursorCount},
		{"Generic", summary.GenericCount},
	} {
		fmt.Printf("  %-15s %d server(s)\n", row.label+":", row.count)
	}
	fmt.Println()

	for i, srv := range result.Servers {
		target := ""
		switch {
		case srv.Stdio != nil:
			target = srv.Stdio.Command
		case srv.HTTP != nil:
			target = fmt.Sprintf("%s %s", srv.Transport, srv.HTTP.URL)
		}
		fmt.Printf("  [%d] %s (%s) - %s\n", i+1, srv.Name, srv.Source, target)
	}

	if migrateDryRun || DryRunEnabled {
		dr := dryrun.NewDryRunner(true)
		dr.Preview("migrate_import", map[string]interface{}{
			"server_count": summary.TotalServers,
			"target":       migrateTarget,
			"sources":      len(result.Scanners),
		})
		fmt.Println("\nDry-run mode: no changes were made.")
		return nil
	}

	if migrateValidateOnly {
		fmt.Println("\n--- Validation Mode ---")
		validationResult := migrator.Validate(result.Servers)

		if validationResult.HasErrors() {
			fmt.Printf("❌ Validation failed with %d error(s):\n\n", validationResult.ErrorCount())
			for _, err := range validationResult.Errors {
				fmt.Printf("  ✗ Server '%s': %s\n", err.ServerName, err.Message)
			}
			fmt.Println()
		} else {
			fmt.Println("✅ All servers passed validation!")
		}

		if validationResult.HasWarnings() {
			fmt.Printf("⚠️  %d warning(s):\n\n", validationResult.WarningCount())
			for _, warn := range validationResult.Warnings {
				fmt.Printf("  ⚠ Server '%s': %s\n", warn.ServerName, warn.Message)
			}
			fmt.Println()
		}

		if validationResult.HasErrors() {
			return fmt.Errorf("validation failed")
		}
		return nil
	}

	target := migrateTarget
	if target == "" {
		target = os.Getenv("LEANPROXY_CONFIG")
		if target == "" {
			home := os.Getenv("HOME")
			if home == "" {
				home = os.Getenv("USERPROFILE")
			}
			target = home + "/.config/leanproxy_servers.yaml"
		}
	}

	if !migrateYes {
		fmt.Printf("\nImport to %s? [y/N]: ", target)
		var response string
		if _, err := fmt.Scanln(&response); err != nil {
			// Treat unreadable/empty input as a declined confirmation.
			response = ""
		}
		if response != "y" && response != "Y" {
			fmt.Println("Import canceled.")
			return nil
		}
	}

	importResult, err := migrator.Import(ctx, result.Servers, target, migrateYes)
	if err != nil {
		return fmt.Errorf("import failed: %w", err)
	}

	fmt.Printf("\nImport complete!\n")
	fmt.Printf("  Imported: %d server(s)\n", importResult.Imported)
	fmt.Printf("  Target:   %s\n", target)

	if importResult.Validation != nil {
		if importResult.Validation.HasErrors() {
			fmt.Printf("\n⚠️  Validation warnings:\n")
			for _, err := range importResult.Validation.Errors {
				fmt.Printf("  ✗ Server '%s': %s\n", err.ServerName, err.Message)
			}
		}
		if importResult.Validation.HasWarnings() {
			for _, warn := range importResult.Validation.Warnings {
				fmt.Printf("  ⚠ Server '%s': %s\n", warn.ServerName, warn.Message)
			}
		}
	}

	return nil
}
