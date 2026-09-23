package telemetry

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
)

// This file, plus otlpjson_trace.go and otlpjson_metric.go, is a deliberately
// minimal, hand-written OTLP/HTTP exporter using the JSON encoding the OTLP
// spec requires every HTTP receiver to accept
// (https://opentelemetry.io/docs/specs/otlp/#otlphttp — "Response
// requirements" mandates Content-Type: application/json support alongside
// protobuf), instead of go.opentelemetry.io/otel/exporters/otlp/*.
//
// Why: those exporters' otlptracehttp/otlpmetrichttp packages transitively
// pull in google.golang.org/grpc and google.golang.org/protobuf through an
// internal config package they share with the gRPC exporter variant (a
// long-standing upstream design choice; see
// open-telemetry/opentelemetry-go#4783), which alone added roughly 6 MB to
// this binary — well over issue #317's +3 MB budget. Every otel-go release
// through v1.46 has the same shared-internal-config shape, so avoiding it
// means not depending on those packages at all. This file only depends on
// go.opentelemetry.io/otel/sdk's own types (ReadOnlySpan, metricdata) plus
// the standard library, which keeps the size delta small (see the PR
// description for the measured before/after).
//
// It supports exactly what pkg/mcp emits: string/int64/float64/bool
// attributes, the trace SpanKind/Status vocabulary OTLP defines, and the
// Sum/Histogram/Gauge aggregations the meter provider in provider.go
// produces. It never serializes argument or result payload content — see
// pkg/mcp/telemetry.go for what actually gets attached to a span or metric.

// otlpClient posts an OTLP JSON request body to endpoint+path.
type otlpClient struct {
	httpClient *http.Client
	url        string
}

func newOTLPClient(url string) *otlpClient {
	return &otlpClient{httpClient: &http.Client{}, url: url}
}

func (c *otlpClient) post(ctx context.Context, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("otlp export to %s: %w", c.url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("otlp export to %s: status %s", c.url, resp.Status)
	}
	return nil
}

// jsonAny is the OTLP `AnyValue` shape: exactly one of these fields is set.
type jsonAny struct {
	StringValue *string  `json:"stringValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"` // OTLP JSON encodes int64 as a string
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

type jsonKV struct {
	Key   string  `json:"key"`
	Value jsonAny `json:"value"`
}

// toKV converts an otel attribute.KeyValue to its OTLP JSON representation.
// Slice-valued attributes are rendered as their Go %v string form: pkg/mcp
// never attaches array-valued attributes, so this keeps the encoder small
// rather than implementing OTLP's arrayValue for an unused case.
func toKV(kv attribute.KeyValue) jsonKV {
	out := jsonKV{Key: string(kv.Key)}
	switch kv.Value.Type() {
	case attribute.BOOL:
		v := kv.Value.AsBool()
		out.Value.BoolValue = &v
	case attribute.INT64:
		v := fmt.Sprintf("%d", kv.Value.AsInt64())
		out.Value.IntValue = &v
	case attribute.FLOAT64:
		v := kv.Value.AsFloat64()
		out.Value.DoubleValue = &v
	default:
		v := kv.Value.String()
		out.Value.StringValue = &v
	}
	return out
}

func attrsToKVs(attrs []attribute.KeyValue) []jsonKV {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]jsonKV, len(attrs))
	for i, a := range attrs {
		out[i] = toKV(a)
	}
	return out
}

func setToKVs(set attribute.Set) []jsonKV {
	if set.Len() == 0 {
		return nil
	}
	iter := set.Iter()
	out := make([]jsonKV, 0, set.Len())
	for iter.Next() {
		out = append(out, toKV(iter.Attribute()))
	}
	return out
}

type jsonResource struct {
	Attributes []jsonKV `json:"attributes,omitempty"`
}

func resourceToJSON(res *resource.Resource) jsonResource {
	if res == nil {
		return jsonResource{}
	}
	iter := res.Iter()
	attrs := make([]attribute.KeyValue, 0, iter.Len())
	for iter.Next() {
		attrs = append(attrs, iter.Attribute())
	}
	return jsonResource{Attributes: attrsToKVs(attrs)}
}

type jsonScope struct {
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

func scopeToJSON(s instrumentation.Scope) jsonScope {
	return jsonScope{Name: s.Name, Version: s.Version}
}

// hexID renders a trace/span ID's raw bytes as lowercase hex, the encoding
// OTLP JSON uses for traceId/spanId/parentSpanId.
func hexID(b []byte) string { return hex.EncodeToString(b) }

func marshalJSON(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}
