package mcp

import (
	"encoding/json"
	"testing"

	"github.com/mmornati/leanproxy-mcp/pkg/reporter"
)

func TestGetToolDefinition(t *testing.T) {
	tests := []struct {
		name       string
		searchName string
		wantNil    bool
	}{
		{
			name:       "list_servers exists",
			searchName: "list_servers",
			wantNil:    false,
		},
		{
			name:       "list_tools exists",
			searchName: "list_tools",
			wantNil:    false,
		},
		{
			name:       "invoke_tool exists",
			searchName: "invoke_tool",
			wantNil:    false,
		},
		{
			name:       "non_existent tool",
			searchName: "fake_tool",
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetToolDefinition(tt.searchName)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetToolDefinition() = %v, want nil=%v", got, tt.wantNil)
			}
		})
	}
}

func TestGetAllToolDefinitions(t *testing.T) {
	tools := GetAllToolDefinitions()
	if len(tools) != 3 {
		t.Errorf("GetAllToolDefinitions() = %d tools, want 3", len(tools))
	}

	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"list_servers", "list_tools", "invoke_tool"} {
		if !names[want] {
			t.Errorf("GetAllToolDefinitions() missing tool %q", want)
		}
	}
}

func TestToolDefinitionFields(t *testing.T) {
	listTools := GetToolDefinition("list_tools")
	if listTools == nil {
		t.Fatal("list_tools should not be nil")
	}

	if listTools.Name != "list_tools" {
		t.Errorf("Name = %s, want list_tools", listTools.Name)
	}

	if listTools.InputSchema == nil {
		t.Error("list_tools should have InputSchema")
	}
}

func TestInvokeToolDefinition(t *testing.T) {
	invokeTool := GetToolDefinition("invoke_tool")
	if invokeTool == nil {
		t.Fatal("invoke_tool should not be nil")
	}

	if len(invokeTool.Categories) == 0 {
		t.Error("invoke_tool should have categories")
	}
}

func TestListServersToolDefinition(t *testing.T) {
	listServers := GetToolDefinition("list_servers")
	if listServers == nil {
		t.Fatal("list_servers should not be nil")
	}

	if listServers.InputSchema == nil {
		t.Error("list_servers should have InputSchema")
	}

	var schema map[string]interface{}
	if err := json.Unmarshal(listServers.InputSchema, &schema); err != nil {
		t.Fatalf("list_servers InputSchema should be valid JSON: %v", err)
	}
	if _, ok := schema["required"]; ok {
		t.Error("list_servers should have no required parameters")
	}
}

// TestToolsListTokenBudget asserts the acceptance criterion from #300: the
// marshaled tools/list result for the 3 gateway tools must stay under 250
// estimated tokens (pkg/reporter.Estimator), matching what the README claims
// and what tests/bench measures.
func TestToolsListTokenBudget(t *testing.T) {
	type wireTool struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		InputSchema json.RawMessage `json:"inputSchema"`
	}

	defs := GetAllToolDefinitions()
	tools := make([]wireTool, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, wireTool{
			Name:        def.Name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		})
	}

	payload, err := json.Marshal(struct {
		Tools []wireTool `json:"tools"`
	}{Tools: tools})
	if err != nil {
		t.Fatalf("marshal tools/list result: %v", err)
	}

	estimator := reporter.NewEstimator()
	tokens := estimator.EstimateTokens(string(payload))

	const budget = 250
	if tokens >= budget {
		t.Errorf("tools/list estimated tokens = %d, want < %d", tokens, budget)
	}
	t.Logf("tools/list: %d bytes, %d estimated tokens", len(payload), tokens)
}
