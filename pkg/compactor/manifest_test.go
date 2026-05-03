package compactor

import (
	"context"
	"encoding/json"
	"testing"
)

func TestManifestProcessor_Process(t *testing.T) {
	processor := NewManifestProcessor(nil)

	manifest := RawManifest{
		Name:        "test-server",
		Description: "A test server",
		Tools: []RawTool{
			{
				Name:        "tool1",
				Description: "First tool description that is quite long",
				Parameters:  json.RawMessage(`{"type":"object"}`),
			},
			{
				Name:        "tool2",
				Description: "Short",
				Parameters:  json.RawMessage(`{"type":"string"}`),
			},
		},
	}

	result, err := processor.Process(context.Background(), manifest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.ServerName != "test-server" {
		t.Errorf("expected server_name 'test-server', got %s", result.ServerName)
	}

	if len(result.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(result.Tools))
	}

	if result.Tools[0].Name != "tool1" {
		t.Errorf("expected tool name 'tool1', got %s", result.Tools[0].Name)
	}

	if len(result.Tools[0].Description) > 50 {
		t.Error("description should be compacted to under 50 chars")
	}
}

func TestManifestProcessor_Process_EmptyTools(t *testing.T) {
	processor := NewManifestProcessor(nil)

	manifest := RawManifest{
		Name:        "test-server",
		Description: "A test server",
		Tools:       []RawTool{},
	}

	_, err := processor.Process(context.Background(), manifest)
	if err == nil {
		t.Error("expected error for empty tools")
	}
}

func TestCompactDescription(t *testing.T) {
	processor := NewManifestProcessor(nil)

	testLong := "This description is longer than fifty characters"
	result, err := processor.Process(context.Background(), RawManifest{
		Name: "test",
		Tools: []RawTool{
			{Name: "tool", Description: testLong, Parameters: json.RawMessage("{}")},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil && len(result.Tools) > 0 {
		desc := result.Tools[0].Description
		if len(desc) > 50 {
			t.Errorf("description should be compacted, got %q (len=%d)", desc, len(desc))
		}
	}

	testCases := []struct {
		input    string
		expected string
	}{
		{"Short", "Short"},
		{"Exactly fifty characters!12345678901234567890", "Exactly fifty characters!12345678901234567890"},
	}

	for _, tc := range testCases {
		result, err := processor.Process(context.Background(), RawManifest{
			Name: "test",
			Tools: []RawTool{
				{Name: "tool", Description: tc.input, Parameters: json.RawMessage("{}")},
			},
		})
		if err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.input, err)
		}
		if result == nil || len(result.Tools) == 0 {
			t.Fatalf("unexpected nil result for %q", tc.input)
		}
		if result.Tools[0].Description != tc.expected {
			t.Errorf("for %q got %q, want %q", tc.input, result.Tools[0].Description, tc.expected)
		}
	}
}

func TestValidateDistilledManifest(t *testing.T) {
	tests := []struct {
		name    string
		m       *DistilledManifest
		wantErr bool
	}{
		{
			name: "valid",
			m: &DistilledManifest{
				ServerName: "test",
				Tools: []DistilledTool{
					{Name: "tool", Description: "Test", Parameters: json.RawMessage("{}")},
				},
			},
			wantErr: false,
		},
		{
			name:    "nil",
			m:       nil,
			wantErr: true,
		},
		{
			name: "missing server name",
			m: &DistilledManifest{
				ServerName: "",
				Tools:      []DistilledTool{},
			},
			wantErr: true,
		},
		{
			name: "no tools",
			m: &DistilledManifest{
				ServerName: "test",
				Tools:      []DistilledTool{},
			},
			wantErr: true,
		},
		{
			name: "description too long",
			m: &DistilledManifest{
				ServerName: "test",
				Tools: []DistilledTool{
					{Name: "tool", Description: "This description is definitely longer than fifty characters and should fail validation", Parameters: json.RawMessage("{}")},
				},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateDistilledManifest(tc.m)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateDistilledManifest() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestCalculateTokenReduction(t *testing.T) {
	original := []byte(`{"name":"test","description":"A very long description that should be reduced significantly when tokens are calculated"}`)
	distilled := []byte(`{"name":"test","description":"Short"}`)

	reduction := calculateTokenReduction(original, distilled)

	if reduction < 0 {
		t.Errorf("expected positive reduction, got %f", reduction)
	}
}

func TestCalculateTokenReduction_EmptyOriginal(t *testing.T) {
	reduction := calculateTokenReduction([]byte{}, []byte(`{"test":1}`))
	if reduction != 0 {
		t.Errorf("expected 0 for empty original, got %f", reduction)
	}
}
