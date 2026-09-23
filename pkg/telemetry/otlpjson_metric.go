package telemetry

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// jsonMetricExporter implements sdkmetric.Exporter using OTLP/HTTP JSON (see
// otlpjson.go for why this exists instead of otlpmetrichttp). It uses the
// SDK's own default temporality/aggregation selection (cumulative
// temporality, explicit-bucket histograms) — pkg/mcp never changes those.
type jsonMetricExporter struct {
	client *otlpClient
}

func newJSONMetricExporter(endpoint string) *jsonMetricExporter {
	return &jsonMetricExporter{client: newOTLPClient(endpoint)}
}

func (e *jsonMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return sdkmetric.DefaultTemporalitySelector(k)
}

func (e *jsonMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(k)
}

type jsonNumberDataPoint struct {
	Attributes        []jsonKV `json:"attributes,omitempty"`
	StartTimeUnixNano string   `json:"startTimeUnixNano,omitempty"`
	TimeUnixNano      string   `json:"timeUnixNano,omitempty"`
	AsInt             *string  `json:"asInt,omitempty"`
	AsDouble          *float64 `json:"asDouble,omitempty"`
}

type jsonHistogramDataPoint struct {
	Attributes        []jsonKV  `json:"attributes,omitempty"`
	StartTimeUnixNano string    `json:"startTimeUnixNano,omitempty"`
	TimeUnixNano      string    `json:"timeUnixNano,omitempty"`
	Count             string    `json:"count"`
	Sum               *float64  `json:"sum,omitempty"`
	BucketCounts      []string  `json:"bucketCounts,omitempty"`
	ExplicitBounds    []float64 `json:"explicitBounds,omitempty"`
}

type jsonMetric struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Unit        string `json:"unit,omitempty"`

	Sum       *jsonSum       `json:"sum,omitempty"`
	Gauge     *jsonGauge     `json:"gauge,omitempty"`
	Histogram *jsonHistogram `json:"histogram,omitempty"`
}

type jsonSum struct {
	DataPoints             []jsonNumberDataPoint `json:"dataPoints"`
	AggregationTemporality int                   `json:"aggregationTemporality"`
	IsMonotonic            bool                  `json:"isMonotonic"`
}

type jsonGauge struct {
	DataPoints []jsonNumberDataPoint `json:"dataPoints"`
}

type jsonHistogram struct {
	DataPoints             []jsonHistogramDataPoint `json:"dataPoints"`
	AggregationTemporality int                      `json:"aggregationTemporality"`
}

type jsonScopeMetrics struct {
	Scope   jsonScope    `json:"scope"`
	Metrics []jsonMetric `json:"metrics"`
}

type jsonResourceMetrics struct {
	Resource     jsonResource       `json:"resource"`
	ScopeMetrics []jsonScopeMetrics `json:"scopeMetrics"`
}

type jsonMetricRequest struct {
	ResourceMetrics []jsonResourceMetrics `json:"resourceMetrics"`
}

// otlpTemporality maps the SDK's Temporality to OTLP's wire enum
// (UNSPECIFIED=0, DELTA=1, CUMULATIVE=2).
func otlpTemporality(t metricdata.Temporality) int {
	if t == metricdata.DeltaTemporality {
		return 1
	}
	return 2
}

func numberPoint[N int64 | float64](attrs []jsonKV, start, ts string, v N) jsonNumberDataPoint {
	p := jsonNumberDataPoint{Attributes: attrs, StartTimeUnixNano: start, TimeUnixNano: ts}
	switch any(v).(type) {
	case int64:
		s := itoa64(int64(v))
		p.AsInt = &s
	default:
		d := float64(v)
		p.AsDouble = &d
	}
	return p
}

func convertSum[N int64 | float64](data metricdata.Sum[N]) *jsonSum {
	out := &jsonSum{
		AggregationTemporality: otlpTemporality(data.Temporality),
		IsMonotonic:            data.IsMonotonic,
		DataPoints:             make([]jsonNumberDataPoint, len(data.DataPoints)),
	}
	for i, dp := range data.DataPoints {
		out.DataPoints[i] = numberPoint(setToKVs(dp.Attributes), unixNano(dp.StartTime), unixNano(dp.Time), dp.Value)
	}
	return out
}

func convertGauge[N int64 | float64](data metricdata.Gauge[N]) *jsonGauge {
	out := &jsonGauge{DataPoints: make([]jsonNumberDataPoint, len(data.DataPoints))}
	for i, dp := range data.DataPoints {
		out.DataPoints[i] = numberPoint(setToKVs(dp.Attributes), unixNano(dp.StartTime), unixNano(dp.Time), dp.Value)
	}
	return out
}

func convertHistogram[N int64 | float64](data metricdata.Histogram[N]) *jsonHistogram {
	out := &jsonHistogram{AggregationTemporality: otlpTemporality(data.Temporality)}
	for _, dp := range data.DataPoints {
		sum := float64(dp.Sum)
		bucketCounts := make([]string, len(dp.BucketCounts))
		for i, c := range dp.BucketCounts {
			bucketCounts[i] = utoa64(c)
		}
		out.DataPoints = append(out.DataPoints, jsonHistogramDataPoint{
			Attributes:        setToKVs(dp.Attributes),
			StartTimeUnixNano: unixNano(dp.StartTime),
			TimeUnixNano:      unixNano(dp.Time),
			Count:             utoa64(dp.Count),
			Sum:               &sum,
			BucketCounts:      bucketCounts,
			ExplicitBounds:    dp.Bounds,
		})
	}
	return out
}

// convertAggregation converts one instrument's aggregated data. An
// aggregation type pkg/mcp never produces (ExponentialHistogram) is skipped
// rather than erroring: a future instrument using it would simply not
// appear in the export instead of breaking the whole batch.
func convertAggregation(m metricdata.Metrics) jsonMetric {
	out := jsonMetric{Name: m.Name, Description: m.Description, Unit: m.Unit}
	switch data := m.Data.(type) {
	case metricdata.Sum[int64]:
		out.Sum = convertSum(data)
	case metricdata.Sum[float64]:
		out.Sum = convertSum(data)
	case metricdata.Gauge[int64]:
		out.Gauge = convertGauge(data)
	case metricdata.Gauge[float64]:
		out.Gauge = convertGauge(data)
	case metricdata.Histogram[int64]:
		out.Histogram = convertHistogram(data)
	case metricdata.Histogram[float64]:
		out.Histogram = convertHistogram(data)
	}
	return out
}

func (e *jsonMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if rm == nil || len(rm.ScopeMetrics) == 0 {
		return nil
	}
	scopeMetrics := make([]jsonScopeMetrics, 0, len(rm.ScopeMetrics))
	for _, sm := range rm.ScopeMetrics {
		metrics := make([]jsonMetric, 0, len(sm.Metrics))
		for _, m := range sm.Metrics {
			metrics = append(metrics, convertAggregation(m))
		}
		scopeMetrics = append(scopeMetrics, jsonScopeMetrics{Scope: scopeToJSON(sm.Scope), Metrics: metrics})
	}

	reqBody := jsonMetricRequest{ResourceMetrics: []jsonResourceMetrics{{
		Resource:     resourceToJSON(rm.Resource),
		ScopeMetrics: scopeMetrics,
	}}}
	body, err := marshalJSON(reqBody)
	if err != nil {
		return err
	}
	return e.client.post(ctx, body)
}

func (e *jsonMetricExporter) ForceFlush(ctx context.Context) error { return nil }
func (e *jsonMetricExporter) Shutdown(ctx context.Context) error   { return nil }
