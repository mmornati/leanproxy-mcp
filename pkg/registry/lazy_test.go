package registry

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNewLazySchemaCache(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)
	if cache == nil {
		t.Fatal("expected non-nil cache")
	}
	if cache.cache == nil {
		t.Error("expected cache map to be initialized")
	}
	if cache.lastAccess == nil {
		t.Error("expected lastAccess map to be initialized")
	}
}

func TestLazySchemaCache_SetAndGetFullSchema(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.SetFullSchema("test_tool", schema)

	result, found := cache.GetFullSchema("test_tool")
	if !found {
		t.Fatal("expected to find schema")
	}
	if result.Name != schema.Name {
		t.Errorf("expected name %s, got %s", schema.Name, result.Name)
	}
	if result.Description != schema.Description {
		t.Errorf("expected description %s, got %s", schema.Description, result.Description)
	}
}

func TestLazySchemaCache_GetFullSchema_NotFound(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	_, found := cache.GetFullSchema("nonexistent")
	if found {
		t.Error("expected not to find schema")
	}
}

func TestLazySchemaCache_GetFullSchema_Expiry(t *testing.T) {
	cache := NewLazySchemaCache(1 * time.Millisecond)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.SetFullSchema("test_tool", schema)

	time.Sleep(10 * time.Millisecond)

	_, found := cache.GetFullSchema("test_tool")
	if found {
		t.Error("expected schema to be expired")
	}
}

func TestLazySchemaCache_GetStub(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool description",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.SetFullSchema("test_tool", schema)

	stub, found := cache.GetStub("test_tool")
	if !found {
		t.Fatal("expected to find stub")
	}
	if stub.Name != "test_tool" {
		t.Errorf("expected name %s, got %s", "test_tool", stub.Name)
	}
	if stub.Description != "A test tool description" {
		t.Errorf("expected description %s, got %s", "A test tool description", stub.Description)
	}
}

func TestLazySchemaCache_GetStub_NotFound(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	_, found := cache.GetStub("nonexistent")
	if found {
		t.Error("expected not to find stub")
	}
}

func TestLazySchemaCache_Invalidate(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.SetFullSchema("test_tool", schema)
	cache.Invalidate("test_tool")

	_, found := cache.GetFullSchema("test_tool")
	if found {
		t.Error("expected schema to be invalidated")
	}
}

func TestLazySchemaCache_Clear(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.SetFullSchema("test_tool", schema)
	cache.Clear()

	_, found := cache.GetFullSchema("test_tool")
	if found {
		t.Error("expected cache to be cleared")
	}
}

func TestLazySchemaCache_CacheWithTTL(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.CacheWithTTL("test_tool", schema, 1*time.Hour)

	result, found := cache.GetFullSchema("test_tool")
	if !found {
		t.Fatal("expected to find schema")
	}
	if result.Name != "test_tool" {
		t.Errorf("expected name test_tool, got %s", result.Name)
	}
}

func TestLazySchemaCache_Stats(t *testing.T) {
	cache := NewLazySchemaCache(24 * time.Hour)

	schema := ToolSchema{
		Name:        "test_tool",
		Description: "A test tool",
		InputSchema: json.RawMessage(`{"type": "object"}`),
		ServerID:    "test_server",
	}

	cache.SetFullSchema("tool1", schema)
	cache.SetFullSchema("tool2", schema)

	cached, expired := cache.Stats()
	if cached != 2 {
		t.Errorf("expected 2 cached, got %d", cached)
	}
	if expired != 0 {
		t.Errorf("expected 0 expired, got %d", expired)
	}
}
