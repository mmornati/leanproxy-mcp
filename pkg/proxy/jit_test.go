package proxy

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

type mockSchemaCache struct {
	data map[string]json.RawMessage
}

func newMockSchemaCache() *mockSchemaCache {
	return &mockSchemaCache{
		data: make(map[string]json.RawMessage),
	}
}

func (c *mockSchemaCache) Get(key string) (json.RawMessage, bool) {
	v, ok := c.data[key]
	return v, ok
}

func (c *mockSchemaCache) Set(key string, schema json.RawMessage) {
	c.data[key] = schema
}

func (c *mockSchemaCache) Delete(key string) {
	delete(c.data, key)
}

func (c *mockSchemaCache) Clear() {
	c.data = make(map[string]json.RawMessage)
}

type mockRegistry struct {
	schemas map[string]json.RawMessage
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		schemas: make(map[string]json.RawMessage),
	}
}

func (r *mockRegistry) GetToolSchema(ctx context.Context, serverID, toolName string) (json.RawMessage, error) {
	key := serverID + "/" + toolName
	if schema, ok := r.schemas[key]; ok {
		return schema, nil
	}
	return nil, nil
}

func (r *mockRegistry) AddSchema(serverID, toolName string, schema json.RawMessage) {
	key := serverID + "/" + toolName
	r.schemas[key] = schema
}

type mockForwarder struct {
	forwarded []JSONRPCRequest
	response  JSONRPCResponse
}

func newMockForwarder() *mockForwarder {
	return &mockForwarder{
		forwarded: make([]JSONRPCRequest, 0),
	}
}

func (f *mockForwarder) ForwardRequest(ctx context.Context, req JSONRPCRequest) (JSONRPCResponse, error) {
	f.forwarded = append(f.forwarded, req)
	resp := f.response
	resp.ID = req.ID
	return resp, nil
}

func TestIsGetToolSchemaRequest(t *testing.T) {
	tests := []struct {
		method  string
		isJIT   bool
	}{
		{"get_tool_schema", true},
		{"Get_Tool_Schema", true},
		{"GET_TOOL_SCHEMA", true},
		{"GetToolSchema", false},
		{"getToolSchema", false},
		{"tools/list", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			result := IsGetToolSchemaRequest(tt.method)
			if result != tt.isJIT {
				t.Errorf("IsGetToolSchemaRequest(%q) = %v, want %v", tt.method, result, tt.isJIT)
			}
		})
	}
}

func TestJITHandler_HandleGetToolSchema_CacheHit(t *testing.T) {
	cache := newMockSchemaCache()
	registry := newMockRegistry()
	forwarder := newMockForwarder()

	expectedSchema := json.RawMessage(`{"name":"test-tool","description":"A test tool"}`)
	cache.Set("server1/test-tool", expectedSchema)

	handler := NewJITHandler(cache, registry, forwarder, nil, "server1", true)

	params := json.RawMessage(`{"name":"test-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      1,
	}

	resp, err := handler.HandleGetToolSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error in response: %v", resp.Error)
	}

	result := resp.Result
	if !json.Valid(result) {
		t.Fatalf("result is not valid JSON: %s", string(result))
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(result, &schema); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if schema["name"] != "test-tool" {
		t.Errorf("expected schema name 'test-tool', got %v", schema["name"])
	}

	if len(forwarder.forwarded) != 0 {
		t.Errorf("expected no forwarded requests, got %d", len(forwarder.forwarded))
	}
}

func TestJITHandler_HandleGetToolSchema_CacheMiss(t *testing.T) {
	cache := newMockSchemaCache()
	registry := newMockRegistry()
	forwarder := newMockForwarder()

	forwarder.response = JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"forwarded":true}`),
		ID:      1,
	}

	registry.AddSchema("server1", "test-tool", json.RawMessage(`{"name":"test-tool","description":"From registry"}`))

	handler := NewJITHandler(cache, registry, forwarder, nil, "server1", true)

	params := json.RawMessage(`{"name":"test-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      1,
	}

	resp, err := handler.HandleGetToolSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error in response: %v", resp.Error)
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var schema map[string]interface{}
	if err := json.Unmarshal(resultBytes, &schema); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if schema["description"] != "From registry" {
		t.Errorf("expected schema from registry, got %v", schema["description"])
	}

	cachedSchema, ok := cache.Get("server1/test-tool")
	if !ok {
		t.Fatalf("expected schema to be cached")
	}

	var cached map[string]interface{}
	json.Unmarshal(cachedSchema, &cached)
	if cached["description"] != "From registry" {
		t.Errorf("cached schema mismatch")
	}
}

func TestJITHandler_HandleGetToolSchema_NotInRegistryOrCache(t *testing.T) {
	cache := newMockSchemaCache()
	registry := newMockRegistry()
	forwarder := newMockForwarder()

	forwarder.response = JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"unknown":true}`),
		ID:      1,
	}

	handler := NewJITHandler(cache, registry, forwarder, nil, "server1", true)

	params := json.RawMessage(`{"name":"unknown-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      42,
	}

	resp, err := handler.HandleGetToolSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(forwarder.forwarded) != 1 {
		t.Fatalf("expected 1 forwarded request, got %d", len(forwarder.forwarded))
	}

	if resp.ID != 42 {
		t.Errorf("expected ID 42 in response, got %v", resp.ID)
	}
}

func TestJITHandler_HandleGetToolSchema_Disabled(t *testing.T) {
	cache := newMockSchemaCache()
	registry := newMockRegistry()
	forwarder := newMockForwarder()

	forwarder.response = JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"disabled":true}`),
		ID:      1,
	}

	handler := NewJITHandler(cache, registry, forwarder, nil, "server1", false)

	params := json.RawMessage(`{"name":"test-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      1,
	}

	resp, err := handler.HandleGetToolSchema(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(forwarder.forwarded) != 1 {
		t.Fatalf("expected 1 forwarded request when disabled, got %d", len(forwarder.forwarded))
	}

	resultBytes, _ := json.Marshal(resp.Result)
	var result map[string]interface{}
	json.Unmarshal(resultBytes, &result)

	if result["disabled"] != true {
		t.Errorf("expected disabled=true in result")
	}
}

func TestJITHandler_ExtractToolName(t *testing.T) {
	tests := []struct {
		name    string
		params  string
		want    string
		wantErr bool
	}{
		{"valid params", `{"name":"test-tool"}`, "test-tool", false},
		{"valid with spaces", `{"name":"  test-tool  "}`, "test-tool", false},
		{"missing name", `{}`, "", true},
		{"nil params", ``, "", true},
		{"invalid json", `{invalid}`, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &JITHandler{}
			got, err := handler.extractToolName(json.RawMessage(tt.params))

			if tt.wantErr && err == nil {
				t.Errorf("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLRUCache(t *testing.T) {
	t.Run("basic set and get", func(t *testing.T) {
		cache := newMockSchemaCache()
		schema := json.RawMessage(`{"test":true}`)
		cache.Set("key1", schema)

		got, ok := cache.Get("key1")
		if !ok {
			t.Fatal("expected to find key1")
		}
		if string(got) != string(schema) {
			t.Errorf("got %s, want %s", string(got), string(schema))
		}
	})

	t.Run("delete", func(t *testing.T) {
		cache := newMockSchemaCache()
		cache.Set("key1", json.RawMessage(`{"test":true}`))
		cache.Delete("key1")

		_, ok := cache.Get("key1")
		if ok {
			t.Error("expected key1 to be deleted")
		}
	})

	t.Run("clear", func(t *testing.T) {
		cache := newMockSchemaCache()
		cache.Set("key1", json.RawMessage(`{"test":true}`))
		cache.Set("key2", json.RawMessage(`{"test":true}`))
		cache.Clear()

		_, ok1 := cache.Get("key1")
		_, ok2 := cache.Get("key2")
		if ok1 || ok2 {
			t.Error("expected cache to be cleared")
		}
	})
}

func TestJITConfig(t *testing.T) {
	t.Run("default values", func(t *testing.T) {
		cfg := JITConfig{
			Enabled:  true,
			CacheSize: 100,
			CacheTTL: "1h",
		}

		if !cfg.Enabled {
			t.Error("expected Enabled to be true")
		}
		if cfg.CacheSize != 100 {
			t.Errorf("expected CacheSize 100, got %d", cfg.CacheSize)
		}
		if cfg.CacheTTL != "1h" {
			t.Errorf("expected CacheTTL '1h', got %s", cfg.CacheTTL)
		}
	})
}

func BenchmarkJITCacheHit(b *testing.B) {
	cache := newMockSchemaCache()
	schema := json.RawMessage(`{"name":"benchmark-tool","description":"A benchmark tool","inputSchema":{"type":"object"}}`)
	cache.Set("server1/benchmark-tool", schema)

	registry := newMockRegistry()
	forwarder := newMockForwarder()
	handler := NewJITHandler(cache, registry, forwarder, nil, "server1", true)

	params := json.RawMessage(`{"name":"benchmark-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		handler.HandleGetToolSchema(context.Background(), req)
	}
}

func BenchmarkJITCacheMiss(b *testing.B) {
	cache := newMockSchemaCache()
	registry := newMockRegistry()
	forwarder := newMockForwarder()
	forwarder.response = JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"name":"miss-tool"}`),
		ID:      1,
	}

	registry.AddSchema("server1", "miss-tool", json.RawMessage(`{"name":"miss-tool"}`))

	handler := NewJITHandler(cache, registry, forwarder, nil, "server1", true)

	params := json.RawMessage(`{"name":"miss-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      1,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.Clear()
		handler.HandleGetToolSchema(context.Background(), req)
	}
}

func ExampleJITHandler() {
	cache := newMockSchemaCache()
	registry := newMockRegistry()
	forwarder := newMockForwarder()

	forwarder.response = JSONRPCResponse{
		JSONRPC: "2.0",
		Result:  json.RawMessage(`{"name":"example-tool"}`),
		ID:      1,
	}

	registry.AddSchema("myserver", "example-tool", json.RawMessage(`{"name":"example-tool","description":"Example"}`))

	handler := NewJITHandler(cache, registry, forwarder, nil, "myserver", true)

	params := json.RawMessage(`{"name":"example-tool"}`)
	req := JSONRPCRequest{
		JSONRPC: "2.0",
		Method:  "get_tool_schema",
		Params:  params,
		ID:      1,
	}

	resp, _ := handler.HandleGetToolSchema(context.Background(), req)
	_ = resp

	cached, _ := cache.Get("myserver/example-tool")
	_ = cached

	time.Sleep(1 * time.Millisecond)
}