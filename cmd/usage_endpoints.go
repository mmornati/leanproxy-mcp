package cmd

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/mmornati/leanproxy-mcp/pkg/dashboard"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/usage"
	"github.com/spf13/cobra"
)

// endpointFlags are the dashboard and /metrics endpoint flags, shared by
// `serve` and `server run` so both front ends expose the same endpoints
// with the same security rules (#316).
type endpointFlags struct {
	metricsBind           string
	metricsToken          string
	metricsAllowedHosts   []string
	dashboardBind         string
	dashboardToken        string
	dashboardTokenFile    string
	dashboardAllowedHosts []string
}

// register adds the endpoint flags to c. dashboardDefault is the default
// --dashboard-bind: `serve` keeps its historical 127.0.0.1:9090, `server
// run` defaults to off (an MCP client may start several `server run
// --stdio` processes, which cannot all bind the same port).
func (f *endpointFlags) register(c *cobra.Command, dashboardDefault string) {
	fs := c.Flags()
	fs.StringVar(&f.metricsBind, "metrics-bind", "", "Metrics endpoint bind address (e.g. 127.0.0.1:9091). Set to 'off' or empty to disable.")
	fs.StringVar(&f.metricsToken, "metrics-token", "", "Bearer token for the metrics endpoint; required on a non-loopback --metrics-bind")
	fs.StringSliceVar(&f.metricsAllowedHosts, "metrics-allowed-hosts", nil, "Extra Host header values accepted by the metrics endpoint, beyond the bind host and loopback names")
	fs.StringVar(&f.dashboardBind, "dashboard-bind", dashboardDefault, "Dashboard endpoint bind address (e.g. 127.0.0.1:9090). Set to 'off' or empty to disable.")
	fs.StringVar(&f.dashboardToken, "dashboard-token", "", "Bearer token for dashboard access; required on a non-loopback --dashboard-bind, and then required from every client including loopback")
	fs.StringVar(&f.dashboardTokenFile, "dashboard-token-file", "", "Read the dashboard token from this file (created with a random token if missing). The background service uses it so the token never appears in a process listing")
	fs.StringSliceVar(&f.dashboardAllowedHosts, "dashboard-allowed-hosts", nil, "Extra Host header values accepted by the dashboard, beyond the bind host and loopback names")
}

// startEndpoints starts the metrics endpoint and the dashboard per f, both
// fed from the usage store (initUsageStore must have run first). A
// non-loopback bind without a token is refused (#316); the returned
// servers are nil when disabled. On an error nothing is left running.
//
// admin, when not nil, adds the servers / clients / calls views to the
// dashboard; its write actions need a dashboard token.
func (f *endpointFlags) startEndpoints(admin dashboard.Admin) (metricsSrv, dashboardSrv *http.Server, err error) {
	dashboardToken, err := f.resolveDashboardToken()
	if err != nil {
		return nil, nil, err
	}
	summary := usageSummaryProvider()
	metricsSrv, err = metrics.ListenAndServeConfig(metrics.Config{
		Bind:         f.metricsBind,
		Token:        f.metricsToken,
		AllowedHosts: f.metricsAllowedHosts,
		Usage:        summary,
	}, slog.Default())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start metrics endpoint: %w", err)
	}
	dashboardSrv, err = dashboard.ListenAndServe(dashboard.Config{
		Bind:         f.dashboardBind,
		Token:        dashboardToken,
		AllowedHosts: f.dashboardAllowedHosts,
		Usage:        summary,
		Admin:        admin,
	}, slog.Default())
	if err != nil {
		if metricsSrv != nil {
			metricsSrv.Close()
		}
		return nil, nil, fmt.Errorf("failed to start dashboard endpoint: %w", err)
	}
	if metricsSrv != nil || dashboardSrv != nil {
		// Read the week's usage files once now, so the first dashboard or
		// /metrics request does not pay for it.
		go summary()
	}
	return metricsSrv, dashboardSrv, nil
}

var (
	usageLiveMu    sync.Mutex
	usageLive      *usage.Live
	usageLiveStore *usage.Store
)

// usageSummaryProvider returns the function the dashboard and /metrics
// read today / week-to-date usage with. It reads the process-wide usage
// store incrementally (usage.Live), so it also covers every other front
// end on this machine writing to the same store. It returns nil when the
// store is not open or cannot be read, which both endpoints show as
// "unavailable" rather than as zeros.
func usageSummaryProvider() func() *metrics.UsageSummary {
	return func() *metrics.UsageSummary {
		usageLiveMu.Lock()
		if usageLiveStore != usageStore {
			usageLive, usageLiveStore = nil, usageStore
			if usageStore != nil {
				usageLive = usage.NewLive(usageStore)
			}
		}
		live := usageLive
		usageLiveMu.Unlock()
		if live == nil {
			return nil
		}
		sum, err := live.Summary()
		if err != nil {
			slog.Warn("usage: failed to read the usage store for the dashboard/metrics", "error", err)
			return nil
		}
		return &sum
	}
}

// resolveDashboardToken returns the dashboard token: --dashboard-token,
// else the content of --dashboard-token-file (created on first use, like
// the serve token file).
func (f *endpointFlags) resolveDashboardToken() (string, error) {
	if f.dashboardToken != "" || f.dashboardTokenFile == "" {
		return f.dashboardToken, nil
	}
	if f.dashboardBind == "" || f.dashboardBind == "off" {
		return "", nil
	}
	token, err := loadOrCreateServeToken(f.dashboardTokenFile)
	if err != nil {
		return "", fmt.Errorf("--dashboard-token-file: %w", err)
	}
	return token, nil
}
