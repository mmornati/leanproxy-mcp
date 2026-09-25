package cmd

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/metrics"
	"github.com/mmornati/leanproxy-mcp/pkg/usage"
)

// `server run` exposes the same endpoint flags as `serve`, off by default
// (several `server run --stdio` processes cannot share a port); `serve`
// keeps its dashboard on 127.0.0.1:9090.
func TestEndpointFlagsOnBothFrontEnds(t *testing.T) {
	for _, name := range []string{"metrics-bind", "metrics-token", "metrics-allowed-hosts", "dashboard-bind", "dashboard-token", "dashboard-allowed-hosts"} {
		if runCmd.Flags().Lookup(name) == nil {
			t.Errorf("server run has no --%s", name)
		}
		if serveCmd.Flags().Lookup(name) == nil {
			t.Errorf("serve has no --%s", name)
		}
	}
	if got := runCmd.Flags().Lookup("dashboard-bind").DefValue; got != "" {
		t.Errorf("server run --dashboard-bind default = %q, want off", got)
	}
	if got := runCmd.Flags().Lookup("metrics-bind").DefValue; got != "" {
		t.Errorf("server run --metrics-bind default = %q, want off", got)
	}
	if got := serveCmd.Flags().Lookup("dashboard-bind").DefValue; got != "127.0.0.1:9090" {
		t.Errorf("serve --dashboard-bind default = %q", got)
	}
}

func TestStartEndpointsRefusesNonLoopbackWithoutToken(t *testing.T) {
	f := endpointFlags{metricsBind: "127.0.0.1:0", dashboardBind: "0.0.0.0:0"}
	m, d, err := f.startEndpoints()
	if err == nil {
		t.Fatal("expected an error for a non-loopback dashboard bind without a token")
	}
	if m != nil || d != nil {
		t.Fatal("nothing may be left running after a refused start")
	}
}

// The metrics endpoint started by startEndpoints serves the usage store's
// windows.
func TestStartEndpointsServesUsageFromStore(t *testing.T) {
	store, err := usage.NewStoreWithDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prev := usageStore
	usageStore = store
	t.Cleanup(func() { usageStore = prev })

	rec := usage.Record{Timestamp: time.Now().UTC(), SessionID: "stdio-1", Snapshot: metrics.MetricsSnapshot{
		Telemetry: mcp.TelemetryCounters{SchemaListings: 1, SchemaNativeTokens: 4000, SchemaSentTokens: 400},
		ResponseGovernor: &mcp.GovernorStats{Enabled: true, Results: 2, OriginalTokens: 900, ReturnedTokens: 300,
			ByTool: []mcp.GovernorToolStats{{Tool: "github.search_code", Results: 2, OriginalTokens: 900, ReturnedTokens: 300}}},
	}}
	if err := store.Append(rec); err != nil {
		t.Fatal(err)
	}

	f := endpointFlags{metricsBind: "127.0.0.1:0"}
	m, d, err := f.startEndpoints()
	if err != nil {
		t.Fatal(err)
	}
	if d != nil {
		t.Fatal("dashboard started without --dashboard-bind")
	}
	defer m.Close()

	var resp *http.Response
	for i := 0; i < 40; i++ {
		if resp, err = http.Get("http://" + m.Addr + "/metrics"); err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Usage *metrics.UsageSummary `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Usage == nil {
		t.Fatal("no usage section")
	}
	today := body.Usage.Today
	if today.SavedTokens != 3600+600 || today.TopServer != "github" || today.TopTool != "github.search_code" {
		t.Fatalf("today = %+v", today)
	}
}
