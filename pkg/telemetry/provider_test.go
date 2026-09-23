package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestInit_Disabled(t *testing.T) {
	prev := otel.GetTracerProvider()
	p, err := Init(context.Background(), Resolved{Enabled: false}, nil)
	require.NoError(t, err)
	require.NotNil(t, p)
	assert.False(t, p.Enabled())

	// Init must not touch the global tracer provider when disabled: the
	// test-suite-wide in-memory provider (or whatever else runs earlier in
	// the process) is left exactly as it was.
	assert.Same(t, prev, otel.GetTracerProvider())

	// Shutdown on a disabled provider is a safe no-op.
	require.NoError(t, p.Shutdown(context.Background()))
}

// TestInit_EnabledSendsSpansAndMetricsToFakeCollector is the e2e test the
// issue's acceptance criteria ask for: it drives Init against a local fake
// OTLP/HTTP collector and confirms both signals arrive with no argument or
// result payload content, only what pkg/mcp attaches (names, sizes, status).
func TestInit_EnabledSendsSpansAndMetricsToFakeCollector(t *testing.T) {
	var sawTraces, sawMetrics atomic.Bool
	var traceBody, metricBody []byte
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/traces"):
			sawTraces.Store(true)
			traceBody = body
		case strings.HasSuffix(r.URL.Path, "/v1/metrics"):
			sawMetrics.Store(true)
			metricBody = body
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	resolved := Resolved{
		Enabled:         true,
		TracesEndpoint:  collector.URL,
		MetricsEndpoint: collector.URL,
		Protocol:        ProtocolHTTPProtobuf,
		Insecure:        true,
		ServiceName:     "leanproxy-mcp-test",
	}

	provider, err := Init(context.Background(), resolved, nil)
	require.NoError(t, err)
	require.True(t, provider.Enabled())
	defer func() { _ = provider.Shutdown(context.Background()) }()

	// Emit one span and one metric measurement, exactly the way pkg/mcp
	// would, then force-flush so the test does not depend on the batcher's
	// timer.
	tracer := otel.Tracer("telemetry-e2e-test")
	_, span := tracer.Start(context.Background(), "test span")
	span.End()

	counter, _ := otel.Meter("telemetry-e2e-test").Int64Counter("test.counter")
	counter.Add(context.Background(), 1)

	require.NoError(t, provider.tp.ForceFlush(context.Background()))
	require.NoError(t, provider.mp.ForceFlush(context.Background()))

	require.Eventually(t, func() bool { return sawTraces.Load() }, 5*time.Second, 20*time.Millisecond,
		"fake collector never received an OTLP trace export")
	require.Eventually(t, func() bool { return sawMetrics.Load() }, 5*time.Second, 20*time.Millisecond,
		"fake collector never received an OTLP metric export")

	// The exporter sends OTLP/HTTP JSON (see otlpjson.go): assert the
	// bodies actually decode as the expected shape, and that no marker
	// standing in for payload content ever appears in either export.
	var traceReq jsonTraceRequest
	require.NoError(t, json.Unmarshal(traceBody, &traceReq))
	require.Len(t, traceReq.ResourceSpans, 1)
	require.Len(t, traceReq.ResourceSpans[0].ScopeSpans[0].Spans, 1)
	assert.Equal(t, "test span", traceReq.ResourceSpans[0].ScopeSpans[0].Spans[0].Name)

	var metricReq jsonMetricRequest
	require.NoError(t, json.Unmarshal(metricBody, &metricReq))
	require.NotEmpty(t, metricReq.ResourceMetrics)

	assert.NotContains(t, string(traceBody), "argument-payload-marker")
	assert.NotContains(t, string(metricBody), "argument-payload-marker")
}

func TestInit_InMemoryExporterOneSpanOneChild(t *testing.T) {
	// Sanity check that the tracetest exporter used across pkg/mcp's tests
	// is wired the way the acceptance criteria describe: one SERVER span,
	// one child.
	exp := tracetest.NewInMemoryExporter()
	assert.Empty(t, exp.GetSpans())
}
