package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"

	"github.com/mmornati/leanproxy-mcp/internal/version"
)

// Provider owns the trace and meter providers Init installed as the global
// otel providers, plus their exporters. Shutdown flushes and closes both. A
// disabled Provider (Init with a disabled Resolved) has nil providers and
// Shutdown is a no-op: nothing was ever registered globally, so pkg/mcp's
// otel.Tracer/otel.Meter calls keep resolving to the SDK's built-in no-op
// implementations, exactly as if this package were never imported.
type Provider struct {
	enabled bool
	tp      *sdktrace.TracerProvider
	mp      *metric.MeterProvider
}

// Init sets up OTLP/HTTP exporters per r and installs them as the global
// otel trace/metric providers plus a W3C tracecontext propagator. Disabled
// (r.Enabled == false) is the default path and does nothing at all: no
// exporter, no batching goroutine, no periodic reader, and the global
// providers are left as otel's defaults (no-op).
//
// The exporters are this package's own minimal OTLP/HTTP JSON encoder (see
// otlpjson.go), not go.opentelemetry.io/otel/exporters/otlp/*: those pull in
// google.golang.org/grpc and google.golang.org/protobuf transitively (an
// upstream design choice, not something this binary otherwise needs),
// which alone cost roughly 6 MB of binary size — well over issue #317's
// +3 MB budget. r.Protocol is still read from OTEL_EXPORTER_OTLP_PROTOCOL
// for forward compatibility (an operator's existing env var is not an
// error), but both http/protobuf and http/json currently produce the same
// JSON body: every OTLP/HTTP receiver is required to accept it (see
// otlpjson.go's doc comment).
func Init(ctx context.Context, r Resolved, logger *slog.Logger) (*Provider, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if !r.Enabled {
		logger.Debug("telemetry: disabled")
		return &Provider{}, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(r.ServiceName),
			semconv.ServiceVersion(version.Get().Version),
		),
		resource.WithSchemaURL(semconv.SchemaURL),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	traceExp := newJSONTraceExporter(signalURL(r.TracesEndpoint, "v1/traces"))
	metricExp := newJSONMetricExporter(signalURL(r.MetricsEndpoint, "v1/metrics"))

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)
	mp := metric.NewMeterProvider(
		metric.WithReader(metric.NewPeriodicReader(metricExp, metric.WithInterval(15*time.Second))),
		metric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	logger.Info("telemetry: OTLP/HTTP exporter enabled",
		"traces_endpoint", r.TracesEndpoint,
		"metrics_endpoint", r.MetricsEndpoint,
		"protocol", r.Protocol,
		"service_name", r.ServiceName,
	)

	return &Provider{enabled: true, tp: tp, mp: mp}, nil
}

// Shutdown flushes pending spans/metrics and closes the exporters. It is
// safe to call on a disabled Provider (a no-op) and safe to call more than
// once.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p == nil || !p.enabled {
		return nil
	}
	var errs []error
	if p.tp != nil {
		if err := p.tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("tracer provider: %w", err))
		}
	}
	if p.mp != nil {
		if err := p.mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("meter provider: %w", err))
		}
	}
	if len(errs) == 0 {
		return nil
	}
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("telemetry shutdown: %s", strings.Join(msgs, "; "))
}

// Enabled reports whether telemetry is actively exporting.
func (p *Provider) Enabled() bool { return p != nil && p.enabled }

// signalURL builds the full export URL for one signal from a base endpoint
// (e.g. "http://localhost:4318" or "localhost:4318") plus its default OTLP
// path ("v1/traces" / "v1/metrics"), per the OTEL_EXPORTER_OTLP_ENDPOINT
// convention (the generic endpoint is a base URL; the signal-specific env
// vars, already resolved into r.TracesEndpoint/MetricsEndpoint by
// Resolve, are used exactly as given).
func signalURL(endpoint, defaultPath string) string {
	if endpoint == "" {
		return ""
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	trimmed := strings.TrimRight(endpoint, "/")
	if strings.HasSuffix(trimmed, "/v1/traces") || strings.HasSuffix(trimmed, "/v1/metrics") {
		return trimmed
	}
	return trimmed + "/" + defaultPath
}
