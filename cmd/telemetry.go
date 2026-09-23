package cmd

import (
	"context"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/telemetry"
)

// initTelemetry resolves the `telemetry:` config block (plus the standard
// OTEL_EXPORTER_OTLP_* env vars, which win) and starts the OTLP/HTTP
// exporters when enabled. It is called once, early in both `server run
// --stdio` and `serve`'s startup, before the middleware pipeline is built,
// so every request's span reaches a real exporter from the first one on.
//
// telemetry is off by default: a nil cfg or an unset endpoint returns a
// disabled *telemetry.Provider whose Shutdown is a no-op, and Init never
// touches the global otel providers in that case.
func initTelemetry(ctx context.Context, cfg *migrate.Config) *telemetry.Provider {
	var tcfg *telemetry.Config
	if cfg != nil {
		tcfg = cfg.Telemetry
	}
	resolved, err := telemetry.Resolve(tcfg)
	if err != nil {
		slog.Warn("telemetry: invalid configuration, disabling", "error", err)
		mcp.SetTelemetryActive(false)
		return &telemetry.Provider{}
	}
	provider, err := telemetry.Init(ctx, resolved, slog.Default())
	if err != nil {
		slog.Warn("telemetry: failed to start OTLP exporters, disabling", "error", err)
		mcp.SetTelemetryActive(false)
		return &telemetry.Provider{}
	}
	mcp.SetTelemetryActive(provider.Enabled())
	return provider
}

// tracedMiddlewares assembles the front end's middleware pipeline with
// telemetry instrumentation: the outermost SERVER span (mcp.TelemetryMiddleware),
// then the response cache and each firewall stage individually wrapped in
// its own named child span (issue #317: "child spans for each middleware").
// This is the single place both `server run --stdio` (Handler.Use) and
// `serve` (the per-request mcp.Chain in serveRequest) build their pipeline
// from, so the two front ends stay instrumented identically.
//
// Tool pinning (#310) comes right after the telemetry span and before the
// response cache, so a call to a tool blocked since its answer was cached
// is still refused.
func tracedMiddlewares(respCache *mcp.ResponseCache, firewall *mcp.Firewall, pins *mcp.ToolPins) []mcp.Middleware {
	mws := []mcp.Middleware{mcp.TelemetryMiddleware()}
	if pins != nil {
		mws = append(mws, mcp.Traced("tool_pinning", pins.Middleware()))
	}
	if respCache != nil {
		mws = append(mws, mcp.Traced("cache", respCache.Middleware()))
	}
	if firewall != nil {
		for i, mw := range firewall.Middlewares() {
			mws = append(mws, mcp.Traced(firewallStageName(i), mw))
		}
	}
	return mws
}

// firewallStageName names the firewall's middlewares in the fixed order
// Firewall.Middlewares() returns them (response redaction, request
// redaction, injection) for their span names.
func firewallStageName(i int) string {
	switch i {
	case 0:
		return "redact_response"
	case 1:
		return "redact_request"
	case 2:
		return "injection"
	default:
		return "firewall"
	}
}
