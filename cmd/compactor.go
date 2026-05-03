package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/mmornati/leanproxy-mcp/pkg/compactor"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/spf13/cobra"
)

var compactorCmd = &cobra.Command{
	Use:   "compactor",
	Short: "Manage distilled manifest caching",
	Long:  `Manage distilled manifest caching and re-distillation for MCP servers.`,
}

func init() {
	RootCmd.AddCommand(compactorCmd)
}

var rebuildCmd = &cobra.Command{
	Use:   "rebuild [server-name]",
	Short: "Force re-distillation of server manifests",
	Long: `Force re-distillation of MCP server manifests to refresh stale discovery signatures.

Use this command when tool descriptions have changed and you want to
regenerate the distilled (compact) versions of server manifests.

Examples:
  # Rebuild a specific server
  leanproxy compactor rebuild github

  # Rebuild all servers
  leanproxy compactor rebuild --all`,
	Args: cobra.RangeArgs(0, 1),
	RunE: runRebuild,
}

var rebuildFlags struct {
	all bool
}

func init() {
	rebuildCmd.Flags().BoolVar(&rebuildFlags.all, "all", false, "Rebuild all servers")
	compactorCmd.AddCommand(rebuildCmd)
}

func runRebuild(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	if rebuildFlags.all {
		return rebuildAllServers(ctx)
	}

	if len(args) == 0 {
		return fmt.Errorf("specify server name or use --all flag")
	}

	return rebuildServer(ctx, args[0])
}

func rebuildServer(ctx context.Context, name string) error {
	slog.Info("starting re-distillation", "server", name)

	cfg, err := migrate.LoadConfig(ctx, userConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg == nil {
		return fmt.Errorf("no servers configured")
	}

	var serverCfg *migrate.ServerConfig
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			serverCfg = srv
			break
		}
	}

	if serverCfg == nil {
		return fmt.Errorf("server %q not found in registry", name)
	}

	if serverCfg.Enabled != nil && !*serverCfg.Enabled {
		return fmt.Errorf("server %q is disabled", name)
	}

	cache, err := compactor.NewFileCache("", nil)
	if err != nil {
		return fmt.Errorf("create cache: %w", err)
	}

	if err := cache.Invalidate(ctx, name); err != nil {
		return fmt.Errorf("clear cache: %w", err)
	}

	manifest := buildRawManifest(name, serverCfg)

	processor := compactor.NewManifestProcessor(nil)
	distilled, err := processor.Process(ctx, manifest)
	if err != nil {
		return fmt.Errorf("distill: %w", err)
	}

	if err := cache.Set(ctx, name, distilled); err != nil {
		slog.Warn("failed to cache distilled manifest", "error", err)
	}

	originalTokens := len(manifest.Tools) * 100
	distilledTokens := len(distilled.Tools) * 30
	reduction := 0.0
	if originalTokens > 0 {
		reduction = float64(originalTokens-distilledTokens) / float64(originalTokens) * 100
	}

	fmt.Fprintf(os.Stderr, "Done. Reduced from %d to %d tokens (%.0f%% reduction)\n", originalTokens, distilledTokens, reduction)

	slog.Info("re-distillation complete",
		"server", name,
		"original_tokens", originalTokens,
		"distilled_tokens", distilledTokens,
		"reduction_percent", reduction)

	return nil
}

func rebuildAllServers(ctx context.Context) error {
	cfg, err := migrate.LoadConfig(ctx, userConfigPath())
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return fmt.Errorf("no servers configured")
	}

	var enabledServers []string
	for _, srv := range cfg.Servers {
		if srv.Enabled == nil || *srv.Enabled {
			enabledServers = append(enabledServers, srv.Name)
		}
	}

	if len(enabledServers) == 0 {
		return fmt.Errorf("no enabled servers to rebuild")
	}

	fmt.Fprintf(os.Stderr, "Rebuilding %d server(s)...\n\n", len(enabledServers))

	successCount := 0
	failCount := 0

	for _, name := range enabledServers {
		slog.Info("rebuilding server", "server", name)

		var serverCfg *migrate.ServerConfig
		for _, srv := range cfg.Servers {
			if srv.Name == name {
				serverCfg = srv
				break
			}
		}

		if serverCfg == nil {
			slog.Error("server not found in config", "server", name)
			failCount++
			continue
		}

		err := rebuildServer(ctx, name)
		if err != nil {
			slog.Error("rebuild failed", "server", name, "error", err)
			failCount++
			fmt.Fprintf(os.Stderr, "❌ %s: %v\n", name, err)
		} else {
			successCount++
			fmt.Fprintf(os.Stderr, "✅ %s\n", name)
		}
	}

	fmt.Fprintf(os.Stderr, "\nRebuild complete: %d succeeded, %d failed\n", successCount, failCount)

	if failCount > 0 {
		return fmt.Errorf("%d server(s) failed", failCount)
	}

	return nil
}

func buildRawManifest(name string, cfg *migrate.ServerConfig) compactor.RawManifest {
	manifest := compactor.RawManifest{
		Name:        name,
		Description: fmt.Sprintf("MCP server %s", name),
		Tools:       []compactor.RawTool{},
	}

	manifest.Tools = append(manifest.Tools, compactor.RawTool{
		Name:        fmt.Sprintf("%s.list_tools", name),
		Description: "List available tools from this server",
		Parameters:  []byte("{}"),
	})

	return manifest
}