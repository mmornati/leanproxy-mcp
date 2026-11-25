package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/statusfile"
	"github.com/mmornati/leanproxy-mcp/pkg/utils"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display real-time status of MCP servers",
	Long: `Display the status of the running LeanProxy processes and of their
upstream servers: state, tools, calls, errors, latency and the last error.

Each running proxy (the shared "server run --http" gateway, and every
"server run --stdio" an MCP client started) is listed with its own servers.
When no proxy is running, or with --probe, status starts the configured
servers itself to check that they answer.`,
	Run: runStatus,
}

var statusFlags struct {
	watch       bool
	verbose     bool
	server      string
	jsonOut     bool
	interval    time.Duration
	config      string
	runningOnly bool
	probe       bool
}

func init() {
	statusCmd.Flags().BoolVar(&statusFlags.watch, "watch", false, "Continuously update status every second")
	statusCmd.Flags().BoolVar(&statusFlags.verbose, "verbose", false, "Show additional details (memory, request count, error rate)")
	statusCmd.Flags().StringVar(&statusFlags.server, "server", "", "Filter by specific server name")
	statusCmd.Flags().BoolVar(&statusFlags.jsonOut, "json", false, "Output in JSON format")
	statusCmd.Flags().DurationVar(&statusFlags.interval, "interval", 1*time.Second, "Watch mode refresh interval")
	statusCmd.Flags().StringVar(&statusFlags.config, "config", "", "Path to leanproxy_servers.yaml config file")
	statusCmd.Flags().BoolVar(&statusFlags.runningOnly, "running", false, "Never probe: only report running instances")
	statusCmd.Flags().BoolVar(&statusFlags.probe, "probe", false, "Ignore running instances: start every configured server in this process and report whether it answers")
	RootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) {
	initLogger(cmd)
	// Running proxies describe themselves in their status files: read
	// those. Only when none is running (or with --probe) does status start
	// the configured servers itself to see whether they answer.
	if !statusFlags.probe {
		if shown := runStatusInstances(); shown {
			return
		}
		if statusFlags.runningOnly {
			fmt.Println("No running leanproxy instance found")
			return
		}
		fmt.Println("No running leanproxy instance found: starting the configured servers to probe them.")
		fmt.Println()
	}
	if statusFlags.watch {
		runStatusWatch()
	} else {
		runStatusSingle()
	}
}

func statusConfigPath() string {
	if path := statusFlags.config; path != "" {
		return path
	}
	if path := os.Getenv("LEANPROXY_CONFIG"); path != "" {
		return path
	}
	home := os.Getenv("HOME")
	if home == "" {
		home = os.Getenv("USERPROFILE")
	}
	return filepath.Join(home, ".config", "leanproxy_servers.yaml")
}

func getRunningStatusList() proxy.ServerStatusList {
	info, err := statusfile.ReadCurrentStatus()
	if err != nil {
		fmt.Printf("Error reading running status: %v\n", err)
		return proxy.ServerStatusList{}
	}
	if info == nil {
		fmt.Println("No running leanproxy instance found")
		return proxy.ServerStatusList{}
	}

	fmt.Printf("Running leanproxy instance (PID: %d, started: %s, listen: %s)\n\n",
		info.PID, info.StartedAt.Format("2006-01-02 15:04:05"), info.ListenAddr)

	statusList := make([]proxy.ServerStatus, 0, len(info.Servers))
	for _, s := range info.Servers {
		var healthStatus proxy.ServerHealthStatus
		switch s.Status {
		case "running":
			healthStatus = proxy.StatusRunning
		case "error":
			healthStatus = proxy.StatusError
		case "stopped":
			healthStatus = proxy.StatusStopped
		default:
			healthStatus = proxy.StatusUnresponsive
		}

		var uptime time.Duration
		if s.Uptime != "" {
			uptime, _ = time.ParseDuration(s.Uptime)
		}

		status := proxy.ServerStatus{
			Name:         s.Name,
			Status:       healthStatus,
			RequestCount: s.RequestCount,
			ErrorRate:    float64(s.ErrorCount),
			RestartCount: s.RestartCount,
			Uptime:       uptime,
		}
		statusList = append(statusList, status)
	}

	return proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers:   statusList,
	}
}

func getRealStatusList() proxy.ServerStatusList {
	ctx := context.Background()

	configPath := statusConfigPath()
	cfg, err := migrate.LoadConfig(ctx, configPath)
	if err != nil {
		if !errors.Is(err, migrate.ErrConfigNotFound) {
			fmt.Printf("Error loading config: %v\n", err)
		}
		return proxy.ServerStatusList{}
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return proxy.ServerStatusList{}
	}

	stdioPool := pool.NewStdioPool(5, 5*time.Minute, slog.Default())
	httpPool := pool.NewHTTPClientPool(slog.Default())
	ssePool := pool.NewSSEPool(slog.Default())
	unifiedPool := pool.NewUnifiedPool(stdioPool, httpPool, ssePool, slog.Default())

	for _, srv := range cfg.Servers {
		if srv.Enabled != nil && !*srv.Enabled {
			continue
		}
		switch srv.Transport {
		case registry.TransportStdio:
			if err := stdioPool.StartServer(ctx, srv); err != nil {
				slog.Warn("failed to start server for status check", "name", srv.Name, "error", err)
			}
		case registry.TransportHTTP:
			if err := httpPool.StartServer(ctx, srv); err != nil {
				slog.Warn("failed to start HTTP server for status check", "name", srv.Name, "error", err)
			}
		case registry.TransportSSE:
			if err := ssePool.StartServer(ctx, srv); err != nil {
				slog.Warn("failed to start SSE server for status check", "name", srv.Name, "error", err)
			}
		}
	}

	servers := unifiedPool.ListServers()
	statusList := make([]proxy.ServerStatus, 0, len(servers))

	for _, serverName := range servers {
		state, _ := unifiedPool.GetServerState(serverName)

		stats := pool.ServerStats{}
		stdioStats, err := stdioPool.GetServerStats(serverName)
		if err == nil {
			stats = stdioStats
		}

		status := proxy.ServerStatus{
			Name:         serverName,
			RequestCount: stats.RequestCount,
			ErrorRate:    calculateErrorRate(stats.ErrorCount, stats.RequestCount),
			RestartCount: stats.RestartCount,
			LastError:    getLastError(state, stats),
		}

		switch state {
		case pool.StateIdle, pool.StateRunning, pool.StateBusy:
			status.Status = proxy.StatusRunning
		case pool.StateError:
			status.Status = proxy.StatusError
		case pool.StateStopped, pool.StateStopping:
			status.Status = proxy.StatusStopped
		default:
			status.Status = proxy.StatusUnresponsive
		}

		if stats.LastRequestAt.IsZero() {
			status.LastResponseTime = time.Time{}
		} else {
			status.LastResponseTime = stats.LastRequestAt
			status.Uptime = time.Since(stats.LastRequestAt)
		}

		if stats.AvgLatencyMs > 0 {
			status.LastResponseTime = time.Now().Add(-time.Duration(stats.AvgLatencyMs) * time.Millisecond)
		}

		statusList = append(statusList, status)
	}

	stdioPool.Close()
	httpPool.Close()

	return proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers:   statusList,
	}
}

func calculateErrorRate(errors, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total) * 100
}

func getLastError(state pool.ServerState, stats pool.ServerStats) string {
	switch state {
	case pool.StateError:
		return "server in error state"
	case pool.StateStopped:
		return "server stopped"
	case pool.StateStopping:
		return "server stopping"
	}
	if stats.RestartCount > 0 && stats.CurrentBackoff > 0 {
		return fmt.Sprintf("restart count: %d, backoff: %v", stats.RestartCount, stats.CurrentBackoff)
	}
	return ""
}

func runStatusSingle() {
	var statusList proxy.ServerStatusList

	if statusFlags.runningOnly {
		statusList = getRunningStatusList()
	} else {
		statusList = getRealStatusList()
	}

	if statusFlags.server != "" {
		statusList = filterByServer(statusList, statusFlags.server)
	}

	if len(statusList.Servers) == 0 {
		if statusFlags.server != "" {
			fmt.Printf("Server not found: %s\n", statusFlags.server)
		} else {
			fmt.Println("No servers configured")
		}
		return
	}

	display := utils.NewStatusDisplay()

	if statusFlags.jsonOut {
		output, _ := display.RenderJSON(statusList)
		fmt.Println(output)
		return
	}

	if statusFlags.verbose {
		fmt.Print(display.RenderVerbose(statusList))
	} else {
		fmt.Print(display.RenderTable(statusList))
	}
}

func runStatusWatch() {
	display := utils.NewStatusDisplay()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		<-sigChan
		cancel()
	}()

	ticker := time.NewTicker(statusFlags.interval)
	defer ticker.Stop()

	fmt.Println("Watching server status (Ctrl+C to exit)...")
	fmt.Println()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopped watching status")
			return
		case <-ticker.C:
			var statusList proxy.ServerStatusList
			if statusFlags.runningOnly {
				statusList = getRunningStatusList()
			} else {
				statusList = getRealStatusList()
			}

			if statusFlags.server != "" {
				statusList = filterByServer(statusList, statusFlags.server)
			}

			if statusFlags.jsonOut {
				output, _ := display.RenderJSON(statusList)
				fmt.Println(output)
			} else if statusFlags.verbose {
				fmt.Print(display.RenderVerbose(statusList))
			} else {
				fmt.Print(display.RenderTable(statusList))
			}
			fmt.Println()
		}
	}
}

func filterByServer(statusList proxy.ServerStatusList, serverName string) proxy.ServerStatusList {
	filtered := make([]proxy.ServerStatus, 0)
	for _, s := range statusList.Servers {
		if s.Name == serverName {
			filtered = append(filtered, s)
			break
		}
	}
	return proxy.ServerStatusList{
		Timestamp: statusList.Timestamp,
		Servers:   filtered,
	}
}

// runStatusInstances prints the status of every running proxy process,
// once or (with --watch) repeatedly. It returns false when none is
// running and nothing was printed.
func runStatusInstances() bool {
	all, err := statusfile.ReadAllStatus()
	if err != nil {
		fmt.Printf("Error reading running status: %v\n", err)
		return false
	}
	if len(all) == 0 {
		return false
	}
	if !statusFlags.watch {
		printInstances(all)
		return true
	}
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	ticker := time.NewTicker(statusFlags.interval)
	defer ticker.Stop()
	for {
		printInstances(all)
		fmt.Println()
		select {
		case <-sigChan:
			return true
		case <-ticker.C:
			all, _ = statusfile.ReadAllStatus()
		}
	}
}

func printInstances(all []statusfile.StatusInfo) {
	if statusFlags.jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(all)
		return
	}
	if len(all) > 1 {
		fmt.Printf("%d leanproxy processes are running, each with its own upstream servers.\n", len(all))
		fmt.Println("Run one shared gateway instead: leanproxy-mcp daemon install")
		fmt.Println()
	}
	for i, info := range all {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("leanproxy-mcp pid %d  %s  up %s  %d client(s)\n", info.PID, info.ListenAddr,
			time.Since(info.StartedAt).Round(time.Second), info.Clients)
		if info.Dashboard != "" {
			fmt.Printf("dashboard: %s\n", info.Dashboard)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  SERVER\tTRANSPORT\tSTATE\tTOOLS\tCALLS\tERRORS\tAVG\tDETAIL")
		for _, sv := range info.Servers {
			if statusFlags.server != "" && sv.Name != statusFlags.server {
				continue
			}
			state := sv.State
			if state == "" {
				state = sv.Status
			}
			avg := "-"
			if sv.AvgLatencyMs > 0 {
				avg = fmt.Sprintf("%.0fms", sv.AvgLatencyMs)
			}
			detail := sv.Summary
			if statusFlags.verbose && sv.LastError != "" {
				detail += " [" + sv.LastError + "]"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%d\t%d\t%s\t%s\n", sv.Name, sv.Transport, state,
				sv.ToolCount, sv.RequestCount, sv.ErrorCount, avg, detail)
		}
		_ = tw.Flush()
	}
}
