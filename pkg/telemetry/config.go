// Package telemetry is the optional OpenTelemetry bootstrap for leanproxy-mcp
// (issue #317): OTLP/HTTP trace and metric exporters, off by default. It
// never touches the global otel providers unless explicitly enabled, so a
// deployment that does not configure telemetry pays no exporter cost and
// starts no background goroutines — otel's default no-op providers stay in
// effect and every span/metric call pkg/mcp makes is a cheap no-op.
//
// Enabling telemetry never changes what is recorded: pkg/mcp attaches only
// names, sizes, durations, counts and status codes to spans and metrics,
// never argument or result payloads (see pkg/mcp's telemetry.go).
package telemetry

import (
	"fmt"
	"os"
	"strings"
)

// Protocol is the OTLP wire protocol used for both traces and metrics. Only
// HTTP is supported (issue #317 deliberately excludes the gRPC exporter to
// keep the binary size delta small).
type Protocol string

const (
	// ProtocolHTTPProtobuf is the OTLP/HTTP exporter with protobuf bodies
	// (the OTEL_EXPORTER_OTLP_PROTOCOL default, "http/protobuf").
	ProtocolHTTPProtobuf Protocol = "http/protobuf"
	// ProtocolHTTPJSON is the OTLP/HTTP exporter with JSON bodies
	// ("http/json").
	ProtocolHTTPJSON Protocol = "http/json"
)

// Config is the `telemetry:` YAML block plus the standard OTel environment
// variables. A zero Config is disabled.
type Config struct {
	// Enabled is the master switch. Even with Endpoint set, telemetry stays
	// off unless Enabled is true or an OTEL_EXPORTER_OTLP_*ENDPOINT env var
	// is present (env vars are the standard, expected way to turn this on
	// per issue #317, so they imply Enabled).
	Enabled bool `yaml:"enabled,omitempty"`

	// OTLP holds the exporter endpoint/protocol. Absent means "read from
	// OTEL_EXPORTER_OTLP_ENDPOINT / OTEL_EXPORTER_OTLP_PROTOCOL" (or
	// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT / _METRICS_ENDPOINT for
	// per-signal overrides, per the OTel spec).
	OTLP *OTLPConfig `yaml:"otlp,omitempty"`

	// ServiceName overrides the `service.name` resource attribute reported
	// to the collector. Defaults to "leanproxy-mcp".
	ServiceName string `yaml:"service_name,omitempty"`
}

// OTLPConfig is the `telemetry.otlp:` block.
type OTLPConfig struct {
	// Endpoint is the collector base URL, e.g. "http://localhost:4318".
	// Empty means "read OTEL_EXPORTER_OTLP_ENDPOINT".
	Endpoint string `yaml:"endpoint,omitempty"`
	// Protocol is "http/protobuf" (default) or "http/json". Empty means
	// "read OTEL_EXPORTER_OTLP_PROTOCOL", defaulting to http/protobuf.
	Protocol string `yaml:"protocol,omitempty"`
	// Insecure allows a plain-HTTP (non-TLS) endpoint. Inferred from the
	// endpoint's scheme when false and the endpoint is http://.
	Insecure bool `yaml:"insecure,omitempty"`
}

// DefaultServiceName is the `service.name` resource attribute used when
// Config.ServiceName is empty.
const DefaultServiceName = "leanproxy-mcp"

// Resolved is the effective, environment-merged configuration Init consumes.
type Resolved struct {
	Enabled         bool
	TracesEndpoint  string
	MetricsEndpoint string
	Protocol        Protocol
	Insecure        bool
	ServiceName     string
}

// otelEnv reads the standard OTel SDK environment variables (see
// https://opentelemetry.io/docs/specs/otel/protocol/exporter/). Only the
// generic and OTLP-specific variables this exporter honors are read; the SDK
// itself is not used for env parsing to keep the dependency surface small.
type otelEnv struct {
	endpoint        string
	tracesEndpoint  string
	metricsEndpoint string
	protocol        string
	insecure        bool
}

func readOtelEnv() otelEnv {
	insecure, _ := lookupBool("OTEL_EXPORTER_OTLP_INSECURE")
	return otelEnv{
		endpoint:        os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
		tracesEndpoint:  os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		metricsEndpoint: os.Getenv("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"),
		protocol:        os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"),
		insecure:        insecure,
	}
}

func lookupBool(name string) (bool, bool) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return false, false
	}
	return strings.EqualFold(v, "true"), true
}

// Resolve merges cfg (the `telemetry:` config block, possibly nil) with the
// standard OTEL_EXPORTER_OTLP_* environment variables, which always win over
// the config block, matching every other OTel SDK's precedence rules.
// Telemetry is enabled when cfg.Enabled is true, or when any endpoint is
// configured (env or config) — an operator who sets an endpoint clearly
// wants telemetry on, without also having to set `enabled: true`.
func Resolve(cfg *Config) (Resolved, error) {
	env := readOtelEnv()

	r := Resolved{
		ServiceName: DefaultServiceName,
		Protocol:    ProtocolHTTPProtobuf,
	}

	var cfgEndpoint, cfgProtocol string
	var cfgInsecure bool
	if cfg != nil {
		r.Enabled = cfg.Enabled
		if cfg.ServiceName != "" {
			r.ServiceName = cfg.ServiceName
		}
		if cfg.OTLP != nil {
			cfgEndpoint = cfg.OTLP.Endpoint
			cfgProtocol = cfg.OTLP.Protocol
			cfgInsecure = cfg.OTLP.Insecure
		}
	}

	endpoint := firstNonEmpty(env.endpoint, cfgEndpoint)
	protocol := firstNonEmpty(env.protocol, cfgProtocol)
	tracesEndpoint := firstNonEmpty(env.tracesEndpoint, endpoint)
	metricsEndpoint := firstNonEmpty(env.metricsEndpoint, endpoint)

	if endpoint != "" || tracesEndpoint != "" || metricsEndpoint != "" {
		r.Enabled = true
	}
	if env.insecure || cfgInsecure {
		r.Insecure = true
	}

	switch Protocol(protocol) {
	case "", ProtocolHTTPProtobuf:
		r.Protocol = ProtocolHTTPProtobuf
	case ProtocolHTTPJSON:
		r.Protocol = ProtocolHTTPJSON
	default:
		return Resolved{}, fmt.Errorf("telemetry: unsupported OTEL_EXPORTER_OTLP_PROTOCOL %q (leanproxy-mcp only ships the OTLP/HTTP exporter: use %q or %q)",
			protocol, ProtocolHTTPProtobuf, ProtocolHTTPJSON)
	}

	r.TracesEndpoint = tracesEndpoint
	r.MetricsEndpoint = metricsEndpoint

	if !r.Enabled {
		return r, nil
	}
	if r.TracesEndpoint == "" {
		return Resolved{}, fmt.Errorf("telemetry: enabled but no OTLP endpoint set (OTEL_EXPORTER_OTLP_ENDPOINT or telemetry.otlp.endpoint)")
	}
	return r, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
