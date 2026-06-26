package cmd

import (
	"fmt"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

var marketplaceSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search MCP Registry servers by name or description",
	Long: `Search the local MCP Registry cache for servers matching the query.

Displays a table with trust score, maintenance status, and download metrics
for each matching server. Scores below 40 are considered low trust.

Usage:
  leanproxy marketplace search <query>

Examples:
  leanproxy marketplace search github
  leanproxy marketplace search database`,
	Args: cobra.ExactArgs(1),
	RunE: runMarketplaceSearch,
}

func runMarketplaceSearch(cmd *cobra.Command, args []string) error {
	initLogger(cmd)

	query := strings.ToLower(strings.TrimSpace(args[0]))

	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		return fmt.Errorf("determine cache directory: %w", err)
	}
	if cacheDir == "" {
		return fmt.Errorf("determine cache directory: empty path")
	}

	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)
	index, err := fetcher.LoadCache()
	if err != nil {
		return fmt.Errorf("load registry cache: %w", err)
	}
	if index == nil || len(index.Entries) == 0 {
		return fmt.Errorf("registry cache is empty. Run `leanproxy marketplace sync` first")
	}

	var matches []registry.RegistryFeedEntry
	for _, e := range index.Entries {
		if strings.Contains(strings.ToLower(e.Name), query) ||
			strings.Contains(strings.ToLower(e.Description), query) {
			matches = append(matches, e)
		}
	}

	if len(matches) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No servers found matching %q\n", args[0])
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "NAME\tTRUST\tLAST RELEASE\tOPEN ISSUES\tDOWNLOADS\tEST. TOKENS/TURN")
	for _, m := range matches {
		trust := registry.CalculateTrustScore(m)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Name,
			registry.FormatTrustLabel(trust),
			registry.FormatString(m.LastRelease),
			registry.FormatInt(m.OpenIssues),
			registry.FormatInt(m.Downloads),
			registry.FormatInt64(m.TokensPerTurn),
		)
	}
	w.Flush()

	return nil
}
