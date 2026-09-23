package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// jsonTraceExporter implements sdktrace.SpanExporter using OTLP/HTTP JSON
// (see otlpjson.go for why this exists instead of otlptracehttp).
type jsonTraceExporter struct {
	client *otlpClient
}

func newJSONTraceExporter(endpoint string) *jsonTraceExporter {
	return &jsonTraceExporter{client: newOTLPClient(endpoint)}
}

// jsonSpan mirrors OTLP's Span message (traces v1).
type jsonSpan struct {
	TraceID           string          `json:"traceId"`
	SpanID            string          `json:"spanId"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	Name              string          `json:"name"`
	Kind              int             `json:"kind"`
	StartTimeUnixNano string          `json:"startTimeUnixNano"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano"`
	Attributes        []jsonKV        `json:"attributes,omitempty"`
	Status            *jsonSpanStatus `json:"status,omitempty"`
}

type jsonSpanStatus struct {
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type jsonScopeSpans struct {
	Scope jsonScope  `json:"scope"`
	Spans []jsonSpan `json:"spans"`
}

type jsonResourceSpans struct {
	Resource   jsonResource     `json:"resource"`
	ScopeSpans []jsonScopeSpans `json:"scopeSpans"`
}

type jsonTraceRequest struct {
	ResourceSpans []jsonResourceSpans `json:"resourceSpans"`
}

// otlpSpanKind maps otel's SpanKind to the OTLP wire enum
// (SPAN_KIND_UNSPECIFIED=0 .. SPAN_KIND_CONSUMER=5).
func otlpSpanKind(k trace.SpanKind) int {
	switch k {
	case trace.SpanKindInternal:
		return 1
	case trace.SpanKindServer:
		return 2
	case trace.SpanKindClient:
		return 3
	case trace.SpanKindProducer:
		return 4
	case trace.SpanKindConsumer:
		return 5
	default:
		return 0
	}
}

// otlpStatusCode maps otel's codes.Code to OTLP's Status.code
// (STATUS_CODE_UNSET=0, STATUS_CODE_OK=1, STATUS_CODE_ERROR=2).
func otlpStatusCode(c codes.Code) int {
	switch c {
	case codes.Ok:
		return 1
	case codes.Error:
		return 2
	default:
		return 0
	}
}

func unixNano(t time.Time) string {
	if t.IsZero() {
		return "0"
	}
	return itoa64(t.UnixNano())
}

func itoa64(v int64) string {
	// Avoids pulling in strconv's larger formatting paths for the one shape
	// used here.
	if v < 0 {
		return "-" + utoa64(uint64(-v))
	}
	return utoa64(uint64(v))
}

// utoa64 renders v in decimal without going through a signed conversion, so
// callers with an already-unsigned count (histogram bucket counts, span
// counts) never trip an int64-overflow lint on values above 1<<63.
func utoa64(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func (e *jsonTraceExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	// Group by resource+scope is unnecessary here: the SDK's batch span
	// processor only ever hands us spans from this one process's single
	// Resource, and pkg/mcp uses a single instrumentation scope, so one
	// resourceSpans/scopeSpans entry covers every span in the batch.
	jsonSpans := make([]jsonSpan, 0, len(spans))
	var scope jsonScope
	var res jsonResource
	for i, s := range spans {
		if i == 0 {
			scope = scopeToJSON(s.InstrumentationScope())
			res = resourceToJSON(s.Resource())
		}
		sc := s.SpanContext()
		tid, sid := sc.TraceID(), sc.SpanID()
		js := jsonSpan{
			TraceID:           hexID(tid[:]),
			SpanID:            hexID(sid[:]),
			Name:              s.Name(),
			Kind:              otlpSpanKind(s.SpanKind()),
			StartTimeUnixNano: unixNano(s.StartTime()),
			EndTimeUnixNano:   unixNano(s.EndTime()),
			Attributes:        attrsToKVs(s.Attributes()),
		}
		if p := s.Parent(); p.IsValid() {
			pid := p.SpanID()
			js.ParentSpanID = hexID(pid[:])
		}
		if st := s.Status(); st.Code != codes.Unset || st.Description != "" {
			js.Status = &jsonSpanStatus{Code: otlpStatusCode(st.Code), Message: st.Description}
		}
		jsonSpans = append(jsonSpans, js)
	}

	reqBody := jsonTraceRequest{ResourceSpans: []jsonResourceSpans{{
		Resource:   res,
		ScopeSpans: []jsonScopeSpans{{Scope: scope, Spans: jsonSpans}},
	}}}
	body, err := marshalJSON(reqBody)
	if err != nil {
		return err
	}
	return e.client.post(ctx, body)
}

func (e *jsonTraceExporter) Shutdown(ctx context.Context) error { return nil }
