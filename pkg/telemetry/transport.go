package telemetry

import (
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// WrapTransport wraps an http.RoundTripper so every outgoing request carries
// the current trace context as a W3C `traceparent` header (issue #317,
// acceptance criterion "an upstream HTTP server receives a traceparent
// header"). It reads the span from the request's own context, so callers
// must build requests with http.NewRequestWithContext using a context that
// carries the span pkg/mcp started for the call.
//
// This is always safe to install, telemetry enabled or not: with telemetry
// disabled the global propagator stays the SDK's default no-op composite
// propagator, so Inject is a cheap type switch that writes nothing.
func WrapTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &tracingTransport{base: base}
}

type tracingTransport struct {
	base http.RoundTripper
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone so header propagation never mutates a request another goroutine
	// might retry or reuse (net/http's own RoundTripper contract already
	// requires callers not to mutate a Request they don't own, but the
	// header map itself is otherwise shared).
	req = req.Clone(req.Context())
	otel.GetTextMapPropagator().Inject(req.Context(), propagation.HeaderCarrier(req.Header))
	return t.base.RoundTrip(req)
}
