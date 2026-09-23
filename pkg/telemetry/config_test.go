package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolve_DisabledByDefault(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")

	r, err := Resolve(nil)
	require.NoError(t, err)
	assert.False(t, r.Enabled)
	assert.Equal(t, DefaultServiceName, r.ServiceName)
}

func TestResolve_EnvEndpointEnablesTelemetry(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")

	r, err := Resolve(nil)
	require.NoError(t, err)
	assert.True(t, r.Enabled)
	assert.Equal(t, "http://localhost:4318", r.TracesEndpoint)
	assert.Equal(t, "http://localhost:4318", r.MetricsEndpoint)
	assert.Equal(t, ProtocolHTTPProtobuf, r.Protocol)
}

func TestResolve_ConfigBlockEnablesTelemetry(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")

	cfg := &Config{
		OTLP:        &OTLPConfig{Endpoint: "collector.internal:4318", Protocol: "http/json"},
		ServiceName: "custom-service",
	}
	r, err := Resolve(cfg)
	require.NoError(t, err)
	assert.True(t, r.Enabled)
	assert.Equal(t, "collector.internal:4318", r.TracesEndpoint)
	assert.Equal(t, ProtocolHTTPJSON, r.Protocol)
	assert.Equal(t, "custom-service", r.ServiceName)
}

func TestResolve_EnvWinsOverConfig(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://env-endpoint:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")

	cfg := &Config{OTLP: &OTLPConfig{Endpoint: "config-endpoint:4318"}}
	r, err := Resolve(cfg)
	require.NoError(t, err)
	assert.Equal(t, "http://env-endpoint:4318", r.TracesEndpoint)
}

func TestResolve_UnsupportedProtocolRejected(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

	_, err := Resolve(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported")
}

func TestResolve_EnabledButNoEndpointErrors(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")

	cfg := &Config{Enabled: true}
	_, err := Resolve(cfg)
	require.Error(t, err)
}

func TestResolve_PerSignalEndpointOverride(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://base:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "http://traces-only:4318")
	t.Setenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "")

	r, err := Resolve(nil)
	require.NoError(t, err)
	assert.Equal(t, "http://traces-only:4318", r.TracesEndpoint)
	assert.Equal(t, "http://base:4318", r.MetricsEndpoint)
}
