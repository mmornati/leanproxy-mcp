package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

var (
	marketplaceUpdateYes    bool
	marketplaceUpdateDryRun bool

	marketplaceOutdatedCmd = &cobra.Command{
		Use:   "outdated",
		Short: "List installed servers whose registry version has changed",
		Long: `Compare each installed server's pinned version (servers[].installed_from) against
the version currently in the registry cache and list the ones that differ.

Run 'leanproxy marketplace sync' first to refresh the cache.`,
		Args: cobra.NoArgs,
		RunE: runMarketplaceOutdated,
	}

	marketplaceUpdateCmd = &cobra.Command{
		Use:   "update <name>",
		Short: "Update an installed server to the registry's current version",
		Long: `Show the diff between an installed server's pinned command/version and what the
registry currently publishes, then ask for confirmation before rewriting it.

--yes skips the confirmation prompt for scripts; --dry-run only shows the diff.`,
		Args: cobra.ExactArgs(1),
		RunE: runMarketplaceUpdate,
	}
)

func init() {
	marketplaceUpdateCmd.Flags().BoolVarP(&marketplaceUpdateYes, "yes", "y", false, "Skip the confirmation prompt")
	marketplaceUpdateCmd.Flags().BoolVar(&marketplaceUpdateDryRun, "dry-run", false, "Show the diff without writing")
	marketplaceCmd.AddCommand(marketplaceOutdatedCmd)
	marketplaceCmd.AddCommand(marketplaceUpdateCmd)
}

// outdatedServer pairs an installed server with the registry entry it was
// installed from, when the pinned version differs from what the cache now
// has.
type outdatedServer struct {
	name           string
	installed      string
	current        string
	registrySource string
}

func runMarketplaceOutdated(cmd *cobra.Command, args []string) error {
	initLogger(cmd)
	stdout := cmd.OutOrStdout()

	cfg, cache, err := loadInstalledAndCache(cmd)
	if err != nil {
		return err
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		fmt.Fprintln(stdout, "No servers are configured.")
		return nil
	}

	var outdated []outdatedServer
	for _, sc := range cfg.Servers {
		if sc == nil || sc.InstalledFrom == nil || sc.InstalledFrom.Version == "" {
			continue
		}
		entry, found := findFeedEntry(cache.Entries, sc.InstalledFrom.Name)
		if !found || entry.Version == "" {
			continue
		}
		if entry.Version != sc.InstalledFrom.Version {
			outdated = append(outdated, outdatedServer{
				name:           sc.Name,
				installed:      sc.InstalledFrom.Version,
				current:        entry.Version,
				registrySource: sc.InstalledFrom.Registry,
			})
		}
	}

	if len(outdated) == 0 {
		fmt.Fprintln(stdout, "All installed servers are up to date.")
		return nil
	}

	w := tabwriter.NewWriter(stdout, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "name\tregistry\tinstalled\tcurrent")
	for _, o := range outdated {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", o.name, registry.FormatString(o.registrySource), o.installed, o.current)
	}
	w.Flush()
	fmt.Fprintln(stdout, "\nRun `leanproxy marketplace update <name>` to update one.")

	return nil
}

func runMarketplaceUpdate(cmd *cobra.Command, args []string) error {
	initLogger(cmd)
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	name := strings.TrimSpace(args[0])
	if name == "" {
		return fmt.Errorf("server name is required")
	}

	cfg, cache, err := loadInstalledAndCache(cmd)
	if err != nil {
		return err
	}
	if cfg == nil {
		return fmt.Errorf("no servers are configured")
	}

	var current *migrate.ServerConfig
	for _, sc := range cfg.Servers {
		if sc != nil && sc.Name == name {
			current = sc
			break
		}
	}
	if current == nil {
		return fmt.Errorf("server %q is not configured", name)
	}
	lookupName := name
	if current.InstalledFrom != nil && current.InstalledFrom.Name != "" {
		lookupName = current.InstalledFrom.Name
	}

	entry, found := findFeedEntry(cache.Entries, lookupName)
	if !found {
		return fmt.Errorf("server %q was not found in the registry cache; run `leanproxy marketplace sync`", lookupName)
	}

	preview, err := migrate.PreviewServerConfig(migrate.CacheEntry{
		Name:              current.Name,
		Transport:         entry.Transport,
		Command:           entry.Command,
		Args:              entry.Args,
		Env:               entry.Env,
		URL:               entry.URL,
		Version:           entry.Version,
		PackageRegistry:   entry.PackageRegistry,
		PackageIdentifier: entry.PackageIdentifier,
		Registry:          entry.Source,
	})
	if err != nil {
		return fmt.Errorf("preview update for %q: %w", name, err)
	}

	fmt.Fprintf(stdout, "Update %q:\n", name)
	if current.InstalledFrom != nil {
		fmt.Fprintf(stdout, "  Installed version: %s\n", registry.FormatString(current.InstalledFrom.Version))
	}
	fmt.Fprintf(stdout, "  Registry version:  %s\n", registry.FormatString(entry.Version))
	if preview.Stdio != nil {
		fmt.Fprintf(stdout, "  New command:       %s\n", strings.TrimSpace(preview.Stdio.Command+" "+strings.Join(preview.Stdio.Args, " ")))
	} else if preview.HTTP != nil {
		fmt.Fprintf(stdout, "  New URL:           %s\n", preview.HTTP.URL)
	}

	if marketplaceUpdateDryRun {
		fmt.Fprintln(stdout, "Dry-run: no changes were written.")
		return nil
	}

	if !marketplaceUpdateYes {
		if !confirmPrompt(cmd, "Proceed with update? [y/N]: ") {
			fmt.Fprintln(stdout, "Update canceled. Re-run with --yes to update without prompting.")
			return nil
		}
	}

	enabled := false
	if current.Enabled != nil {
		enabled = *current.Enabled
	}
	installer := migrate.NewInstaller(nil, userConfigPath(), slog.Default())
	installCtx := cmd.Context()
	if installCtx == nil {
		installCtx = context.Background()
	}
	result, err := installer.Install(installCtx, migrate.CacheEntry{
		Name:              current.Name,
		Transport:         entry.Transport,
		Command:           entry.Command,
		Args:              entry.Args,
		Env:               entry.Env,
		URL:               entry.URL,
		Version:           entry.Version,
		PackageRegistry:   entry.PackageRegistry,
		PackageIdentifier: entry.PackageIdentifier,
		Registry:          entry.Source,
	}, migrate.InstallOptions{
		Force:   true,
		Logger:  slog.Default(),
		Enabled: enabled,
	})
	if err != nil {
		return fmt.Errorf("update %q: %w", name, err)
	}

	fmt.Fprintf(stdout, "\n✓ Updated %s to %s\n", result.ServerName, registry.FormatString(entry.Version))
	if !enabled {
		fmt.Fprintln(stderr, "Note: server was disabled before the update and remains disabled.")
	}
	return nil
}

// loadInstalledAndCache loads the user's server config and the registry
// cache together, since both `outdated` and `update` need to compare them.
func loadInstalledAndCache(cmd *cobra.Command) (*migrate.Config, *registry.FeedIndex, error) {
	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil && !errors.Is(err, migrate.ErrConfigNotFound) {
		return nil, nil, fmt.Errorf("load config: %w", err)
	}

	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		return nil, nil, fmt.Errorf("determine cache directory: %w", err)
	}
	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)
	if notice := fetcher.CacheStaleInfo(); notice != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", notice)
	}
	cache, err := fetcher.LoadCache()
	if err != nil {
		return nil, nil, fmt.Errorf("load registry cache: %w", err)
	}
	if cache == nil {
		cache = &registry.FeedIndex{}
	}
	return cfg, cache, nil
}
