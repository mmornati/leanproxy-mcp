package cmd

import (
	"fmt"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

const (
	searchDefaultLimit = 25
	searchMaxLimit     = 200
)

var (
	searchLimit          int
	marketplaceSearchCmd = &cobra.Command{
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
)

func init() {
	marketplaceSearchCmd.Flags().IntVar(&searchLimit, "limit", searchDefaultLimit,
		fmt.Sprintf("Maximum number of rows to display (1-%d)", searchMaxLimit))
}

func runMarketplaceSearch(cmd *cobra.Command, args []string) error {
	initLogger(cmd)

	rawQuery := strings.TrimSpace(args[0])
	if rawQuery == "" {
		return fmt.Errorf("search query must not be empty")
	}
	query := strings.ToLower(rawQuery)

	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		return fmt.Errorf("determine cache directory: %w", err)
	}

	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)
	if notice := fetcher.CacheStaleInfo(); notice != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", notice)
	}

	index, err := fetcher.LoadCache()
	if err != nil {
		return fmt.Errorf("load registry cache: %w", err)
	}
	if index == nil || len(index.Entries) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(),
			"Registry cache is empty. Run `leanproxy marketplace sync` to populate it, then retry this search.\n")
		return nil
	}

	limit := searchLimit
	if limit < 1 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}

	var matches []registry.RegistryFeedEntry
	for _, e := range index.Entries {
		if strings.Contains(strings.ToLower(e.Name), query) ||
			strings.Contains(strings.ToLower(e.Description), query) {
			matches = append(matches, e)
			if len(matches) >= limit {
				break
			}
		}
	}

	if len(matches) == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "No servers found matching %q\n", rawQuery)
		return nil
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "name\ttrust\tlast release\topen issues\tdownloads\test tokens/turn")
	for _, m := range matches {
		trust := registry.CalculateTrustScore(m)
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Name,
			registry.FormatTrustLabel(trust),
			registry.FormatLastRelease(m.LastRelease),
			registry.FormatInt(m.OpenIssues),
			registry.FormatInt(m.Downloads),
			registry.FormatInt64(m.TokensPerTurn),
		)
	}
	w.Flush()

	return nil
}
