package mcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp/governor"
)

// BenchmarkGovernor_* measure the governor stage alone on a normal-size
// result (the fast path: nothing is parsed) and on large text and JSON
// results (issue #319: the governor must stay cheap), and field projection
// (#320) on a wide JSON listing.
func benchGovernor(b *testing.B, result json.RawMessage) {
	benchGovernorWith(b, &governor.Config{Enabled: true}, result)
}

func benchGovernorWith(b *testing.B, cfg *governor.Config, result json.RawMessage) {
	g := NewGovernor(cfg)
	defer g.Close()
	mw := g.Middleware()(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{JSONRPC: JSONRPCVersion, ID: req.ID, Result: result}, nil
	})
	req := &Request{JSONRPC: JSONRPCVersion, Method: MethodToolsCall, ID: 1,
		Params: json.RawMessage(`{"name":"invoke_tool","arguments":{"server":"fs","tool":"read"}}`)}
	b.SetBytes(int64(len(result)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := mw(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func benchTextResult(s string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": s}}})
	return b
}

func BenchmarkGovernor_SmallResult(b *testing.B) {
	benchGovernor(b, benchTextResult(`{"server":"slack","called":"slack_post_message","args":{"text":"msg 1"}}`))
}

func BenchmarkGovernor_LargeText(b *testing.B) {
	benchGovernor(b, benchTextResult(govBigText()))
}

func BenchmarkGovernor_LargeJSON(b *testing.B) {
	benchGovernor(b, benchTextResult(govJSONArray(3000)))
}

func BenchmarkGovernor_ProjectionOnly(b *testing.B) {
	cfg := &governor.Config{Enabled: true, MaxTokens: intPtr(0),
		Projections: []governor.ProjectionRule{{Match: "fs.*", Drop: githubDrop}}}
	benchGovernorWith(b, cfg, benchTextResult(wideJSON(30)))
}

func BenchmarkGovernor_ProjectionThenTruncation(b *testing.B) {
	cfg := &governor.Config{Enabled: true,
		Projections: []governor.ProjectionRule{{Match: "fs.*", Drop: githubDrop}}}
	benchGovernorWith(b, cfg, benchTextResult(wideJSON(600)))
}
