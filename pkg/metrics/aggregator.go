package metrics

import (
	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
)

// MetricsSnapshot is this process's live counters. It is what /metrics
// serves (plus the usage store's windows, see UsageSummary) and what every
// front end appends to the usage store (pkg/usage.Record), so it must stay
// payload-free: only numbers, server/tool names and flags.
//
// It used to carry by_tool/by_server/total_spend/top_5_expensive_tools
// from reporter.GlobalCostTracker, which nothing in the live pipeline
// feeds; those were always empty and are gone. Per-server/per-tool token
// figures now come from the usage store (UsageSummary), built from the
// response governor's per-tool accounting below.
type MetricsSnapshot struct {
	ResponseCache *ResponseCacheMetric `json:"response_cache,omitempty"`
	// ResponseGovernor is the response token governor's accounting (issue
	// #319): tokens before/after per tool, truncations, spill store size.
	// Omitted while the governor is off.
	ResponseGovernor *mcp.GovernorStats `json:"response_governor,omitempty"`
	// Telemetry mirrors the counters OpenTelemetry records (issue #317):
	// requests, errors, redactions, injection detections, cache hits/misses,
	// policy decisions, rate-limit waits and in-flight requests. Populated
	// unconditionally (see pkg/mcp.TelemetrySnapshot), independent of
	// whether an OTLP exporter is configured, so /metrics keeps working the
	// same whether or not telemetry is enabled.
	Telemetry mcp.TelemetryCounters `json:"telemetry"`
}

// Snapshot returns this process's live counters.
func Snapshot() MetricsSnapshot {
	return MetricsSnapshot{
		ResponseCache:    responseCacheSnapshot(),
		ResponseGovernor: responseGovernorSnapshot(),
		Telemetry:        mcp.TelemetrySnapshot(),
	}
}
