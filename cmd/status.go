package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
	"github.com/mmornati/leanproxy-mcp/pkg/utils"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Display real-time status of MCP servers",
	Long:  `Display real-time status of all active proxied servers including health, uptime, and request metrics.`,
	Run:   runStatus,
}

var statusFlags struct {
	watch    bool
	verbose  bool
	server   string
	jsonOut  bool
	interval time.Duration
	config   string
}

func init() {
	statusCmd.Flags().BoolVar(&statusFlags.watch, "watch", false, "Continuously update status every second")
	statusCmd.Flags().BoolVar(&statusFlags.verbose, "verbose", false, "Show additional details (memory, request count, error rate)")
	statusCmd.Flags().StringVar(&statusFlags.server, "server", "", "Filter by specific server name")
	statusCmd.Flags().BoolVar(&statusFlags.jsonOut, "json", false, "Output in JSON format")
	statusCmd.Flags().DurationVar(&statusFlags.interval, "interval", 1*time.Second, "Watch mode refresh interval")
	statusCmd.Flags().StringVar(&statusFlags.config, "config", "", "Path to leanproxy_servers.yaml config file")
	RootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) {
	initLogger(cmd)
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

func getRealStatusList() proxy.ServerStatusList {
	ctx := context.Background()

	configPath := statusConfigPath()
	cfg, err := migrate.LoadConfig(ctx, configPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		return proxy.ServerStatusList{}
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return proxy.ServerStatusList{}
	}

	stdioPool := pool.NewStdioPool(5, 5*time.Minute, slog.Default())

	for _, srv := range cfg.Servers {
		if srv.Enabled != nil && !*srv.Enabled {
			continue
		}
		if srv.Transport != registry.TransportStdio {
			continue
		}
		if err := stdioPool.StartServer(ctx, srv); err != nil {
			slog.Warn("failed to start server for status check", "name", srv.Name, "error", err)
		}
	}

	servers := stdioPool.ListServers()
	statusList := make([]proxy.ServerStatus, 0, len(servers))

	for _, serverName := range servers {
		state, _ := stdioPool.GetServerState(serverName)
		stats, _ := stdioPool.GetServerStats(serverName)

		status := proxy.ServerStatus{
			Name:            serverName,
			RequestCount:    stats.RequestCount,
			ErrorRate:       calculateErrorRate(stats.ErrorCount, stats.RequestCount),
			RestartCount:    stats.RestartCount,
			LastError:       getLastError(state, stats),
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
	statusList := getRealStatusList()

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
			statusList := getRealStatusList()

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