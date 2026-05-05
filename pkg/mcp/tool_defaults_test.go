package mcp

import (
	"testing"
)

func TestApplyDefaults(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		args     map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "list_tools with no max_description_chars",
			toolName: "list_tools",
			args:    map[string]interface{}{"server_name": "github"},
			expected: map[string]interface{}{"server_name": "github", "max_description_chars": 200},
		},
		{
			name:     "list_tools with existing max_description_chars",
			toolName: "list_tools",
			args:    map[string]interface{}{"server_name": "github", "max_description_chars": float64(100)},
			expected: map[string]interface{}{"server_name": "github", "max_description_chars": float64(100)},
		},
		{
			name:     "list_tools with nil args",
			toolName: "list_tools",
			args:    nil,
			expected: map[string]interface{}{"max_description_chars": 200},
		},
		{
			name:     "invoke_tool with empty args",
			toolName: "invoke_tool",
			args:    map[string]interface{}{},
			expected: map[string]interface{}{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ApplyDefaults(tt.toolName, tt.args)
			for key, expectedVal := range tt.expected {
				if got[key] != expectedVal {
					t.Errorf("ApplyDefaults()[%s] = %v, want %v", key, got[key], expectedVal)
				}
			}
		})
	}
}

func TestValidateParam(t *testing.T) {
	tests := []struct {
		name      string
		toolName string
		param   string
		value   interface{}
		wantOk  bool
	}{
		{
			name:      "valid max_description_chars",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   float64(100),
			wantOk:  true,
		},
		{
			name:      "max_description_chars below min",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   float64(30),
			wantOk:  false,
		},
		{
			name:      "max_description_chars above max",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   float64(600),
			wantOk:  false,
		},
		{
			name:      "edge case min",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   float64(50),
			wantOk:  true,
		},
		{
			name:      "edge case max",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   float64(500),
			wantOk:  true,
		},
		{
			name:      "zero value should pass",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   float64(0),
			wantOk:  true,
		},
		{
			name:      "nil value should pass",
			toolName: "list_tools",
			param:   "max_description_chars",
			value:   nil,
			wantOk:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := ValidateParam(tt.toolName, tt.param, tt.value)
			if got != tt.wantOk {
				t.Errorf("ValidateParam() = %v, want %v", got, tt.wantOk)
			}
		})
	}
}

func TestGetParamMeta(t *testing.T) {
	meta := GetParamMeta("list_tools", "server_name")
	if meta == nil {
		t.Fatal("meta should not be nil")
	}

	if !meta.Required {
		t.Error("server_name should be required")
	}

	if meta.Description == "" {
		t.Error("server_name should have description")
	}

	meta = GetParamMeta("list_tools", "max_description_chars")
	if meta == nil {
		t.Fatal("meta should not be nil for max_description_chars")
	}

	if meta.Default != 200 {
		t.Errorf("max_description_chars default = %v, want 200", meta.Default)
	}
}

func TestGetAllParamDefaults(t *testing.T) {
	defaults := GetAllParamDefaults("list_tools")
	if defaults == nil {
		t.Fatal("defaults should not be nil")
	}

	if defaults["max_description_chars"] != 200 {
		t.Errorf("max_description_chars default = %v, want 200", defaults["max_description_chars"])
	}
}