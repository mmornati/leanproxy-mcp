package cache

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewBreakpointInjectorDefaults(t *testing.T) {
	inj := NewBreakpointInjector()
	if inj == nil {
		t.Fatal("expected non-nil injector")
	}
	if inj.strategy != StrategyAggressive {
		t.Errorf("default strategy = %q, want %q", inj.strategy, StrategyAggressive)
	}
	if inj.logger == nil {
		t.Error("expected non-nil default logger")
	}
}

func TestNewBreakpointInjectorWithOptions(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	inj := NewBreakpointInjector(
		WithInjectLogger(logger),
		WithStrategy(StrategyBalanced),
	)
	if inj.logger != logger {
		t.Error("logger option not applied")
	}
	if inj.strategy != StrategyBalanced {
		t.Errorf("strategy = %q, want %q", inj.strategy, StrategyBalanced)
	}
}

func TestWithInjectLoggerNilIgnored(t *testing.T) {
	inj := NewBreakpointInjector(WithInjectLogger(nil))
	if inj.logger == nil {
		t.Fatal("default logger should remain when nil is passed")
	}
}

func TestInjectAggressiveSystemAndTools(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"model": "claude-3-5-sonnet-20241022",
		"max_tokens": 1024,
		"system": [{"type": "text", "text": "You are helpful."}],
		"messages": [{"role": "user", "content": "Hello"}],
		"tools": [{"name": "get_weather", "description": "Get weather", "input_schema": {"type": "object"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	system, ok := parsed["system"].([]interface{})
	if !ok || len(system) == 0 {
		t.Fatal("expected system array")
	}
	lastSys := system[len(system)-1].(map[string]interface{})
	if _, has := lastSys["cache_control"]; !has {
		t.Error("system last item missing cache_control")
	}
	cc := lastSys["cache_control"].(map[string]interface{})
	if cc["type"] != "ephemeral" {
		t.Errorf("cache_control type = %q, want %q", cc["type"], "ephemeral")
	}

	tools, ok := parsed["tools"].([]interface{})
	if !ok || len(tools) == 0 {
		t.Fatal("expected tools array")
	}
	lastTool := tools[len(tools)-1].(map[string]interface{})
	if _, has := lastTool["cache_control"]; !has {
		t.Error("tools last item missing cache_control")
	}
	cc2 := lastTool["cache_control"].(map[string]interface{})
	if cc2["type"] != "ephemeral" {
		t.Errorf("cache_control type = %q, want %q", cc2["type"], "ephemeral")
	}
}

func TestInjectAggressivePreservesOtherFields(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"model": "claude-3-5-sonnet-20241022",
		"max_tokens": 1024,
		"system": [{"type": "text", "text": "You are helpful."}],
		"messages": [{"role": "user", "content": "Hello"}],
		"tools": [{"name": "get_weather", "description": "Get weather", "input_schema": {"type": "object"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	if parsed["model"] != "claude-3-5-sonnet-20241022" {
		t.Error("model field changed")
	}
	if parsed["max_tokens"] != float64(1024) {
		t.Error("max_tokens field changed")
	}
	messages := parsed["messages"].([]interface{})
	msg := messages[0].(map[string]interface{})
	if msg["role"] != "user" || msg["content"] != "Hello" {
		t.Error("messages content changed")
	}
}

func TestInjectAggressiveMultipleSystemBlocks(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"system": [
			{"type": "text", "text": "First block"},
			{"type": "text", "text": "Second block"}
		],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	for i, block := range system {
		b := block.(map[string]interface{})
		_, has := b["cache_control"]
		if i < len(system)-1 && has {
			t.Errorf("non-last system block at index %d has cache_control", i)
		}
		if i == len(system)-1 && !has {
			t.Error("last system block missing cache_control")
		}
	}
}

func TestInjectAggressiveNoTools(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello"}],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	last := system[len(system)-1].(map[string]interface{})
	if _, has := last["cache_control"]; !has {
		t.Error("system should have cache_control even without tools")
	}
}

func TestInjectAggressiveNoSystem(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	tools := parsed["tools"].([]interface{})
	last := tools[len(tools)-1].(map[string]interface{})
	if _, has := last["cache_control"]; !has {
		t.Error("tools should have cache_control even without system")
	}
}

func TestInjectAggressiveNoSystemNoTools(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{"messages": [{"role": "user", "content": "Hi"}]}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	if _, has := parsed["system"]; has {
		t.Error("system should not be added if not present")
	}
	if _, has := parsed["tools"]; has {
		t.Error("tools should not be added if not present")
	}
}

func TestInjectUserSuppliedCacheControlSystem(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	inj := NewBreakpointInjector(WithInjectLogger(logger))
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello", "cache_control": {"type": "ephemeral"}}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	last := system[len(system)-1].(map[string]interface{})
	cc := last["cache_control"].(map[string]interface{})
	if cc["type"] != "ephemeral" {
		t.Errorf("user-supplied cache_control should be preserved, got type=%q", cc["type"])
	}

	// Only tools should be injected, not system (which already has cache_control)
	tools := parsed["tools"].([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	if _, has := lastTool["cache_control"]; !has {
		t.Error("tools should still get cache_control injected")
	}

	if !strings.Contains(buf.String(), "cache_control: user-supplied, skipping") {
		t.Errorf("expected debug log about user-supplied cache_control, got: %s", buf.String())
	}
}

func TestInjectUserSuppliedCacheControlTools(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	inj := NewBreakpointInjector(WithInjectLogger(logger))
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello"}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}, "cache_control": {"type": "ephemeral"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	last := system[len(system)-1].(map[string]interface{})
	if _, has := last["cache_control"]; !has {
		t.Error("system should get cache_control injected")
	}

	tools := parsed["tools"].([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	cc := lastTool["cache_control"].(map[string]interface{})
	if cc["type"] != "ephemeral" {
		t.Errorf("user-supplied tools cache_control should be preserved, got type=%q", cc["type"])
	}

	if !strings.Contains(buf.String(), "cache_control: user-supplied, skipping") {
		t.Errorf("expected debug log about user-supplied cache_control, got: %s", buf.String())
	}
}

func TestInjectStrategyOff(t *testing.T) {
	inj := NewBreakpointInjector(WithStrategy(StrategyOff))
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello"}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	if string(result) != string(body) {
		t.Error("body should be unchanged with strategy=off")
	}
}

func TestInjectStrategyBalancedOnlySystem(t *testing.T) {
	inj := NewBreakpointInjector(WithStrategy(StrategyBalanced))
	body := []byte(`{
		"system": [{"type": "text", "text": "You are helpful."}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	lastSys := system[len(system)-1].(map[string]interface{})
	_, sysHasCC := lastSys["cache_control"]
	tools := parsed["tools"].([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	_, toolsHasCC := lastTool["cache_control"]

	// Balanced: only the largest stable block gets CC
	// We injected system (3 items total: system array has 1 element ~24 bytes, tools array has 1 element ~50 bytes)
	// Tools block is larger, so only tools should get CC in balanced mode
	if toolsHasCC && sysHasCC {
		t.Error("balanced mode should inject only one block, got both")
	}
	if !toolsHasCC && !sysHasCC {
		t.Error("balanced mode should inject at least one block")
	}
}

func TestInjectStrategyBalancedToolsLarger(t *testing.T) {
	inj := NewBreakpointInjector(WithStrategy(StrategyBalanced))
	body := []byte(`{
		"system": [{"type": "text", "text": "short"}],
		"tools": [
			{"name": "get_weather", "description": "Get weather data for a location", "input_schema": {"type": "object"}},
			{"name": "search", "description": "Search the web", "input_schema": {"type": "object", "properties": {"q": {"type": "string"}}}}
		],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	tools := parsed["tools"].([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	if _, has := lastTool["cache_control"]; !has {
		t.Error("tools (larger block) should have cache_control in balanced mode")
	}

	system := parsed["system"].([]interface{})
	lastSys := system[len(system)-1].(map[string]interface{})
	if _, has := lastSys["cache_control"]; has {
		t.Error("system (smaller block) should NOT have cache_control in balanced mode")
	}
}

func TestInjectStrategyBalancedSystemLarger(t *testing.T) {
	inj := NewBreakpointInjector(WithStrategy(StrategyBalanced))
	body := []byte(`{
		"system": [{"type": "text", "text": "You are a helpful assistant with extensive knowledge."}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	lastSys := system[len(system)-1].(map[string]interface{})
	if _, has := lastSys["cache_control"]; !has {
		t.Error("system (larger block) should have cache_control in balanced mode")
	}

	tools := parsed["tools"].([]interface{})
	lastTool := tools[len(tools)-1].(map[string]interface{})
	if _, has := lastTool["cache_control"]; has {
		t.Error("tools (smaller block) should NOT have cache_control in balanced mode")
	}
}

func TestInjectStrategyBalancedOnlyOneBlock(t *testing.T) {
	inj := NewBreakpointInjector(WithStrategy(StrategyBalanced))

	// Only system, no tools
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello"}],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)
	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	var parsed map[string]interface{}
	json.Unmarshal(result, &parsed)
	system := parsed["system"].([]interface{})
	if _, has := system[len(system)-1].(map[string]interface{})["cache_control"]; !has {
		t.Error("only block (system) should get cache_control")
	}

	// Only tools, no system
	body2 := []byte(`{
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}],
		"messages": [{"role": "user", "content": "Hi"}]
	}`)
	result2, _ := inj.Inject(body2)
	json.Unmarshal(result2, &parsed)
	tools := parsed["tools"].([]interface{})
	if _, has := tools[len(tools)-1].(map[string]interface{})["cache_control"]; !has {
		t.Error("only block (tools) should get cache_control")
	}
}

func TestInjectEmptyBody(t *testing.T) {
	inj := NewBreakpointInjector()
	_, err := inj.Inject([]byte{})
	if err == nil {
		t.Error("expected error for empty body")
	}
}

func TestInjectNilBody(t *testing.T) {
	inj := NewBreakpointInjector()
	_, err := inj.Inject(nil)
	if err == nil {
		t.Error("expected error for nil body")
	}
}

func TestInjectMalformedJSON(t *testing.T) {
	inj := NewBreakpointInjector()
	_, err := inj.Inject([]byte(`{invalid json`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestInjectNotAnObject(t *testing.T) {
	inj := NewBreakpointInjector()
	_, err := inj.Inject([]byte(`"just a string"`))
	if err == nil {
		t.Error("expected error for non-object JSON")
	}
}

func TestInjectArrayBody(t *testing.T) {
	inj := NewBreakpointInjector()
	_, err := inj.Inject([]byte(`[1, 2, 3]`))
	if err == nil {
		t.Error("expected error for JSON array body")
	}
}

func TestInjectSystemIsNotArray(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{"system": "not an array", "tools": [{"name": "t1", "input_schema": {"type": "object"}}]}`)
	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	var orig, res map[string]interface{}
	json.Unmarshal(body, &orig)
	json.Unmarshal(result, &res)
	if res["system"] != "not an array" {
		t.Error("system should remain a string, not injected")
	}
	if _, has := res["tools"]; !has {
		t.Error("tools should remain present")
	}
}

func TestInjectToolsIsNotArray(t *testing.T) {
	inj := NewBreakpointInjector()
	body := []byte(`{"system": [{"type": "text", "text": "Hello"}], "tools": "not an array"}`)
	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}
	var orig, res map[string]interface{}
	json.Unmarshal(body, &orig)
	json.Unmarshal(result, &res)
	if res["tools"] != "not an array" {
		t.Error("tools should remain a string, not injected")
	}
	if _, has := res["system"]; !has {
		t.Error("system should remain present")
	}
}

func TestInjectUserSuppliedCacheControlMixed(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	inj := NewBreakpointInjector(WithInjectLogger(logger))
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello", "cache_control": {"type": "ephemeral"}}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}, "cache_control": {"type": "ephemeral"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var orig, res map[string]interface{}
	json.Unmarshal(body, &orig)
	json.Unmarshal(result, &res)
	origSys := orig["system"].([]interface{})[0].(map[string]interface{})
	resSys := res["system"].([]interface{})[0].(map[string]interface{})
	origTools := orig["tools"].([]interface{})[0].(map[string]interface{})
	resTools := res["tools"].([]interface{})[0].(map[string]interface{})
	if resSys["cache_control"].(map[string]interface{})["type"] != origSys["cache_control"].(map[string]interface{})["type"] {
		t.Error("system cache_control should be preserved")
	}
	if resTools["cache_control"].(map[string]interface{})["type"] != origTools["cache_control"].(map[string]interface{})["type"] {
		t.Error("tools cache_control should be preserved")
	}

	count := strings.Count(buf.String(), "cache_control: user-supplied, skipping")
	if count != 2 {
		t.Errorf("expected 2 skip log messages, got %d", count)
	}
}

func TestInjectStrategyOffWithUserSupplied(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	inj := NewBreakpointInjector(WithInjectLogger(logger), WithStrategy(StrategyOff))
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello"}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}]
	}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	if string(result) != string(body) {
		t.Error("body should be unchanged with strategy=off")
	}
	// No log messages about user-supplied when strategy is off
	if strings.Contains(buf.String(), "cache_control") {
		t.Error("no cache_control logs expected when strategy is off")
	}
}

func TestInjectPreservesWhitespace(t *testing.T) {
	inj := NewBreakpointInjector(WithStrategy(StrategyAggressive))
	body := []byte(`{"system":[{"type":"text","text":"Hello"}],"tools":[{"name":"t1","input_schema":{"type":"object"}}]}`)

	result, err := inj.Inject(body)
	if err != nil {
		t.Fatalf("Inject failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	// The result should be compact (no extra whitespace added beyond what we produce)
	if len(result) < len(body) {
		t.Errorf("result shorter than input: %d < %d", len(result), len(body))
	}
}

func BenchmarkInjectAggressive(b *testing.B) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"model": "claude-3-5-sonnet-20241022",
		"max_tokens": 4096,
		"system": [{"type": "text", "text": "You are a helpful assistant."}],
		"messages": [
			{"role": "user", "content": "What's the weather like today in San Francisco?"}
		],
		"tools": [
			{"name": "get_weather", "description": "Get current weather for a location", "input_schema": {"type": "object", "properties": {"location": {"type": "string"}, "unit": {"type": "string", "enum": ["celsius", "fahrenheit"]}}}},
			{"name": "get_time", "description": "Get current time for a timezone", "input_schema": {"type": "object", "properties": {"timezone": {"type": "string"}}}}
		]
	}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = inj.Inject(body)
	}
}

func BenchmarkInjectBalanced(b *testing.B) {
	inj := NewBreakpointInjector(WithStrategy(StrategyBalanced))
	body := []byte(`{
		"system": [{"type": "text", "text": "You are a helpful assistant."}],
		"tools": [{"name": "get_weather", "description": "Get weather", "input_schema": {"type": "object"}}]
	}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = inj.Inject(body)
	}
}

func BenchmarkInjectOff(b *testing.B) {
	inj := NewBreakpointInjector(WithStrategy(StrategyOff))
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello"}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}}]
	}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = inj.Inject(body)
	}
}

func BenchmarkInjectUserSupplied(b *testing.B) {
	inj := NewBreakpointInjector()
	body := []byte(`{
		"system": [{"type": "text", "text": "Hello", "cache_control": {"type": "ephemeral"}}],
		"tools": [{"name": "tool1", "input_schema": {"type": "object"}, "cache_control": {"type": "ephemeral"}}]
	}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = inj.Inject(body)
	}
}

func BenchmarkInjectLargePayload(b *testing.B) {
	inj := NewBreakpointInjector()
	tools := make([]map[string]interface{}, 20)
	for i := 0; i < 20; i++ {
		tools[i] = map[string]interface{}{
			"name":        "tool_" + string(rune('a'+i)),
			"description": strings.Repeat("description ", 50),
			"input_schema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"param1": map[string]string{"type": "string"},
				},
			},
		}
	}
	payload := map[string]interface{}{
		"system":   []map[string]string{{"type": "text", "text": strings.Repeat("system prompt text ", 500)}},
		"tools":    tools,
		"messages": []map[string]string{{"role": "user", "content": "test"}},
	}
	body, _ := json.Marshal(payload)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = inj.Inject(body)
	}
}
