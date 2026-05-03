package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/proxy"
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
}

func init() {
	statusCmd.Flags().BoolVar(&statusFlags.watch, "watch", false, "Continuously update status every second")
	statusCmd.Flags().BoolVar(&statusFlags.verbose, "verbose", false, "Show additional details (memory, request count, error rate)")
	statusCmd.Flags().StringVar(&statusFlags.server, "server", "", "Filter by specific server name")
	statusCmd.Flags().BoolVar(&statusFlags.jsonOut, "json", false, "Output in JSON format")
	statusCmd.Flags().DurationVar(&statusFlags.interval, "interval", 1*time.Second, "Watch mode refresh interval")
	RootCmd.AddCommand(statusCmd)
}

func runStatus(cmd *cobra.Command, args []string) {
	if statusFlags.watch {
		runStatusWatch()
	} else {
		runStatusSingle()
	}
}

func runStatusSingle() {
	statusList := getMockStatusList()

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

	statusChan := getMockStatusChannel(ctx, statusFlags.interval)

	fmt.Println("Watching server status (Ctrl+C to exit)...")
	fmt.Println()

	for {
		select {
		case <-ctx.Done():
			fmt.Println("\nStopped watching status")
			return
		case statusList, ok := <-statusChan:
			if !ok {
				return
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

func getMockStatusList() proxy.ServerStatusList {
	return proxy.ServerStatusList{
		Timestamp: time.Now(),
		Servers: []proxy.ServerStatus{
			{
				Name:            "server-1",
				Status:          proxy.StatusRunning,
				Uptime:          2*time.Minute + 34*time.Second,
				LastResponseTime: time.Now().Add(-120 * time.Millisecond),
				RequestCount:    1234,
				ErrorRate:       0.1,
				MemoryMB:        45,
			},
			{
				Name:            "server-2",
				Status:          proxy.StatusError,
				Uptime:          5*time.Minute + 12*time.Second,
				LastResponseTime: time.Now().Add(-30 * time.Second),
				RequestCount:    567,
				ErrorRate:       5.2,
				RestartCount:    2,
				LastError:       "process exited with code 1",
			},
			{
				Name:            "server-3",
				Status:          proxy.StatusStopped,
				Uptime:          0,
				LastResponseTime: time.Time{},
				RequestCount:    0,
				RestartCount:    3,
				LastError:       "max restarts reached",
			},
		},
	}
}

func getMockStatusChannel(ctx context.Context, interval time.Duration) <-chan proxy.ServerStatusList {
	outputChan := make(chan proxy.ServerStatusList)

	go func() {
		defer close(outputChan)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		requestCount := int64(1234)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				requestCount += 10
				statusList := proxy.ServerStatusList{
					Timestamp: time.Now(),
					Servers: []proxy.ServerStatus{
						{
							Name:            "server-1",
							Status:          proxy.StatusRunning,
							Uptime:          154 * time.Second,
							LastResponseTime: time.Now().Add(-120 * time.Millisecond),
							RequestCount:    requestCount,
							ErrorRate:       0.1,
							MemoryMB:        45,
						},
						{
							Name:            "server-2",
							Status:          proxy.StatusError,
							Uptime:          312 * time.Second,
							LastResponseTime: time.Now().Add(-30 * time.Second),
							RequestCount:    567,
							ErrorRate:       5.2,
							RestartCount:    2,
							LastError:       "process exited with code 1",
						},
						{
							Name:            "server-3",
							Status:          proxy.StatusStopped,
							Uptime:          0,
							LastResponseTime: time.Time{},
							RequestCount:    0,
							RestartCount:    3,
							LastError:       "max restarts reached",
						},
					},
				}
				select {
				case outputChan <- statusList:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return outputChan
}

type mockManagedServer struct {
	name         string
	pid          int
	running      bool
	startTime    time.Time
	requestCount int64
	errorCount   int64
}

func (m *mockManagedServer) Name() string            { return m.name }
func (m *mockManagedServer) PID() int                { return m.pid }
func (m *mockManagedServer) IsRunning() bool         { return m.running }
func (m *mockManagedServer) StartTime() time.Time   { return m.startTime }
func (m *mockManagedServer) LastResponseTime() time.Time { return time.Now().Add(-120 * time.Millisecond) }
func (m *mockManagedServer) RequestCount() int64     { return m.requestCount }
func (m *mockManagedServer) ErrorCount() int64      { return m.errorCount }
func (m *mockManagedServer) OnCrash(callback func()) {}
func (m *mockManagedServer) OnRestart(callback func()) {}

var _ proxy.ManagedServer = (*mockManagedServer)(nil)