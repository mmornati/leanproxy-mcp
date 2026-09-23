package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestWrapTransport_InjectsTraceparentWhenSpanRecording(t *testing.T) {
	exp := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	prevTP, prevProp := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	var gotHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: WrapTransport(http.DefaultTransport)}

	ctx, span := tp.Tracer("test").Start(context.Background(), "test-span")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	span.End()

	assert.NotEmpty(t, gotHeader, "expected a traceparent header on the upstream request")
	assert.Contains(t, gotHeader, span.SpanContext().TraceID().String())
}

func TestWrapTransport_NoOpWhenTelemetryDisabled(t *testing.T) {
	// The default global propagator (no telemetry.Init call) is a no-op
	// composite: no traceparent header should be added.
	prevProp := otel.GetTextMapPropagator()
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
	t.Cleanup(func() { otel.SetTextMapPropagator(prevProp) })

	var gotHeader string
	var sawHeader bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader, sawHeader = r.Header.Get("traceparent"), r.Header.Get("traceparent") != ""
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{Transport: WrapTransport(http.DefaultTransport)}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	assert.False(t, sawHeader, "no traceparent header expected: got %q", gotHeader)
}

func TestWrapTransport_NilBaseDefaultsToDefaultTransport(t *testing.T) {
	rt := WrapTransport(nil)
	require.NotNil(t, rt)
}
