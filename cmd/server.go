package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
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
	env        []string
	cwd        string
	transport  string
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
		return fmt.Errorf("failed to load config: %w", err)
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
		Name:      name,
		Transport: transport,
		Stdio:     stdio,
		Enabled:   &enabled,
		Timeout:   "30s",
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
		return fmt.Errorf("failed to load config: %w", err)
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
	fmt.Scanln(&response)
	if response != "y" && response != "Y" {
		fmt.Println("Cancelled.")
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
		return fmt.Errorf("failed to load config: %w", err)
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
		return fmt.Errorf("failed to load config: %w", err)
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
	Short: "Run leanproxy-mcp as an MCP server in stdio mode",
	Long: `Run leanproxy-mcp as a Model Context Protocol server that proxies
requests to configured MCP servers. Reads JSON-RPC requests from stdin
and writes responses to stdout.

Use --stdio flag to enable stdio mode. Without --stdio, the command
will show help for the run command.

Example:
  leanproxy-mcp server run --stdio
  leanproxy-mcp server run --stdio --config /path/to/config.yaml
  leanproxy-mcp server run --stdio --log-file /tmp/leanproxy.log`,
	RunE: runServerRun,
}

var runFlags struct {
	stdio     bool
	config    string
	logFile   string
	logLevel  string
	verbose   bool
}

func init() {
	runCmd.Flags().BoolVar(&runFlags.stdio, "stdio", false, "Run in stdio mode (read JSON-RPC from stdin)")
	runCmd.Flags().StringVar(&runFlags.config, "config", "", "Path to leanproxy_servers.yaml config file")
	runCmd.Flags().StringVar(&runFlags.logFile, "log-file", "", "Path to log file")
	runCmd.Flags().StringVar(&runFlags.logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	runCmd.Flags().BoolVarP(&runFlags.verbose, "verbose", "v", false, "Enable verbose logging")
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

	if !runFlags.stdio {
		return fmt.Errorf("--stdio flag is required to run in stdio mode")
	}

	configPath := runFlags.config
	if configPath == "" {
		configPath = userConfigPath()
	}

	ctx := context.Background()

	cfg, err := migrate.LoadConfig(ctx, configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return fmt.Errorf("no servers configured in %s", configPath)
	}

	stdioPool := pool.NewStdioPool(5, 5*time.Minute, slog.Default())
	httpPool := pool.NewHTTPClientPool(slog.Default())
	ssePool := pool.NewSSEPool(slog.Default())
	unifiedPool := pool.NewUnifiedPool(stdioPool, httpPool, ssePool, slog.Default())

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

	var cache toolstore.Cache
	fileCache, err := toolstore.NewFileCache(slog.Default())
	if err != nil {
		slog.Warn("failed to create tool cache, using no-op cache", "error", err)
		cache = toolstore.NewNoOpCache()
	} else {
		cache = fileCache
	}

	statusStore, err := statusfile.NewFileStatusStore("stdio", slog.Default())
	if err != nil {
		slog.Warn("failed to create status store", "error", err)
	} else {
		slog.Info("status file enabled", "path", statusStore.GetFilePath())
		updateStdioServerStatusOnce(statusStore, stdioPool)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		slog.Info("shutting down server")
		if statusStore != nil {
			statusStore.RemoveFile()
		}
		stdioPool.Close()
		httpPool.Close()
		os.Exit(0)
	}()

	if statusStore != nil {
		go updateServerStatus(statusStore, unifiedPool, stdioPool)
	}

	handler := mcp.NewHandlerWithToolStore(unifiedPool, slog.Default(), cache)

	return handleStdio(ctx, handler, stdioPool, statusStore)
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
	}
}

func runServerDisable(cmd *cobra.Command, args []string) error {
	name := args[0]

	cfg, err := migrate.LoadConfig(context.Background(), userConfigPath())
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
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

	return os.WriteFile(path, data, 0644)
}

func joinStrings(strs []string) string {
	result := ""
	for _, s := range strs {
		result += s + " "
	}
	return result
}

func handleStdio(ctx context.Context, handler *mcp.Handler, stdioPool *pool.StdioPool, statusStore *statusfile.FileStatusStore) error {
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)

	slog.Info("leanproxy-mcp stdio mode started")

	defer func() {
		if statusStore != nil {
			statusStore.RemoveFile()
		}
	}()

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				slog.Info("stdin closed, shutting down")
				return nil
			}
			slog.Error("failed to read stdin", "error", err)
			return err
		}

		if len(line) == 0 {
			continue
		}

		line = trimStdioNewline(line)

		var req mcp.Request
		if err := json.Unmarshal(line, &req); err != nil {
			slog.Warn("failed to parse JSON-RPC request", "error", err, "line", string(line))
			resp := mcp.Response{
				JSONRPC: mcp.JSONRPCVersion,
				Error:   mcp.NewError(mcp.ErrCodeParseError, "invalid JSON-RPC request"),
				ID:      nil,
			}
			writeStdioResponse(writer, &resp)
			continue
		}

		resp, err := handler.HandleRequest(ctx, &req)
		if err != nil {
			slog.Error("handler error", "error", err, "method", req.Method)
		}

		if resp != nil {
			writeStdioResponse(writer, resp)
		}

		if req.Method == mcp.MethodShutdown {
			slog.Info("shutdown request received")
			stdioPool.Close()
			return nil
		}
	}
}

func writeStdioResponse(writer *bufio.Writer, resp *mcp.Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("failed to marshal response", "error", err)
		return
	}
	fmt.Fprintln(writer, string(data))
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

	configPath := runFlags.config
	if configPath == "" {
		configPath = userConfigPath()
	}

	ctx := context.Background()

	cfg, err := migrate.LoadConfig(ctx, configPath)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
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
		_, initErr := stdioP.SendRequestToServerWithID(ctx, serverName, mcp.MethodInitialize, []byte(`{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"leanproxy-healthcheck","version":"1.0"}}`), healthTimeout, 1)
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
		fmt.Printf("  Note: Server was initialized during health check\n")
	}
	return nil
}
