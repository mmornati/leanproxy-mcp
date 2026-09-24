package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/streamhttp"
	"github.com/mmornati/leanproxy-mcp/pkg/toolsearch"
	"github.com/mmornati/leanproxy-mcp/pkg/toolstore"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Manage MCP server configurations",
	Long:  `Add, remove, list, enable, or disable MCP servers in leanproxy_servers.yaml`,
}

func init() {
	RootCmd.AddCommand(serverCmd)
}

func userConfigPath() string {
	if path := os.Getenv("LEANPROXY_CONFIG"); path != "" {
		return path
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return filepath.Join(home, ".config", "leanproxy_servers.yaml")
}

var addCmd = &cobra.Command{
	Use:   "add <name> <command> [args...]",
	Short: "Add a new MCP server",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runServerAdd,
}

var addFlags struct {
	env       []string
	cwd       string
	transport string
}

func init() {
	addCmd.Flags().StringArrayVar(&addFlags.env, "env", []string{}, "Environment variables (KEY=value)")
	addCmd.Flags().StringVar(&addFlags.cwd, "cwd", "", "Working directory for the command")
	addCmd.Flags().StringVar(&addFlags.transport, "transport", "stdio", "Transport type (stdio, http, sse)")
	serverCmd.AddCommand(addCmd)
}

func runServerAdd(cmd *cobra.Command, args []string) error {
	name := args[0]
	command := args[1]
	commandArgs := args[2:]

	_, err := exec.LookPath(command)
	if err != nil {
		return fmt.Errorf("command not found in PATH: %s", command)
	}

	transport := registry.TransportType(addFlags.transport)
	switch transport {
	case registry.TransportStdio, registry.TransportHTTP, registry.TransportSSE:
	default:
		return fmt.Errorf("invalid transport type: %s (must be stdio, http, or sse)", addFlags.transport)
	}

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil {
		cfg = &migrate.Config{
			Version: "1.0",
			Servers: []*migrate.ServerConfig{},
		}
	}

	for _, srv := range cfg.Servers {
		if srv.Name == name {
			return fmt.Errorf("server %q already exists", name)
		}
	}

	stdio := &migrate.StdioConfig{
		Command: command,
		Args:    commandArgs,
		CWD:     addFlags.cwd,
		Env:     addFlags.env,
	}
	if stdio.CWD == "" {
		stdio.CWD = filepath.Dir(command)
	}

	enabled := true
	newServer := &migrate.ServerConfig{
		Name:           name,
		Transport:      transport,
		Stdio:          stdio,
		Enabled:        &enabled,
		Timeout:        "30s",
		ConnectTimeout: "10s",
	}

	if transport != registry.TransportStdio {
		newServer.Stdio = nil
	}

	cfg.Servers = append(cfg.Servers, newServer)

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q added successfully\n", name)
	return nil
}

var removeCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runServerRemove,
}

func init() {
	serverCmd.AddCommand(removeCmd)
}

func runServerRemove(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return fmt.Errorf("no servers configured")
	}

	found := -1
	for i, srv := range cfg.Servers {
		if srv.Name == name {
			found = i
			break
		}
	}

	if found == -1 {
		return fmt.Errorf("server %q not found", name)
	}

	fmt.Printf("Remove server %q? [y/N]: ", name)
	var response string
	_, _ = fmt.Scanln(&response) // EOF / empty input means "no"
	if response != "y" && response != "Y" {
		fmt.Println("Canceled.")
		return nil
	}

	cfg.Servers = append(cfg.Servers[:found], cfg.Servers[found+1:]...)

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q removed successfully\n", name)
	return nil
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured MCP servers",
	RunE:  runServerList,
}

var listFlags struct {
	source string
}

func init() {
	listCmd.Flags().StringVar(&listFlags.source, "source", "", "Filter by source (opencode, claude, vscode, cursor, generic)")
	serverCmd.AddCommand(listCmd)
}

func runServerList(cmd *cobra.Command, args []string) error {
	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		fmt.Println("No servers configured.")
		return nil
	}

	fmt.Printf("%-20s %-10s %-15s %s\n", "NAME", "STATUS", "TRANSPORT", "COMMAND")
	fmt.Println("--------------------------------------------------------------")

	for _, srv := range cfg.Servers {
		status := "enabled"
		if srv.Enabled != nil && !*srv.Enabled {
			status = "disabled"
		}

		cmdStr := ""
		if srv.Stdio != nil {
			cmdStr = srv.Stdio.Command
			if len(srv.Stdio.Args) > 0 {
				cmdStr += " " + joinStrings(srv.Stdio.Args)
			}
		} else if srv.HTTP != nil {
			cmdStr = srv.HTTP.URL
		}

		fmt.Printf("%-20s %-10s %-15s %s\n", srv.Name, status, srv.Transport, cmdStr)
	}

	fmt.Printf("\n%d server(s)\n", len(cfg.Servers))
	return nil
}

var enableCmd = &cobra.Command{
	Use:   "enable <name>",
	Short: "Enable a disabled MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runServerEnable,
}

func init() {
	serverCmd.AddCommand(enableCmd)
}

func runServerEnable(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil {
		return fmt.Errorf("no servers configured")
	}

	found := false
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			enabled := true
			srv.Enabled = &enabled
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("server %q not found", name)
	}

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q enabled\n", name)
	return nil
}

var disableCmd = &cobra.Command{
	Use:   "disable <name>",
	Short: "Disable an MCP server",
	Args:  cobra.ExactArgs(1),
	RunE:  runServerDisable,
}

func init() {
	serverCmd.AddCommand(disableCmd)
}

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run leanproxy-mcp as an MCP server (stdio, or Streamable HTTP)",
	Long: `Run leanproxy-mcp as a Model Context Protocol server that proxies
requests to configured MCP servers.

With --stdio it reads JSON-RPC requests from stdin and writes responses to
stdout: one client, the IDE that spawned it.

With --http <host:port> it serves the MCP Streamable HTTP transport at
http://<host:port>/mcp: one shared local gateway that any number of MCP
clients reach by URL, all sharing one set of child servers. It binds
loopback addresses by default, requires a bearer token (the serve token:
--http-token, else $LEANPROXY_SERVE_TOKEN, else
~/.config/leanproxy/serve.token, generated on first start), and rejects
requests whose Host or Origin header is not allowed. --no-auth drops the
token, on a loopback address only.

Exactly one of --stdio and --http is required.

Example:
  leanproxy-mcp server run --stdio
  leanproxy-mcp server run --stdio --config /path/to/config.yaml
  leanproxy-mcp server run --stdio --log-file /tmp/leanproxy.log
  leanproxy-mcp server run --http 127.0.0.1:8765
  leanproxy-mcp server run --http 127.0.0.1:8765 --http-allowed-origins https://app.example
  leanproxy-mcp server run --stdio --exposure passthrough

Exposure: clients with native tool search (Claude Code, Claude Desktop,
Cursor, VS Code) get every upstream tool listed as <server>__<tool>
(passthrough); any other client gets LeanProxy's discovery router
(search_tools, invoke_tool, ...). --exposure forces one mode for every
client; the exposure: config block sets it per client.`,
	RunE: runServerRun,
}

var runFlags struct {
	stdio              bool
	config             string
	logFile            string
	logLevel           string
	verbose            bool
	http               string
	httpToken          string
	httpAllowedHosts   []string
	httpAllowedOrigins []string
	noAuth             bool
	exposure           string
}

func init() {
	runCmd.Flags().BoolVar(&runFlags.stdio, "stdio", false, "Run in stdio mode (read JSON-RPC from stdin)")
	runCmd.Flags().StringVar(&runFlags.config, "config", "", "Path to leanproxy_servers.yaml config file")
	runCmd.Flags().StringVar(&runFlags.logFile, "log-file", "", "Path to log file")
	runCmd.Flags().StringVar(&runFlags.logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	runCmd.Flags().BoolVarP(&runFlags.verbose, "verbose", "v", false, "Enable verbose logging")
	runCmd.Flags().StringVar(&runFlags.http, "http", "", "Serve the MCP Streamable HTTP transport on this address (e.g. 127.0.0.1:8765) instead of stdio")
	runCmd.Flags().StringVar(&runFlags.httpToken, "http-token", "", "Bearer token HTTP clients must send (default: $"+serveTokenEnv+", else ~/.config/leanproxy/serve.token, generated on first start)")
	runCmd.Flags().StringSliceVar(&runFlags.httpAllowedHosts, "http-allowed-hosts", nil, "Extra Host header values accepted by the HTTP front end, beyond the bind host and loopback names (adds to server.http.allowed_hosts)")
	runCmd.Flags().StringSliceVar(&runFlags.httpAllowedOrigins, "http-allowed-origins", nil, "Browser origins (https://app.example) allowed to call the HTTP front end (adds to server.http.allowed_origins)")
	runCmd.Flags().BoolVar(&runFlags.noAuth, "no-auth", false, "Serve --http without a bearer token (only allowed on a loopback address)")
	runCmd.Flags().StringVar(&runFlags.exposure, "exposure", "", "Force how the upstream tools are exposed to every client: router, passthrough or hybrid (default: per client, see exposure: in the config)")
	serverCmd.AddCommand(runCmd)

	var healthCmd = &cobra.Command{
		Use:   "health <server_name>",
		Short: "Check if an MCP server is healthy and responding",
		Args:  cobra.ExactArgs(1),
		RunE:  runServerHealth,
	}
	healthCmd.Flags().StringVar(&runFlags.config, "config", "", "Path to leanproxy_servers.yaml config file")
	healthCmd.Flags().DurationVar(&healthTimeout, "timeout", 10*time.Second, "Health check timeout")
	serverCmd.AddCommand(healthCmd)
}

func runServerRun(cmd *cobra.Command, args []string) error {
	initLogger(cmd)

	if err := checkRunModeFlags(runFlags.stdio, runFlags.http, runFlags.httpToken, runFlags.noAuth,
		len(runFlags.httpAllowedHosts)+len(runFlags.httpAllowedOrigins) > 0); err != nil {
		return err
	}

	configPath := runFlags.config
	if configPath == "" {
		configPath = userConfigPath()
	}

	ctx := context.Background()

	cfg, err := migrate.LoadConfig(ctx, configPath)
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return fmt.Errorf("no servers configured in %s", configPath)
	}

	// Exposure modes (#322): resolved (and the flag validated) before
	// anything starts.
	exposureResolver, err := newExposureResolver(cfg, runFlags.exposure)
	if err != nil {
		return err
	}

	// The HTTP front end's security settings are resolved before anything
	// starts: a non-loopback address without a token is refused, and the
	// token file is created on first start.
	var httpOpts *streamhttp.Options
	if runFlags.http != "" {
		if err := checkHTTPOrigins(runFlags.httpAllowedOrigins); err != nil {
			return err
		}
		home, _ := os.UserHomeDir()
		token, source, err := httpAuthSettings(runFlags.http, runFlags.httpToken, runFlags.noAuth, os.Getenv(serveTokenEnv), home)
		if err != nil {
			return err
		}
		opts := httpFrontendOptions(cfg, runFlags.http, token, runFlags.httpAllowedHosts, runFlags.httpAllowedOrigins)
		if err := streamhttp.CheckOptions(opts); err != nil {
			return err
		}
		if token == "" {
			slog.Warn("HTTP front end authentication disabled (--no-auth): any local process can drive the upstream servers", "listen", runFlags.http)
		} else {
			slog.Info("HTTP front end authentication enabled", "token_source", source)
		}
		httpOpts = &opts
	}

	telemetryProvider := initTelemetry(ctx, cfg)

	stdioPool := pool.NewStdioPool(5, 5*time.Minute, slog.Default())
	httpPool := pool.NewHTTPClientPool(slog.Default())
	ssePool := pool.NewSSEPool(slog.Default())
	unifiedPool := pool.NewUnifiedPool(stdioPool, httpPool, ssePool, slog.Default())

	reconnect := cfg.EffectiveReconnect()
	// Always push the reconnect settings: the Disabled flag is what makes
	// reconnect.enabled=false a real master switch for crash auto-restart,
	// instead of silently keeping the built-in defaults.
	stdioPool.SetReconnect(pool.ReconnectSettings{
		Disabled:           !reconnect.Enabled,
		MaxRestartAttempts: reconnect.MaxRestartAttempts,
		RestartBackoff:     reconnect.RestartBackoff,
		StableWindow:       reconnect.StableWindow,
	})

	startedCount := 0
	for _, srv := range cfg.Servers {
		if srv.Enabled != nil && !*srv.Enabled {
			slog.Debug("server disabled, skipping", "name", srv.Name)
			continue
		}
		switch srv.Transport {
		case registry.TransportStdio:
			if err := stdioPool.StartServer(ctx, srv); err != nil {
				slog.Warn("failed to start stdio server", "name", srv.Name, "error", err)
			} else {
				startedCount++
				slog.Info("stdio server started", "name", srv.Name)
			}
		case registry.TransportHTTP:
			if err := httpPool.StartServer(ctx, srv); err != nil {
				slog.Warn("failed to start HTTP server", "name", srv.Name, "error", err)
			} else {
				startedCount++
				slog.Info("HTTP server started", "name", srv.Name)
			}
		case registry.TransportSSE:
			if err := ssePool.StartServer(ctx, srv); err != nil {
				slog.Warn("failed to start SSE server", "name", srv.Name, "error", err)
			} else {
				startedCount++
				slog.Info("SSE server started", "name", srv.Name)
			}
		}
	}

	if startedCount == 0 {
		slog.Warn("no servers started")
	}

	var healthChecker *pool.HealthChecker
	var healthCancel context.CancelFunc
	if reconnect.Enabled && reconnect.HealthInterval > 0 {
		healthChecker = pool.NewHealthChecker(stdioPool, slog.Default())
		healthChecker.SetMaxFailures(reconnect.MaxFailures)
		healthCtx, cancel := context.WithCancel(context.Background())
		healthCancel = cancel
		go healthChecker.Start(healthCtx, reconnect.HealthInterval)
		slog.Info("auto-reconnect enabled",
			"interval", reconnect.HealthInterval,
			"max_failures", reconnect.MaxFailures,
			"max_restart_attempts", reconnect.MaxRestartAttempts,
			"restart_backoff", reconnect.RestartBackoff,
			"stable_window", reconnect.StableWindow)
	}

	var cache toolstore.Cache
	fileCache, err := toolstore.NewFileCache(slog.Default())
	if err != nil {
		slog.Warn("failed to create tool cache, using no-op cache", "error", err)
		cache = toolstore.NewNoOpCache()
	} else {
		cache = fileCache
	}

	statusListen := "stdio"
	if httpOpts != nil {
		statusListen = "http://" + httpOpts.Addr + streamhttp.DefaultEndpoint
	}
	statusStore, err := statusfile.NewFileStatusStore(statusListen, slog.Default())
	if err != nil {
		slog.Warn("failed to create status store", "error", err)
	} else {
		slog.Info("status file enabled", "path", statusStore.GetFilePath())
		updateStdioServerStatusOnce(statusStore, stdioPool)
	}
	frontendLabel := "stdio"
	if httpOpts != nil {
		frontendLabel = "http"
	}
	initUsageStore(frontendLabel)
	flushUsageSnapshot()

	// Response token governor (#319), off by default. Built before
	// closePools so shutdown removes its spilled results.
	gov := mcp.NewGovernor(cfg.Response)

	// closePools stops the health checker before closing the pools so no
	// health-triggered restart can race the shutdown sweep and orphan a
	// freshly spawned process. It runs once, from the signal handler or
	// after the stdio front end returns (EOF / shutdown).
	refreshCtx, stopRefresh := context.WithCancel(ctx)
	var closeOnce sync.Once
	closePools := func() {
		closeOnce.Do(func() {
			stopRefresh()
			if healthCancel != nil {
				healthCancel()
			}
			if healthChecker != nil {
				healthChecker.Stop()
			}
			stdioPool.Close()
			httpPool.Close()
			ssePool.Close()
			gov.Close()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := telemetryProvider.Shutdown(shutdownCtx); err != nil {
				slog.Warn("telemetry: shutdown error", "error", err)
			}
		})
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	if httpOpts == nil {
		go func() {
			<-sigChan
			slog.Info("shutting down server")
			flushUsageSnapshot()
			if statusStore != nil {
				statusStore.RemoveFile()
			}
			closePools()
			os.Exit(0)
		}()
	}

	if statusStore != nil {
		go updateServerStatus(statusStore, unifiedPool, stdioPool)
	}

	handler := mcp.NewHandlerWithToolStore(unifiedPool, slog.Default(), cache)
	handler.SetExposure(exposureResolver)
	for _, srv := range cfg.Servers {
		if srv.TimeoutValue > 0 {
			handler.SetTimeout(srv.Name, srv.TimeoutValue)
		}
	}

	// search_tools index (#305): BM25 by default, hybrid (BM25 + embedding
	// similarity) only when tool_search.hybrid is enabled.
	configureToolSearch(refreshCtx, handler, cfg.ToolSearch)

	// Tool pinning (#310): every refresh below is compared with the pins
	// before its tools reach the cache.
	pins := mcp.NewToolPins(newToolPinner(cfg))
	handler.SetToolPins(pins)
	slog.Info(pins.Summary())

	// Serve immediately from the persistent tool cache and refresh every
	// server's tools in the background (#297): no request ever waits for
	// another server's tools/list.
	handler.LoadPersistentToolCache()
	handler.StartBackgroundRefresh(refreshCtx)

	// Token Firewall: the same redaction + injection middlewares `serve`
	// runs, on by default (built-in patterns when there is no bouncer block).
	firewall := mcp.NewFirewall(cfg.Bouncer, cfg.Injection)

	// Response cache (issue #299), off by default. It MUST be installed
	// outermost, ahead of the firewall middlewares: see the ordering
	// explanation on mcp.ResponseCache.
	respCache := mcp.NewResponseCache(cfg.ResponseCache)
	respCache.SetToolSource(handler)

	// Per-tool policy (#314): allow / deny / confirm per tool, and no
	// calls to tools a server does not advertise.
	pol := newPolicy(cfg, firewall)
	handler.SetPolicy(pol)
	slog.Info(pol.Summary())
	gov.SetInjectionGuard(firewall.Injection)
	handler.SetGovernor(gov)
	slog.Info(gov.Summary())
	handler.Use(tracedMiddlewares(handler, respCache, firewall, pins, pol, gov)...)

	// Server-to-client requests, progress and resource updates from the
	// upstreams (#308): per-server policy (allow_sampling, roots), the same
	// firewall on relayed traffic.
	handler.ConfigureRelay(cfg.Servers)
	handler.SetRelayFirewall(firewall)
	handler.AttachUpstreamRelay(refreshCtx)
	logFirewallStatus(firewall)
	logResponseCacheStatus(respCache)
	metrics.SetResponseCacheProvider(func() metrics.ResponseCacheMetric {
		return toMetricsResponseCache(respCache)
	})
	metrics.SetResponseGovernorProvider(gov.Stats)

	if httpOpts != nil {
		return handleHTTP(handler, *httpOpts, sigChan, closePools, statusStore)
	}

	frontendOpts := stdioFrontendOptions{
		MaxConcurrent: cfg.EffectiveMaxConcurrentRequests(),
		ShutdownGrace: defaultStdioShutdownGrace,
		MaxLineBytes:  cfg.EffectiveMaxLineBytes(),
	}
	return handleStdio(ctx, handler, frontendOpts, closePools, statusStore)
}

// configureToolSearch builds the handler's search_tools index from the
// `tool_search:` config block and, when hybrid mode is enabled, starts the
// background embedding of tool descriptions (stopped with ctx). A hybrid
// block whose embedder cannot be created leaves search_tools on BM25.
func configureToolSearch(ctx context.Context, handler *mcp.Handler, cfg *toolsearch.Config) {
	ix := handler.ConfigureToolSearch(cfg.Options())
	if !cfg.HybridEnabled() {
		return
	}
	emb, err := embedder.NewFromConfig(cfg.Hybrid.Embedder, slog.Default())
	if err != nil {
		slog.Warn("tool_search.hybrid: embedder unavailable, search_tools uses BM25 only", "error", err)
		return
	}
	context.AfterFunc(ctx, func() { _ = emb.Close() })
	ix.EnableHybrid(ctx, toolSearchEmbedder{emb})
	slog.Info("tool search: hybrid ranking enabled", "provider", cfg.Hybrid.Embedder.Provider)
}

// toolSearchEmbedder adapts a pkg/cache/embedder client to
// toolsearch.Embedder.
type toolSearchEmbedder struct{ emb embedder.Embedder }

func (a toolSearchEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	out, err := a.emb.Embed(ctx, embedder.EmbedRequest{ToolName: text})
	if err != nil {
		return nil, err
	}
	return out.Vector, nil
}

// logFirewallStatus logs the one-line firewall summary at startup, plus a
// warning when redaction was explicitly turned off.
func logFirewallStatus(fw *mcp.Firewall) {
	slog.Info(fw.Summary())
	if !fw.Redaction.Enabled() {
		slog.Warn("bouncer: secret redaction explicitly disabled in config; secrets will pass through verbatim")
	}
}

// logResponseCacheStatus logs whether the response cache (issue #299) is
// active. It stays silent when disabled, since that is the default.
func logResponseCacheStatus(rc *mcp.ResponseCache) {
	if rc.Enabled() {
		slog.Info("response cache enabled")
	}
}

// toMetricsResponseCache adapts a *mcp.ResponseCache's stats to the metrics
// package's own type, so pkg/metrics never needs to import pkg/mcp.
func toMetricsResponseCache(rc *mcp.ResponseCache) metrics.ResponseCacheMetric {
	s := rc.Stats()
	return metrics.ResponseCacheMetric{
		Enabled:   rc.Enabled(),
		Hits:      s.Hits,
		Misses:    s.Misses,
		Evictions: s.Evictions,
		Bytes:     s.Bytes,
		Entries:   s.Entries,
	}
}

func updateStdioServerStatusOnce(statusStore *statusfile.FileStatusStore, stdioPool *pool.StdioPool) {
	if statusStore == nil || stdioPool == nil {
		return
	}

	servers := stdioPool.ListServers()
	statuses := make([]statusfile.ServerStatus, 0, len(servers))

	for _, name := range servers {
		state, _ := stdioPool.GetServerState(name)
		stats, _ := stdioPool.GetServerStats(name)

		status := statusfile.ServerStatus{
			Name:         name,
			RequestCount: stats.RequestCount,
			ErrorCount:   stats.ErrorCount,
			RestartCount: stats.RestartCount,
		}

		switch state {
		case pool.StateIdle, pool.StateRunning, pool.StateBusy:
			status.Status = "running"
		case pool.StateError:
			status.Status = "error"
		case pool.StateStopped, pool.StateStopping:
			status.Status = "stopped"
		default:
			status.Status = "unknown"
		}

		statuses = append(statuses, status)
	}

	statusStore.UpdateServers(statuses)
}

func updateServerStatus(statusStore *statusfile.FileStatusStore, unifiedPool pool.ServerSource, stdioPool *pool.StdioPool) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if statusStore == nil || unifiedPool == nil {
			continue
		}

		servers := unifiedPool.ListServers()
		statuses := make([]statusfile.ServerStatus, 0, len(servers))

		for _, name := range servers {
			state, _ := unifiedPool.GetServerState(name)

			stats := pool.ServerStats{}
			stdioStats, err := stdioPool.GetServerStats(name)
			if err == nil {
				stats = stdioStats
			}

			status := statusfile.ServerStatus{
				Name:         name,
				RequestCount: stats.RequestCount,
				ErrorCount:   stats.ErrorCount,
				RestartCount: stats.RestartCount,
			}

			switch state {
			case pool.StateIdle, pool.StateRunning, pool.StateBusy:
				status.Status = "running"
			case pool.StateError:
				status.Status = "error"
			case pool.StateStopped, pool.StateStopping:
				status.Status = "stopped"
			default:
				status.Status = "unknown"
			}

			statuses = append(statuses, status)
		}

		statusStore.UpdateServers(statuses)
		flushUsageSnapshot()
	}
}

func runServerDisable(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil {
		return fmt.Errorf("no servers configured")
	}

	found := false
	for _, srv := range cfg.Servers {
		if srv.Name == name {
			enabled := false
			srv.Enabled = &enabled
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("server %q not found", name)
	}

	if err := saveConfig(userConfigPath(), cfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Server %q disabled\n", name)
	return nil
}

func saveConfig(path string, cfg *migrate.Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := migrate.MarshalConfig(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0600)
}

func joinStrings(strs []string) string {
	result := ""
	for _, s := range strs {
		result += s + " "
	}
	return result
}

// handleStdio runs the concurrent stdio front end on os.Stdin/os.Stdout
// until EOF or a `shutdown` request, then runs cleanup (which stops the
// health checker and closes the pools, so no child process is orphaned).
func handleStdio(ctx context.Context, handler *mcp.Handler, opts stdioFrontendOptions, cleanup func(), statusStore *statusfile.FileStatusStore) error {
	slog.Info("leanproxy-mcp stdio mode started", "max_concurrent_requests", opts.MaxConcurrent)

	defer func() {
		flushUsageSnapshot()
		if statusStore != nil {
			statusStore.RemoveFile()
		}
	}()

	err := serveStdio(ctx, os.Stdin, os.Stdout, handler, opts)
	cleanup()
	return err
}

func writeStdioResponse(writer *bufio.Writer, resp *mcp.Response) {
	if resp != nil && resp.Result == nil && resp.Error == nil {
		// Belt-and-braces: Handler.HandleRequest already guards against this,
		// but the writer never emits invalid JSON-RPC (neither result nor
		// error) regardless of how the response reached it.
		slog.Error("stdio: response had neither result nor error, replacing with internal error", "id", resp.ID)
		resp = &mcp.Response{
			JSONRPC: mcp.JSONRPCVersion,
			Error:   mcp.NewError(mcp.ErrCodeInternalError, "internal error: empty response"),
			ID:      resp.ID,
		}
	}

	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to marshal response", "error", err)
		return
	}
	writeJSONLine(writer, data)
	writer.Flush()
}

func trimStdioNewline(data []byte) []byte {
	if len(data) > 0 && data[len(data)-1] == '\n' {
		data = data[:len(data)-1]
	}
	if len(data) > 0 && data[len(data)-1] == '\r' {
		data = data[:len(data)-1]
	}
	return data
}

var healthTimeout time.Duration

func runServerHealth(cmd *cobra.Command, args []string) error {
	serverName := args[0]

	info, err := statusfile.ReadCurrentStatus()
	hasRunningInstance := err == nil && info != nil

	if hasRunningInstance {
		for _, s := range info.Servers {
			if s.Name == serverName && s.Status == "running" {
				var uptime time.Duration
				if s.Uptime != "" {
					uptime, _ = time.ParseDuration(s.Uptime)
				}
				fmt.Printf("✓ Server %q is healthy (status: running, uptime: %v)\n", serverName, uptime)
				fmt.Printf("  Note: Connected to running LeanProxy instance (PID: %d)\n", info.PID)
				return nil
			}
		}
		if info.PID > 0 {
			fmt.Printf("Note: Found running LeanProxy (PID: %d) but server %q may have stopped\n", info.PID, serverName)
			fmt.Printf("      Attempting to restart server...\n")
		}
	}

	configPath := runFlags.config
	if configPath == "" {
		configPath = userConfigPath()
	}

	ctx := context.Background()

	cfg, err := migrate.LoadConfig(ctx, configPath)
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			return fmt.Errorf("failed to load config: %w", err)
		}
		cfg = nil
	}
	if cfg == nil {
		return fmt.Errorf("no servers configured in %s", configPath)
	}

	var serverCfg *migrate.ServerConfig
	for _, s := range cfg.Servers {
		if s.Name == serverName {
			serverCfg = s
			break
		}
	}
	if serverCfg == nil {
		return fmt.Errorf("server %q not found in config", serverName)
	}

	if serverCfg.Enabled != nil && !*serverCfg.Enabled {
		return fmt.Errorf("server %q is disabled", serverName)
	}

	logger := slog.Default()

	stdioP := pool.NewStdioPool(2, healthTimeout, logger)
	httpP := pool.NewHTTPClientPool(logger)
	sseP := pool.NewSSEPool(logger)

	switch serverCfg.Transport {
	case "stdio":
		if err := stdioP.StartServer(ctx, serverCfg); err != nil {
			return fmt.Errorf("failed to start stdio server: %w", err)
		}
	case "http":
		if err := httpP.StartServer(ctx, serverCfg); err != nil {
			return fmt.Errorf("failed to start http server: %w", err)
		}
	case "sse":
		if err := sseP.StartServer(ctx, serverCfg); err != nil {
			return fmt.Errorf("failed to start sse server: %w", err)
		}
	default:
		return fmt.Errorf("unsupported transport type: %s", serverCfg.Transport)
	}

	start := time.Now()

	initialized := false
	var resp *pool.Response
	var healthErr error

	switch serverCfg.Transport {
	case "stdio":
		_, initErr := stdioP.SendRequestToServerWithID(ctx, serverName, mcp.MethodInitialize, []byte(`{"protocolVersion":"`+mcp.LatestProtocolVersion+`","capabilities":{},"clientInfo":{"name":"leanproxy-healthcheck","version":"1.0"}}`), healthTimeout, 1)
		if initErr != nil {
			return fmt.Errorf("failed to initialize server: %w", initErr)
		}
		initialized = true
		resp, healthErr = stdioP.SendRequestToServerWithID(ctx, serverName, mcp.MethodPing, nil, healthTimeout, 2)
	case "http":
		resp, healthErr = httpP.SendRequestToServerWithID(ctx, serverName, mcp.MethodPing, nil, healthTimeout, 1)
	case "sse":
		resp, healthErr = sseP.SendRequestToServerWithID(ctx, serverName, mcp.MethodPing, nil, healthTimeout, 1)
	}
	elapsed := time.Since(start)

	if healthErr != nil {
		return fmt.Errorf("health check failed for %q: %w", serverName, healthErr)
	}

	if resp != nil && resp.Error != nil {
		return fmt.Errorf("health check returned error for %q: %s", serverName, resp.Error.Message)
	}

	fmt.Printf("✓ Server %q is healthy (latency: %v)\n", serverName, elapsed)
	if initialized {
		if hasRunningInstance {
			fmt.Printf("  Note: Server was stopped in running LeanProxy, restarted successfully\n")
		} else {
			fmt.Printf("  Note: Started new LeanProxy instance for health check\n")
		}
	}
	return nil
}
