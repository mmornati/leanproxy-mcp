package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

var (
	migrateYes    bool
	migrateDryRun bool
	migrateTarget string
)

var migrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Auto-detect and import MCP server configurations from other tools",
	Long: `Scan for existing MCP configurations from OpenCode, Claude Code, VS Code, and Cursor.
Import discovered servers into leanproxy_servers.yaml with proper conflict resolution.`,
	RunE: runMigrate,
}

func init() {
	migrateCmd.Flags().BoolVar(&migrateYes, "yes", false, "Skip confirmation prompt")
	migrateCmd.Flags().BoolVar(&migrateDryRun, "dry-run", false, "Preview scan results without importing")
	migrateCmd.Flags().StringVar(&migrateTarget, "target", "", "Target config file path (default: ~/.config/leanproxy_servers.yaml)")
	RootCmd.AddCommand(migrateCmd)
}

func runMigrate(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	migrator := migrate.NewMigrator()

	result, err := migrator.Scan(ctx)
	if err != nil {
		return fmt.Errorf("scan failed: %w", err)
	}

	if len(result.Servers) == 0 {
		fmt.Println("No MCP configurations found on this system.")
		fmt.Println("To add servers manually, use: leanproxy server add")
		return nil
	}

	summary := migrator.Summarize(result.Servers)

	fmt.Printf("Found %d MCP server(s) from %d source(s):\n\n", summary.TotalServers, len(result.Scanners))
	fmt.Printf("  OpenCode: %d server(s)\n", summary.OpenCodeCount)
	fmt.Printf("  Claude:   %d server(s)\n", summary.ClaudeCount)
	fmt.Printf("  VS Code:  %d server(s)\n", summary.VSCodeCount)
	fmt.Printf("  Cursor:   %d server(s)\n", summary.CursorCount)
	fmt.Printf("  Generic:  %d server(s)\n\n", summary.GenericCount)

	for i, srv := range result.Servers {
		cmdStr := ""
		if srv.Stdio != nil {
			cmdStr = srv.Stdio.Command
		}
		fmt.Printf("  [%d] %s (%s) - %s\n", i+1, srv.Name, srv.Source, cmdStr)
	}

	if migrateDryRun {
		fmt.Println("\nDry-run mode: no changes were made.")
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
		fmt.Scanln(&response)
		if response != "y" && response != "Y" {
			fmt.Println("Import cancelled.")
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

	return nil
}