package mcp

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry (issue #317) instruments the unified middleware pipeline with
// OpenTelemetry traces and metrics, following the GenAI/MCP semantic
// conventions of semconv v1.41.0 (the first release carrying `mcp.*`
// attributes and the `mcpconv` metric package; `mcp.tool.name` and
// `mcp.server.name` are not yet standardized there, so this file defines
// them locally using the same "mcp." namespace the released attributes use).
//
// Every call in this file goes through the global otel tracer/meter
// providers (otel.Tracer / otel.Meter). When pkg/telemetry.Init was never
// called (telemetry disabled, the default), those resolve to the SDK's
// built-in no-op implementations: starting a span or recording a
// measurement is then a handful of interface calls that allocate nothing
// and touch no exporter, so the pipeline's cost with telemetry off is the
// same as without this file (see BenchmarkTelemetryMiddleware).
//
// Nothing here ever attaches argument or result payloads to a span or
// metric: only names (method, tool, server), sizes (byte counts), counts
// (redactions, injection detections) and outcomes (status, error.type).
const (
	tracerName = "github.com/mmornati/leanproxy-mcp/pkg/mcp"
	meterName  = tracerName

	// attrMCPToolName and attrMCPServerName mirror the semconv `mcp.*`
	// namespace for the tool identity and the upstream server name, which
	// semconv v1.41.0 does not yet define as named constants (only
	// mcp.method.name, mcp.session.id and mcp.protocol.version are
	// released so far).
	attrMCPToolName   = attribute.Key("mcp.tool.name")
	attrMCPServerName = attribute.Key("mcp.server.name")
)

func tracer() trace.Tracer {
	return otel.Tracer(tracerName, trace.WithInstrumentationVersion(instrumentationVersion))
}
func meter() metric.Meter {
	return otel.Meter(meterName, metric.WithInstrumentationVersion(instrumentationVersion))
}

const instrumentationVersion = "1"

// instruments are created once, lazily, against whatever meter provider is
// global at first use. pkg/telemetry.Init (when telemetry is enabled) must
// therefore run before the first request reaches the pipeline, which both
// front ends guarantee (telemetry is initialized during startup, before
// Handler.Use/the per-request Chain runs).
var instrumentsOnce sync.Once
var inst instrumentSet

type instrumentSet struct {
	requestDuration  metric.Float64Histogram
	responseBytes    metric.Int64Histogram
	requestsTotal    metric.Int64Counter
	errorsTotal      metric.Int64Counter
	redactionsTotal  metric.Int64Counter
	injectionsTotal  metric.Int64Counter
	cacheHits        metric.Int64Counter
	cacheMisses      metric.Int64Counter
	policyDecisions  metric.Int64Counter
	rateLimitWaits   metric.Int64Counter
	toolPinEvents    metric.Int64Counter
	inFlightRequests metric.Int64UpDownCounter
	governorTokens   metric.Int64Counter
	governorTrunc    metric.Int64Counter
	governorProj     metric.Int64Counter
	governorProjTok  metric.Int64Counter
}

func instruments() *instrumentSet {
	instrumentsOnce.Do(func() {
		m := meter()
		inst = instrumentSet{}
		inst.requestDuration, _ = m.Float64Histogram("mcp.server.request.duration",
			metric.WithDescription("Duration of an MCP request handled by the proxy, by method, tool and server."),
			metric.WithUnit("ms"))
		inst.responseBytes, _ = m.Int64Histogram("mcp.server.response.size",
			metric.WithDescription("Size of the JSON-RPC response body written to the client."),
			metric.WithUnit("By"))
		inst.requestsTotal, _ = m.Int64Counter("mcp.server.requests",
			metric.WithDescription("Number of MCP requests handled."))
		inst.errorsTotal, _ = m.Int64Counter("mcp.server.errors",
			metric.WithDescription("Number of MCP requests that ended in an error, by error.type."))
		inst.redactionsTotal, _ = m.Int64Counter("leanproxy.redactions",
			metric.WithDescription("Number of secret redactions applied."))
		inst.injectionsTotal, _ = m.Int64Counter("leanproxy.injection.detections",
			metric.WithDescription("Number of prompt-injection detections, by action taken."))
		inst.cacheHits, _ = m.Int64Counter("leanproxy.cache.hits",
			metric.WithDescription("Response cache hits."))
		inst.cacheMisses, _ = m.Int64Counter("leanproxy.cache.misses",
			metric.WithDescription("Response cache misses."))
		inst.policyDecisions, _ = m.Int64Counter("leanproxy.policy.decisions",
			metric.WithDescription("Per-tool policy decisions (#314), by outcome (allow, deny, deny_unknown_tool, confirm_approved, confirm_approved_session, confirm_cached, confirm_denied, confirm_unavailable, confirm_timeout) and server."))
		inst.rateLimitWaits, _ = m.Int64Counter("leanproxy.ratelimit.waits",
			metric.WithDescription("Requests that waited on a rate limiter."))
		inst.toolPinEvents, _ = m.Int64Counter("leanproxy.tool_pin.events",
			metric.WithDescription("Tool pinning events (tool_added, tool_changed, tool_removed, server_identity_changed, tool_flagged, tool_shadowed, call_blocked, ...), by event and server."))
		inst.governorTokens, _ = m.Int64Counter("leanproxy.governor.tokens",
			metric.WithDescription("Estimated tokens of tool results seen by the response governor (#319), by direction (original, returned) and server."))
		inst.governorTrunc, _ = m.Int64Counter("leanproxy.governor.truncations",
			metric.WithDescription("Tool results the response governor shortened and spilled (#319), by server."))
		inst.governorProj, _ = m.Int64Counter("leanproxy.governor.projections",
			metric.WithDescription("Tool results the response governor projected (#320), by server."))
		inst.governorProjTok, _ = m.Int64Counter("leanproxy.governor.projection.tokens",
			metric.WithDescription("Estimated tokens of the projected parts of tool results (#320), by direction (before, after) and server."))
		inst.inFlightRequests, _ = m.Int64UpDownCounter("mcp.server.requests.in_flight",
			metric.WithDescription("Requests currently in flight, by server."))
	})
	return &inst
}

// telemetryActive gates the expensive parts of instrumentation (starting a
// span, building attribute slices, calling into the otel metric API with
// WithAttributes options): they only run once pkg/telemetry.Init has
// actually installed real exporters. Even against a no-op tracer/meter
// provider, building spans and attribute slices on every request measured
// double-digit percent overhead (allocations for the attribute slice, the
// WithAttributes option wrapper, and the global tracer/meter provider's
// delegate indirection) — well over the issue's 5% budget — so instead of
// relying on the SDK's no-op cost alone, disabled telemetry takes a
// dedicated fast path that only bumps a couple of atomics (see
// TelemetryMiddleware, Traced and Handler.sendUpstream). cmd/telemetry.go
// sets this once at startup, before the first request.
var telemetryActive atomic.Bool

// SetTelemetryActive is called once at startup (see cmd/telemetry.go) once
// pkg/telemetry.Resolve has decided whether OTLP exporting is actually
// enabled. It must be set before the first request reaches the pipeline.
func SetTelemetryActive(active bool) { telemetryActive.Store(active) }

// telemetryCounters mirrors, in plain atomics, the same events the OTel
// instruments above record (same call sites — see recordOutcome and the
// Record* helpers below), so pkg/metrics's existing JSON `/metrics`
// endpoint keeps working "fed from the same instruments" whether or not
// OTel exporting is enabled. Reading it costs nothing when telemetry is
// off: the atomics are updated unconditionally (they are cheap int64
// adds), independent of whether an OTLP exporter is configured.
var counters telemetryCounters

type telemetryCounters struct {
	requestsTotal   atomic.Int64
	errorsTotal     atomic.Int64
	redactions      atomic.Int64
	injections      atomic.Int64
	cacheHits       atomic.Int64
	cacheMisses     atomic.Int64
	policyDecisions atomic.Int64
	rateLimitWaits  atomic.Int64
	toolPinEvents   atomic.Int64
	inFlight        atomic.Int64
	governed        atomic.Int64
	governorTrunc   atomic.Int64
	governorSaved   atomic.Int64
	governorProj    atomic.Int64
	governorProjSav atomic.Int64
}

// TelemetryCounters is the plain-value snapshot pkg/metrics exposes on the
// JSON `/metrics` endpoint alongside (not instead of) the OTLP export.
type TelemetryCounters struct {
	RequestsTotal   int64 `json:"requests_total"`
	ErrorsTotal     int64 `json:"errors_total"`
	Redactions      int64 `json:"redactions_total"`
	Injections      int64 `json:"injection_detections_total"`
	CacheHits       int64 `json:"cache_hits_total"`
	CacheMisses     int64 `json:"cache_misses_total"`
	PolicyDecisions int64 `json:"policy_decisions_total"`
	RateLimitWaits  int64 `json:"rate_limit_waits_total"`
	ToolPinEvents   int64 `json:"tool_pin_events_total"`
	InFlight        int64 `json:"requests_in_flight"`
	// Response governor (#319): tool results seen, results shortened, and
	// estimated tokens saved.
	GovernedResults     int64 `json:"governor_results_total"`
	GovernorTruncations int64 `json:"governor_truncations_total"`
	GovernorTokensSaved int64 `json:"governor_tokens_saved_total"`
	// Field projection (#320): results projected, and estimated tokens
	// the projection removed (part of GovernorTokensSaved).
	GovernorProjections           int64 `json:"governor_projections_total"`
	GovernorProjectionTokensSaved int64 `json:"governor_projection_tokens_saved_total"`
}

// TelemetrySnapshot returns the current counters. Safe for concurrent use.
func TelemetrySnapshot() TelemetryCounters {
	return TelemetryCounters{
		RequestsTotal:   counters.requestsTotal.Load(),
		ErrorsTotal:     counters.errorsTotal.Load(),
		Redactions:      counters.redactions.Load(),
		Injections:      counters.injections.Load(),
		CacheHits:       counters.cacheHits.Load(),
		CacheMisses:     counters.cacheMisses.Load(),
		PolicyDecisions: counters.policyDecisions.Load(),
		RateLimitWaits:  counters.rateLimitWaits.Load(),
		ToolPinEvents:   counters.toolPinEvents.Load(),
		InFlight:        counters.inFlight.Load(),

		GovernedResults:     counters.governed.Load(),
		GovernorTruncations: counters.governorTrunc.Load(),
		GovernorTokensSaved: counters.governorSaved.Load(),

		GovernorProjections:           counters.governorProj.Load(),
		GovernorProjectionTokensSaved: counters.governorProjSav.Load(),
	}
}

// RecordRedaction increments the redaction counter (OTel + the plain
// snapshot). Called by the redaction middleware for every pattern match.
func RecordRedaction(ctx context.Context, n int64) {
	if n <= 0 {
		return
	}
	counters.redactions.Add(n)
	instruments().redactionsTotal.Add(ctx, n)
}

// RecordInjectionDetection increments the injection-detection counter for
// action (e.g. "block", "quarantine", "redact", "annotate", "log").
func RecordInjectionDetection(ctx context.Context, action string) {
	counters.injections.Add(1)
	instruments().injectionsTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("action", action)))
}

// RecordCacheHit / RecordCacheMiss increment the response-cache counters.
func RecordCacheHit(ctx context.Context) {
	counters.cacheHits.Add(1)
	instruments().cacheHits.Add(ctx, 1)
}

func RecordCacheMiss(ctx context.Context) {
	counters.cacheMisses.Add(1)
	instruments().cacheMisses.Add(ctx, 1)
}

// RecordPolicyDecision increments the policy-decision counter (#314) for
// outcome (e.g. "allow", "deny", "confirm_approved") and the upstream
// server. Only the outcome and the server name are recorded, never the
// tool's arguments.
func RecordPolicyDecision(ctx context.Context, outcome, server string) {
	counters.policyDecisions.Add(1)
	instruments().policyDecisions.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome), attrMCPServerName.String(server)))
}

// RecordGovernedResult records one tool result the response governor (#319)
// saw: its estimated tokens before and after, and whether it was shortened.
// Only token counts and the server name are recorded, never the result.
func RecordGovernedResult(ctx context.Context, server string, original, returned int64, truncated bool) {
	counters.governed.Add(1)
	if saved := original - returned; saved > 0 {
		counters.governorSaved.Add(saved)
	}
	if truncated {
		counters.governorTrunc.Add(1)
	}
	if !telemetryActive.Load() {
		return
	}
	i := instruments()
	srv := attrMCPServerName.String(server)
	i.governorTokens.Add(ctx, original, metric.WithAttributes(attribute.String("direction", "original"), srv))
	i.governorTokens.Add(ctx, returned, metric.WithAttributes(attribute.String("direction", "returned"), srv))
	if truncated {
		i.governorTrunc.Add(ctx, 1, metric.WithAttributes(srv))
	}
}

// RecordProjection records one tool result the response governor projected
// (#320): the estimated tokens of its projected parts before and after.
// Only token counts and the server name are recorded, never the result.
func RecordProjection(ctx context.Context, server string, before, after int64) {
	counters.governorProj.Add(1)
	if saved := before - after; saved > 0 {
		counters.governorProjSav.Add(saved)
	}
	if !telemetryActive.Load() {
		return
	}
	i := instruments()
	srv := attrMCPServerName.String(server)
	i.governorProj.Add(ctx, 1, metric.WithAttributes(srv))
	i.governorProjTok.Add(ctx, before, metric.WithAttributes(attribute.String("direction", "before"), srv))
	i.governorProjTok.Add(ctx, after, metric.WithAttributes(attribute.String("direction", "after"), srv))
}

// RecordRateLimitWait increments the rate-limit-wait counter.
func RecordRateLimitWait(ctx context.Context) {
	counters.rateLimitWaits.Add(1)
	instruments().rateLimitWaits.Add(ctx, 1)
}

// RecordToolPinEvent counts a tool pinning event (#310) by kind and
// upstream server. Only the event kind and the server name are recorded,
// never a tool definition.
func RecordToolPinEvent(ctx context.Context, event, server string) {
	counters.toolPinEvents.Add(1)
	instruments().toolPinEvents.Add(ctx, 1, metric.WithAttributes(attribute.String("event", event), attrMCPServerName.String(server)))
}

// TelemetryMiddleware is the outermost pipeline stage: it opens the SERVER
// span for the whole request ("<method> <tool>", per the OTel MCP semantic
// conventions), records the request-duration and response-size histograms
// and the requests/errors counters, and tracks in-flight requests. Every
// other middleware and the upstream call nest under this span as long as
// they run inside next(ctx, ...) with the ctx this middleware passes down
// (trace.ContextWithSpan does that automatically for otel.Tracer.Start).
//
// It must be the first entry (outermost) in the slice passed to
// Handler.Use / mcp.Chain so its span covers the whole pipeline, including
// the firewall and cache stages.
func TelemetryMiddleware() Middleware {
	return func(next Next) Next {
		return func(ctx context.Context, req *Request) (*Response, error) {
			if req == nil || req.IsNotification() {
				return next(ctx, req)
			}

			if !telemetryActive.Load() {
				counters.requestsTotal.Add(1)
				counters.inFlight.Add(1)
				resp, err := next(ctx, req)
				counters.inFlight.Add(-1)
				if errorType(resp, err) != "" {
					counters.errorsTotal.Add(1)
				}
				return resp, err
			}

			method := req.Method
			_, tool, _, isToolCall := extractToolCall(req)
			spanName := method
			if isToolCall && tool != "" {
				spanName = method + " " + tool
			}

			// metricAttrs stays low-cardinality (method + tool only) so
			// every metric export stays a small, bounded set of time
			// series; a per-request value (the JSON-RPC id) is only ever
			// attached to the span, never to an instrument.
			metricAttrs := []attribute.KeyValue{
				McpMethodNameAttr(method),
			}
			if isToolCall && tool != "" {
				metricAttrs = append(metricAttrs, attrMCPToolName.String(tool))
			}
			spanAttrs := metricAttrs
			if id := requestIDString(req.ID); id != "" {
				spanAttrs = append(append([]attribute.KeyValue(nil), metricAttrs...), semconv.JSONRPCRequestID(id))
			}

			ctx, span := tracer().Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(spanAttrs...))
			defer span.End()

			counters.requestsTotal.Add(1)
			counters.inFlight.Add(1)
			instruments().requestsTotal.Add(ctx, 1, metric.WithAttributes(metricAttrs...))
			instruments().inFlightRequests.Add(ctx, 1)
			start := time.Now()

			resp, err := next(ctx, req)

			counters.inFlight.Add(-1)
			instruments().inFlightRequests.Add(ctx, -1)
			duration := time.Since(start)
			instruments().requestDuration.Record(ctx, float64(duration.Microseconds())/1000, metric.WithAttributes(metricAttrs...))

			errType := errorType(resp, err)
			if errType != "" {
				counters.errorsTotal.Add(1)
				errAttrs := append(append([]attribute.KeyValue(nil), metricAttrs...), semconv.ErrorTypeKey.String(errType))
				instruments().errorsTotal.Add(ctx, 1, metric.WithAttributes(errAttrs...))
				span.SetAttributes(semconv.ErrorTypeKey.String(errType))
				span.SetStatus(codes.Error, errType)
			} else {
				span.SetStatus(codes.Ok, "")
			}
			if resp != nil && len(resp.Result) > 0 {
				instruments().responseBytes.Record(ctx, int64(len(resp.Result)), metric.WithAttributes(metricAttrs...))
				span.SetAttributes(attribute.Int("mcp.response.size_bytes", len(resp.Result)))
			}
			if err != nil {
				span.RecordError(err)
			}

			return resp, err
		}
	}
}

// errorType classifies the outcome of a request into the OTel `error.type`
// convention's low-cardinality string: "" for success, the transport error
// type name for a Go error, or the JSON-RPC error code for a structured
// error response.
func errorType(resp *Response, err error) string {
	if err != nil {
		return fmt.Sprintf("%T", err)
	}
	if resp != nil && resp.Error != nil {
		return fmt.Sprintf("jsonrpc_%d", resp.Error.Code)
	}
	return ""
}

// requestIDString renders a JSON-RPC id (string, float64/number, or nil) as
// the string jsonrpc.request.id wants, without ever including it when it is
// absent (a notification, already filtered out above, has none).
func requestIDString(id interface{}) string {
	switch v := id.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		return fmt.Sprintf("%v", v)
	}
}

// McpMethodNameAttr returns the semconv `mcp.method.name` attribute. It is
// exported so other packages (e.g. serve's own dispatch loop) that build
// spans outside the pkg/mcp pipeline can reuse the same key.
func McpMethodNameAttr(method string) attribute.KeyValue {
	return semconv.McpMethodNameKey.String(method)
}

// Traced wraps a middleware with a named child span ("mcp.middleware.<name>"),
// used to give each firewall/cache stage its own span under the request's
// SERVER span (issue #317: "child spans for each middleware"). It is a
// no-op wrapper cost-wise when telemetry is disabled (tracer().Start returns
// a no-op span with the parent context unchanged).
func Traced(name string, mw Middleware) Middleware {
	if mw == nil {
		return nil
	}
	return func(next Next) Next {
		wrapped := mw(next)
		return func(ctx context.Context, req *Request) (*Response, error) {
			if !telemetryActive.Load() {
				return wrapped(ctx, req)
			}
			ctx, span := tracer().Start(ctx, "mcp.middleware."+name, trace.WithSpanKind(trace.SpanKindInternal))
			defer span.End()
			resp, err := wrapped(ctx, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			}
			return resp, err
		}
	}
}

// UpstreamServerNameAttr and UpstreamSpanName are shared with the CLIENT
// span the handler starts around the upstream call (see sendUpstream in
// handlers.go), kept here so the attribute key stays in one place.
func UpstreamServerNameAttr(server string) attribute.KeyValue {
	return attrMCPServerName.String(server)
}
