package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/trace"
)

func TestToKV_AllAttributeTypes(t *testing.T) {
	cases := []struct {
		name string
		kv   attribute.KeyValue
		want jsonAny
	}{
		{"string", attribute.String("k", "v"), jsonAny{StringValue: strPtr("v")}},
		{"bool", attribute.Bool("k", true), jsonAny{BoolValue: boolPtr(true)}},
		{"int64", attribute.Int64("k", 42), jsonAny{IntValue: strPtr("42")}},
		{"float64", attribute.Float64("k", 3.5), jsonAny{DoubleValue: float64Ptr(3.5)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := toKV(c.kv)
			assert.Equal(t, "k", got.Key)
			assert.Equal(t, c.want, got.Value)
		})
	}
}

func TestOtlpSpanKind(t *testing.T) {
	assert.Equal(t, 2, otlpSpanKind(trace.SpanKindServer))
	assert.Equal(t, 3, otlpSpanKind(trace.SpanKindClient))
	assert.Equal(t, 1, otlpSpanKind(trace.SpanKindInternal))
	assert.Equal(t, 0, otlpSpanKind(trace.SpanKindUnspecified))
}

func TestOtlpStatusCode(t *testing.T) {
	assert.Equal(t, 1, otlpStatusCode(codes.Ok))
	assert.Equal(t, 2, otlpStatusCode(codes.Error))
	assert.Equal(t, 0, otlpStatusCode(codes.Unset))
}

func TestItoa64(t *testing.T) {
	assert.Equal(t, "0", itoa64(0))
	assert.Equal(t, "42", itoa64(42))
	assert.Equal(t, "-42", itoa64(-42))
	assert.Equal(t, "9223372036854775807", itoa64(9223372036854775807))
}

func TestConvertAggregation_Sum(t *testing.T) {
	m := metricdata.Metrics{
		Name: "mcp.server.requests",
		Data: metricdata.Sum[int64]{
			Temporality: metricdata.CumulativeTemporality,
			IsMonotonic: true,
			DataPoints: []metricdata.DataPoint[int64]{
				{Value: 7, Attributes: attribute.NewSet(attribute.String("mcp.method.name", "tools/call"))},
			},
		},
	}
	out := convertAggregation(m)
	require.NotNil(t, out.Sum)
	assert.True(t, out.Sum.IsMonotonic)
	require.Len(t, out.Sum.DataPoints, 1)
	require.NotNil(t, out.Sum.DataPoints[0].AsInt)
	assert.Equal(t, "7", *out.Sum.DataPoints[0].AsInt)
}

func TestConvertAggregation_Histogram(t *testing.T) {
	m := metricdata.Metrics{
		Name: "mcp.server.request.duration",
		Data: metricdata.Histogram[float64]{
			Temporality: metricdata.CumulativeTemporality,
			DataPoints: []metricdata.HistogramDataPoint[float64]{
				{
					Count:        3,
					Sum:          12.5,
					Bounds:       []float64{1, 5, 10},
					BucketCounts: []uint64{1, 1, 1, 0},
				},
			},
		},
	}
	out := convertAggregation(m)
	require.NotNil(t, out.Histogram)
	require.Len(t, out.Histogram.DataPoints, 1)
	dp := out.Histogram.DataPoints[0]
	assert.Equal(t, "3", dp.Count)
	require.NotNil(t, dp.Sum)
	assert.InDelta(t, 12.5, *dp.Sum, 0.001)
	assert.Equal(t, []float64{1, 5, 10}, dp.ExplicitBounds)
}

func strPtr(s string) *string       { return &s }
func boolPtr(b bool) *bool          { return &b }
func float64Ptr(f float64) *float64 { return &f }
