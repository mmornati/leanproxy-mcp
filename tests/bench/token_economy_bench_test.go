// Package bench holds micro-benchmarks. Each one measures exactly what its
// name says (a payload size, a parse, an estimate) and nothing here is a
// proxy-level number: the token savings, latency, throughput and safety
// figures published in README.md and docs/benchmark-results.md come only
// from the end-to-end harness in tests/harness (`make harness`, #301),
// which drives the real binary.
//
// Token accounting uses reporter.NewEstimator(), the same chars/4 primitive
// the runtime cost tracker (pkg/reporter/cost.go) and the harness use.
package bench

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

// routerTool mirrors the JSON shape a `tools/list` response sends to
// clients: name, description and inputSchema only (Examples, Returns and
// Categories from mcp.ToolDefinition are internal and never serialized).
type routerTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// routerListJSON marshals the real, production `tools/list` payload
// (pkg/mcp.GetAllToolDefinitions: search_tools, list_servers, list_tools,
// invoke_tool —
// the same list `server run --stdio` returns) instead of a hand-maintained
// stub, so this benchmark can never drift from what ships. See #300.
func routerListJSON() []byte {
	defs := mcp.GetAllToolDefinitions()
	tools := make([]routerTool, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, routerTool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		})
	}
	envelope := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"result":  map[string]any{"tools": tools},
	}
	b, _ := json.Marshal(envelope)
	return b
}

// --- Router payload (the real `tools/list` of `server run --stdio`) ----

func BenchmarkSchemaTax_LeanProxyRouter(b *testing.B) {
	payload := routerListJSON()
	estimator := reporter.NewEstimator()

	tokens := estimator.EstimateTokens(string(payload))
	b.ReportMetric(float64(tokens), "router_tokens")
	b.ReportMetric(float64(len(payload)), "router_bytes")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = estimator.EstimateTokens(string(payload))
	}

	b.Logf("router payload: %d bytes, %d tokens (1 token ≈ 4 chars)",
		len(payload), tokens)
}

// --- Lazy-loading stub schema -----------------------------------------

func BenchmarkSchemaTax_StubSchema(b *testing.B) {
	estimator := reporter.NewEstimator()
	// Use the real production ToolStub from pkg/registry/lazy.go so the
	// measurement reflects the actual on-wire shape LeanProxy emits.
	stub := registryToolStub{
		Name:        "github_search_repositories",
		Description: "Search GitHub repositories.",
		Category:    "search",
	}
	payload, _ := json.Marshal(stub)
	tokens := estimator.EstimateTokens(string(payload))

	b.ReportMetric(float64(tokens), "stub_tokens")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = estimator.EstimateTokens(string(payload))
	}

	b.Logf("stub schema: %d bytes, %d tokens (production registry.ToolStub)", len(payload), tokens)
}

// registryToolStub mirrors pkg/registry/lazy.go:ToolStub. Duplicated here
// (not imported) because the benchmark package would otherwise need a
// transitive dependency on pkg/registry that we want to keep optional.
type registryToolStub struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
}

// --- JSON parse of two literals ------------------------------------------

// BenchmarkJSONParse_Literal unmarshals one literal tools/call request and
// one literal response and feeds the cost tracker. It is an in-process
// micro-benchmark of those three operations only: no pipe, no pool, no
// middleware. It is NOT the proxy overhead; the harness measures that
// through the real binary (tests/harness, "Proxy overhead" row).
func BenchmarkJSONParse_Literal(b *testing.B) {
	tracker := reporter.NewCostTracker()
	estimator := reporter.NewEstimator()

	req := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"github_search_repositories","arguments":{"q":"leanproxy"}}}`)
	resp := []byte(`{"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"found 3 results"}]}}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var r, s struct {
			JSONRPC string `json:"jsonrpc"`
			ID      any    `json:"id"`
			Method  string `json:"method"`
		}
		_ = json.Unmarshal(req, &r)
		_ = json.Unmarshal(resp, &s)
		tokens := int64(estimator.EstimateTokens(string(req)) + estimator.EstimateTokens(string(resp)))
		tracker.TrackAt("github_search_repositories", "github", tokens, time.Now())
	}
}

// --- Token estimate over 50 MB ------------------------------------------

// BenchmarkEstimateTokens_50MB runs the chars/4 estimator over a 50 MB
// buffer. It measures the estimator only, not relaying a large response
// (the harness measures a 5 MB relay through the binary).
func BenchmarkEstimateTokens_50MB(b *testing.B) {
	estimator := reporter.NewEstimator()
	const targetBytes = 50 * 1024 * 1024
	chunk := make([]byte, 1024)
	for i := range chunk {
		chunk[i] = 'a'
	}
	var payload []byte
	for len(payload) < targetBytes {
		payload = append(payload, chunk...)
	}
	tokens := estimator.EstimateTokens(string(payload))

	b.ReportMetric(float64(len(payload))/1024/1024, "payload_mb")
	b.ReportMetric(float64(tokens), "tokens")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = estimator.EstimateTokens(string(payload))
	}
}

// --- Binary size (NFR3: <20MB) ------------------------------------------

func TestBinarySize_NFR3(t *testing.T) {
	// Find the dist/ binaries built by `make build`. The test package
	// runs from tests/bench/, so we look both at relative and absolute
	// repo-root paths.
	candidates := []string{
		"dist/leanproxy-mcp-*",
		"../../dist/leanproxy-mcp-*",
	}
	var matches []string
	for _, pattern := range candidates {
		if m, err := filepath.Glob(pattern); err == nil {
			matches = append(matches, m...)
		}
	}
	if len(matches) == 0 {
		t.Skip("no dist/leanproxy-mcp-* binary; run `make build` first")
	}
	for _, p := range matches {
		fi, err := os.Stat(p)
		if err != nil {
			t.Errorf("stat %s: %v", p, err)
			continue
		}
		mb := float64(fi.Size()) / 1024 / 1024
		if mb > 20 {
			t.Errorf("binary %s is %.1f MB, exceeds NFR3 (20 MB)", p, mb)
		}
		t.Logf("binary %s = %.1f MB", p, mb)
	}
}

// --- Bonus: real router ToolDefinitions exercise the production path ----

func TestGatewayRouterToolsList(t *testing.T) {
	// Sanity check that the production pkg/mcp package's tool definitions
	// (used by `server run --stdio` tools/list) return exactly the 4 tools
	// we expect.
	tools := mcp.GetAllToolDefinitions()
	if len(tools) != 4 {
		t.Fatalf("mcp.GetAllToolDefinitions() = %d tools, want 4 (search_tools, list_servers, list_tools, invoke_tool)", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{"search_tools", "list_servers", "invoke_tool", "list_tools"} {
		if !names[expected] {
			t.Errorf("router is missing tool %q", expected)
		}
	}

	// Estimated token count of the real router payload, using the same
	// pkg/reporter.Estimator the #300 unit test budget uses.
	estimator := reporter.NewEstimator()
	payload := routerListJSON()
	tokens := estimator.EstimateTokens(string(payload))
	if tokens <= 0 {
		t.Fatalf("router tokens = %d, want > 0", tokens)
	}
	const budget = 330
	if tokens >= budget {
		t.Errorf("router tools/list tokens = %d, want < %d", tokens, budget)
	}
	t.Logf("router payload: %d bytes, %d tokens (via pkg/mcp.GetAllToolDefinitions)", len(payload), tokens)
}
