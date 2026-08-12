// Package mockmcp provides a deterministic, in-process MCP server used
// exclusively by the benchmark suite (tests/bench/token_economy_bench_test.go).
//
// Usage model:
//
//	// as a subprocess (single-shot, stdio):
//	$ go run ./tests/bench/mockmcp --tools=41
//
//	// as a library (in-process via Server type):
//	srv := mockmcp.New(mockmcp.Config{ToolCount: 41})
//	conn, payload := srv.HandleRequest(line)
//
// The mock implements the JSON-RPC 2.0 subset that an MCP client needs:
// initialize, notifications/initialized, tools/list, tools/call, ping.
// It is intentionally minimal — no resources, no prompts, no sampling —
// because the benchmark only exercises the schema-tax and throughput paths.
package mockmcp

import (
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// Config describes the shape of a mock MCP server.
type Config struct {
	// ToolCount is the number of tools to advertise on tools/list.
	ToolCount int
	// ToolNamePrefix is used to generate tool names: <prefix>_0, <prefix>_1, ...
	ToolNamePrefix string
	// DescriptionBase is the base description for every tool. The benchmark
	// uses a description similar in size to the real Garmin / GitHub tools.
	DescriptionBase string
	// ResponseBytes is the bytes of the canned response returned by
	// tools/call. 0 means return a minimal {"ok":true}.
	ResponseBytes int
}

// DefaultConfig returns a Config that mirrors the schema size of a typical
// GitHub-style tool definition.
func DefaultConfig() Config {
	return Config{
		ToolCount:       41,
		ToolNamePrefix:  "tool",
		DescriptionBase: "Tool description for benchmark purposes.",
		ResponseBytes:   256,
	}
}

// Server is the in-process mock MCP server. It is safe for concurrent use.
type Server struct {
	cfg      Config
	toolJSON []byte
	count    atomic.Uint64
}

// New constructs a Server with the given config.
func New(cfg Config) *Server {
	if cfg.ToolCount <= 0 {
		cfg.ToolCount = 1
	}
	if cfg.ToolNamePrefix == "" {
		cfg.ToolNamePrefix = "tool"
	}
	if cfg.DescriptionBase == "" {
		cfg.DescriptionBase = "Tool description."
	}
	s := &Server{cfg: cfg}
	s.toolJSON = s.buildToolsList()
	return s
}

// ToolsListJSON returns the marshaled `tools/list` response. Useful for
// benchmarks that want to compare token counts.
func (s *Server) ToolsListJSON() []byte {
	return s.toolJSON
}

// HandleRequest is the entry point used by the mock's stdio loop. It reads
// one line of JSON-RPC, writes one line of response, and returns the count
// of requests handled so far. Errors are returned as JSON-RPC error objects.
func (s *Server) HandleRequest(line string) (string, error) {
	s.count.Add(1)
	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params,omitempty"`
	}
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return "", fmt.Errorf("parse: %w", err)
	}
	if req.JSONRPC != "2.0" {
		return "", fmt.Errorf("unsupported jsonrpc %q", req.JSONRPC)
	}
	switch req.Method {
	case "initialize":
		return s.respond(req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mockmcp", "version": "0.0.0"},
		})
	case "notifications/initialized":
		return "", nil // no response for notifications
	case "ping":
		return s.respond(req.ID, map[string]any{})
	case "tools/list":
		return s.respondRaw(req.ID, s.toolJSON)
	case "tools/call":
		var params struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(req.Params, &params)
		body := make([]byte, 0, s.cfg.ResponseBytes)
		body = append(body, []byte(`{"result":{"content":[{"type":"text","text":"`)...)
		// pad to ResponseBytes
		if len(body) < s.cfg.ResponseBytes-2 {
			pad := make([]byte, s.cfg.ResponseBytes-2-len(body))
			for i := range pad {
				pad[i] = '.'
			}
			body = append(body, pad...)
		}
		body = append(body, []byte(`"}]}}`)...)
		return s.respond(req.ID, json.RawMessage(body))
	case "get_tool_schema":
		// Some MCP servers expose a dedicated schema-fetch method; the JIT
		// handler in pkg/proxy/jit.go uses this. Return a tiny schema.
		return s.respond(req.ID, map[string]any{
			"name":        "tool",
			"description": s.cfg.DescriptionBase,
			"inputSchema": map[string]any{"type": "object"},
		})
	default:
		// unknown method — return method-not-found
		return s.respondErr(req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) respond(id json.RawMessage, result any) (string, error) {
	return s.respondRaw(id, mustMarshal(result))
}

func (s *Server) respondRaw(id json.RawMessage, result json.RawMessage) (string, error) {
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  json.RawMessage `json:"result"`
	}{JSONRPC: "2.0", ID: id, Result: result}
	b, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (s *Server) respondErr(id json.RawMessage, code int, msg string) (string, error) {
	resp := struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Error   struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{JSONRPC: "2.0", ID: id}
	resp.Error.Code = code
	resp.Error.Message = msg
	b, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Count returns the total number of requests handled so far.
func (s *Server) Count() uint64 { return s.count.Load() }

func (s *Server) buildToolsList() []byte {
	tools := make([]map[string]any, 0, s.cfg.ToolCount)
	for i := 0; i < s.cfg.ToolCount; i++ {
		tools = append(tools, map[string]any{
			"name":        fmt.Sprintf("%s_%d", s.cfg.ToolNamePrefix, i),
			"description": s.cfg.DescriptionBase,
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
				},
			},
		})
	}
	// buildToolsList returns only the inner `result` payload so callers can
	// wrap it in a fresh JSON-RPC envelope without double-nesting.
	return mustMarshal(map[string]any{"tools": tools})
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
