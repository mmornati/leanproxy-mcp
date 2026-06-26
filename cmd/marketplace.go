package cmd

import (
	"fmt"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

var marketplaceCmd = &cobra.Command{
	Use:   "marketplace",
	Short: "Interact with the MCP Registry marketplace",
	Long:  `Manage the local MCP Registry cache: sync the latest server index, inspect cached entries, and discover available servers.`,
}

var marketplaceSyncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Fetch and cache the MCP Registry index",
	Long: `Download the latest MCP Registry server index and store it locally.
The cached index is used by marketplace commands and kept up-to-date via periodic refresh.

Usage:
  leanproxy marketplace sync`,
	Args: cobra.NoArgs,
	RunE: runMarketplaceSync,
}

func init() {
	RootCmd.AddCommand(marketplaceCmd)
	marketplaceCmd.AddCommand(marketplaceSyncCmd)
}

func runMarketplaceSync(cmd *cobra.Command, args []string) error {
	initLogger(cmd)

	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		return fmt.Errorf("determine cache directory: %w", err)
	}
	if cacheDir == "" {
		return fmt.Errorf("determine cache directory: empty path")
	}

	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)

	fmt.Printf("Fetching registry index...\n")
	if err := fetcher.Sync(cmd.Context()); err != nil {
		return fmt.Errorf("sync failed: %w", err)
	}

	index, err := fetcher.LoadCache()
	switch {
	case err != nil:
		slog.Warn("registry feed: post-sync cache read failed", "error", err)
	case index != nil:
		fmt.Printf("Registry index synced successfully (%d entries)\n", len(index.Entries))
		fmt.Printf("Cache stored at: %s\n", fetcher.IndexPath())
	}

	return nil
}
