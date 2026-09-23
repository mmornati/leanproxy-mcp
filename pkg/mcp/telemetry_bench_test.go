package mcp

import (
	"context"
	"encoding/json"
	"testing"
)

// BenchmarkPipeline_TelemetryDisabled and BenchmarkPipeline_NoTelemetry
// measure the pipeline's per-request cost with and without
// TelemetryMiddleware installed, both with telemetry disabled (the default:
// no otel.Init call, global providers are the SDK no-ops). Issue #317
// requires the overhead to stay under 5%; run with:
//
//	go test ./pkg/mcp/... -bench BenchmarkPipeline -benchmem -run '^$'
func BenchmarkPipeline_NoTelemetry(b *testing.B) {
	p := newLifecyclePool("bench")
	p.setTools("bench", "do_thing")
	h := NewHandler(p, nil)

	req, resetID := benchInvokeRequest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.ID = resetID(i)
		if _, err := h.HandleRequest(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPipeline_TelemetryDisabled(b *testing.B) {
	p := newLifecyclePool("bench")
	p.setTools("bench", "do_thing")
	h := NewHandler(p, nil)
	h.Use(TelemetryMiddleware())

	req, resetID := benchInvokeRequest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.ID = resetID(i)
		if _, err := h.HandleRequest(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func benchInvokeRequest() (*Request, func(int) interface{}) {
	args, _ := json.Marshal(map[string]interface{}{"server": "bench", "tool": "do_thing"})
	params, _ := json.Marshal(ToolsCallParams{Name: "invoke_tool", Arguments: args})
	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, Params: params}
	return req, func(i int) interface{} { return float64(i) }
}
