package mcp

import (
	"testing"
)

func TestGetToolDefinition(t *testing.T) {
	tests := []struct {
		name       string
		searchName string
		wantNil    bool
	}{
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
	if len(tools) != 2 {
		t.Errorf("GetAllToolDefinitions() = %d tools, want 2", len(tools))
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

	if len(listTools.Examples) == 0 {
		t.Error("list_tools should have examples")
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

	if len(invokeTool.Examples) == 0 {
		t.Error("invoke_tool should have examples")
	}

	if len(invokeTool.Categories) == 0 {
		t.Error("invoke_tool should have categories")
	}
}
