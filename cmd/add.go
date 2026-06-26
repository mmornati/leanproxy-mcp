package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/spf13/cobra"
)

var (
	addServerForce        bool
	addServerYes          bool
	addServerDryRun       bool
	addServerGracefulWait int
	addServerStopExisting bool
)

// feedSourceAdapter satisfies migrate.ServerSource by reading from a registry
// FeedFetcher. It lives in cmd/ rather than pkg/registry to keep the package
// dependency direction one-way: migrate owns the installer schema, registry
// owns the cache, and cmd stitches them together.
type feedSourceAdapter struct {
	fetcher *registry.FeedFetcher
}

func (a *feedSourceAdapter) LookupCache(_ context.Context) (migrate.CacheSnapshot, error) {
	if a.fetcher == nil {
		return migrate.CacheSnapshot{}, fmt.Errorf("feed source adapter: nil fetcher")
	}
	index, err := a.fetcher.LoadCache()
	if err != nil {
		return migrate.CacheSnapshot{}, fmt.Errorf("feed source adapter: load cache: %w", err)
	}
	if index == nil {
		return migrate.CacheSnapshot{}, nil
	}
	entries := make([]migrate.CacheEntry, 0, len(index.Entries))
	for _, e := range index.Entries {
		entries = append(entries, migrate.CacheEntry{
			Name:          e.Name,
			Transport:     e.Transport,
			Command:       e.Command,
			Args:          append([]string(nil), e.Args...),
			Env:           cloneStringMap(e.Env),
			URL:           e.URL,
			TokensPerTurn: e.TokensPerTurn,
		})
	}
	return migrate.CacheSnapshot{Entries: entries}, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

var addRegistryCmd = &cobra.Command{
	Use:   "add <server-id>",
	Short: "Install an MCP server from the registry",
	Long: `Install an MCP server from the local MCP Registry cache.

The command resolves the registry entry, merges its definition into
leanproxy_servers.yaml, optionally gracefully stops any running instance with
the same name, and prints a token-cost preview of the new server.

If the cache is empty or stale, run 'leanproxy marketplace sync' first.`,
	Args:    cobra.ExactArgs(1),
	Aliases: []string{"install"},
	RunE:    runAdd,
}

func init() {
	addRegistryCmd.Flags().BoolVar(&addServerForce, "force", false, "Overwrite an existing server definition with the same name")
	addRegistryCmd.Flags().BoolVarP(&addServerYes, "yes", "y", false, "Skip the confirmation prompt when overwriting")
	addRegistryCmd.Flags().BoolVar(&addServerDryRun, "dry-run", false, "Preview the install without writing the config")
	addRegistryCmd.Flags().BoolVar(&addServerStopExisting, "stop-existing", true, "Gracefully stop any running server with the same name before replacing")
	addRegistryCmd.Flags().IntVar(&addServerGracefulWait, "graceful-wait", 10, "Seconds to wait for graceful stop before proceeding (0 = no wait)")
	RootCmd.AddCommand(addRegistryCmd)
}

func runAdd(cmd *cobra.Command, args []string) error {
	initLogger(cmd)

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	serverID := strings.TrimSpace(args[0])
	if serverID == "" {
		return fmt.Errorf("server id is required")
	}

	cacheDir, err := registry.LeanProxyDir()
	if err != nil {
		return fmt.Errorf("determine cache directory: %w", err)
	}

	fetcher := registry.NewFeedFetcher(slog.Default(), cacheDir)
	staleNotice := fetcher.CacheStaleInfo()
	if staleNotice != "" {
		fmt.Fprintf(stderr, "Warning: %s\n", staleNotice)
	}

	cache, cacheErr := fetcher.LoadCache()
	if cacheErr != nil {
		return fmt.Errorf("load registry cache: %w", cacheErr)
	}
	if cache == nil || len(cache.Entries) == 0 {
		return fmt.Errorf(
			"registry cache is empty. Run `leanproxy marketplace sync` to populate it, then retry `leanproxy add %s`",
			serverID,
		)
	}

	source := &feedSourceAdapter{fetcher: fetcher}
	installer := migrate.NewInstaller(source, userConfigPath(), slog.Default())

	resolveCtx := cmd.Context()
	if resolveCtx == nil {
		resolveCtx = context.Background()
	}

	entry, resolveErr := installer.Resolve(resolveCtx, serverID)
	if resolveErr != nil {
		if migrate.IsUnknownServer(resolveErr) {
			var unknown *migrate.ErrUnknownServer
			if errors.As(resolveErr, &unknown) {
				fmt.Fprintf(stderr, "Error: %s\n", unknown.Error())
				if len(unknown.Suggested) > 0 {
					fmt.Fprintln(stderr, "\nDid you mean one of:")
					for _, s := range unknown.Suggested {
						fmt.Fprintf(stderr, "  leanproxy add %s\n", s)
					}
				}
				return fmt.Errorf("unknown server %q", serverID)
			}
		}
		return fmt.Errorf("resolve server %q: %w", serverID, resolveErr)
	}

	// Detect the existing-server case so we can prompt before the install.
	existing, existingErr := loadExistingEntry(userConfigPath(), entry.Name)
	if existingErr != nil {
		return fmt.Errorf("inspect existing config: %w", existingErr)
	}
	alreadyInstalled := existing != nil

	if alreadyInstalled && !addServerForce {
		if !addServerYes {
			fmt.Fprintf(stdout,
				"Server %q already exists in %s.\nReplace it? [y/N]: ",
				entry.Name, userConfigPath(),
			)
			var response string
			_, _ = fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				fmt.Fprintln(stdout, "Install canceled. Re-run with --force to overwrite without prompting.")
				return nil
			}
		}
	}

	graceful := time.Duration(addServerGracefulWait) * time.Second
	opts := migrate.InstallOptions{
		Force:           addServerForce || (alreadyInstalled && addServerYes),
		StopExisting:    addServerStopExisting,
		GracefulTimeout: graceful,
		Stopper:         nil, // lifecycle wiring happens in a follow-up; cmd/add stays config-only by default
		Logger:          slog.Default(),
		DryRun:          addServerDryRun || DryRunEnabled,
	}

	installCtx := cmd.Context()
	if installCtx == nil {
		installCtx = context.Background()
	}

	result, err := installer.Install(installCtx, entry, opts)
	if err != nil {
		if migrate.IsAlreadyInstalled(err) {
			return fmt.Errorf("server already installed; re-run with --force to replace")
		}
		return fmt.Errorf("install %q: %w", serverID, err)
	}

	if result.DryRun {
		fmt.Fprintln(stdout, "Dry-run: no changes were written.")
	}

	snapshot := bouncer.ComputeSnapshot(
		entry.Name, string(result.Transport),
		bouncer.EstimateToolsFromDescription(descriptionFor(entry)),
		entry.TokensPerTurn,
	)
	fmt.Fprintln(stdout, bouncer.FormatSnapshot(snapshot))

	fmt.Fprintf(stdout, "\n✓ Installed %s (%s)\n", result.ServerName, result.Transport)
	fmt.Fprintf(stdout, "  Config: %s\n", result.ConfigPath)
	if result.Replaced {
		fmt.Fprintln(stdout, "  Replaced: yes")
	}
	if result.Stopped {
		fmt.Fprintln(stdout, "  Graceful stop: yes")
	}

	return nil
}

// loadExistingEntry returns a non-nil pointer (true presence) when the config
// already has a server with name, nil otherwise. Errors are returned for
// I/O / parse failures only.
func loadExistingEntry(path, name string) (*migrate.ServerConfig, error) {
	cfg, err := migrate.LoadConfig(context.Background(), path)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	for _, s := range cfg.Servers {
		if s != nil && s.Name == name {
			return s, nil
		}
	}
	return nil, nil
}

func descriptionFor(entry migrate.CacheEntry) string {
	if v := strings.TrimSpace(entry.URL); v != "" {
		return v
	}
	if len(entry.Args) > 0 {
		return strings.Join(entry.Args, " ")
	}
	return entry.Command
}
