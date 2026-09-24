package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/registry/mcpregistry"
	"github.com/spf13/cobra"
)

// officialRegistryURLEnv overrides the official MCP Registry's base URL.
// It exists for operators who mirror the registry internally and for
// tests (e2e drives the real binary against a local httptest server
// instead of the real network, per issue #313's test plan).
const officialRegistryURLEnv = "LEANPROXY_MCP_REGISTRY_URL"

// applyOfficialRegistryOverride points fetcher at a different official
// registry base URL when officialRegistryURLEnv is set, leaving the
// default (registry.modelcontextprotocol.io) untouched otherwise.
func applyOfficialRegistryOverride(fetcher *registry.FeedFetcher) {
	if base := os.Getenv(officialRegistryURLEnv); base != "" {
		fetcher.WithOfficialClient(mcpregistry.New().WithBaseURL(base))
	}
}

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
	marketplaceCmd.AddCommand(marketplaceSearchCmd)
}

func runMarketplaceSync(cmd *cobra.Command, args []string) error {
	initLogger(cmd)

	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		return fmt.Errorf("determine cache directory: %w", err)
	}

	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)
	applyOfficialRegistryOverride(fetcher)
	if sources := loadCustomRegistrySources(); len(sources) > 0 {
		fetcher.WithCustomSources(sources)
		fmt.Printf("Fetching registry index (official + %d custom source(s))...\n", len(sources))
	} else {
		fmt.Printf("Fetching registry index...\n")
	}

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

// loadCustomRegistrySources reads the opt-in registry.sources block from the
// user's server config, if any. A missing or unparseable config is treated
// as "no custom sources" rather than an error: the official MCP Registry
// sync must still proceed.
func loadCustomRegistrySources() []registry.NamedFeedSource {
	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil || cfg == nil || cfg.Registry == nil {
		return nil
	}
	out := make([]registry.NamedFeedSource, 0, len(cfg.Registry.Sources))
	for _, s := range cfg.Registry.Sources {
		out = append(out, registry.NamedFeedSource{Name: s.Name, URL: s.URL})
	}
	return out
}
