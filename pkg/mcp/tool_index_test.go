package mcp

import (
	"testing"
)

func TestGetToolDefinition(t *testing.T) {
	tests := []struct {
		name       string
		searchName string
		wantNil   bool
	}{
		{
			name:       "search_tools exists",
			searchName: "search_tools",
			wantNil:   false,
		},
		{
			name:       "invoke_tool exists",
			searchName: "invoke_tool",
			wantNil:   false,
		},
		{
			name:       "non_existent tool",
			searchName: "fake_tool",
			wantNil:   true,
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
	searchTools := GetToolDefinition("search_tools")
	if searchTools == nil {
		t.Fatal("search_tools should not be nil")
	}

	if searchTools.Name != "search_tools" {
		t.Errorf("Name = %s, want search_tools", searchTools.Name)
	}

	if len(searchTools.Examples) == 0 {
		t.Error("search_tools should have examples")
	}

	if searchTools.InputSchema == nil {
		t.Error("search_tools should have InputSchema")
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