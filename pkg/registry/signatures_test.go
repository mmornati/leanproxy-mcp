package registry

import (
	"context"
	"encoding/json"
	"testing"
)

func TestNewDiscoverySignature(t *testing.T) {
	tests := []struct {
		name        string
		toolName    string
		description string
		fullSchema  json.RawMessage
		wantErr     bool
		maxBytes    int
	}{
		{
			name:        "valid signature under 500 bytes",
			toolName:    "testTool",
			description: "A test tool description",
			fullSchema:  json.RawMessage(`{"type":"object"}`),
			wantErr:     false,
			maxBytes:    500,
		},
		{
			name:        "empty description",
			toolName:    "testTool",
			description: "",
			fullSchema:  json.RawMessage(`{"type":"object"}`),
			wantErr:     false,
			maxBytes:    500,
		},
		{
			name:        "long description still valid",
			toolName:    "testTool",
			description: "This is a much longer description that should still be under the 500 byte limit for a typical tool signature",
			fullSchema:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`),
			wantErr:     false,
			maxBytes:    500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sig, err := NewDiscoverySignature(tt.toolName, tt.description, tt.fullSchema)

			if tt.wantErr && err == nil {
				t.Errorf("NewDiscoverySignature() expected error, got nil")
				return
			}

			if !tt.wantErr && err != nil {
				t.Errorf("NewDiscoverySignature() unexpected error: %v", err)
				return
			}

			if err == nil {
				if sig.Name != tt.toolName {
					t.Errorf("Name = %v, want %v", sig.Name, tt.toolName)
				}
				if sig.Description != tt.description {
					t.Errorf("Description = %v, want %v", sig.Description, tt.description)
				}

				data, _ := json.Marshal(sig)
				if len(data) > tt.maxBytes {
					t.Errorf("signature size %d bytes exceeds limit %d", len(data), tt.maxBytes)
				}
			}
		})
	}
}

func TestInMemoryToolSchemaRegistry_RegisterTool(t *testing.T) {
	registry := NewToolSchemaRegistry().(*inMemoryToolSchemaRegistry)
	ctx := context.Background()

	t.Run("register valid tool", func(t *testing.T) {
		tool := Tool{
			Signature: DiscoverySignature{
				Name:        "testTool",
				Description: "A test tool",
			},
			FullSchema: json.RawMessage(`{"type":"object"}`),
			ServerID:   "server1",
		}

		err := registry.RegisterTool(ctx, tool)
		if err != nil {
			t.Errorf("RegisterTool() error = %v", err)
		}

		sigs := registry.GetDiscoverySignatures()
		if len(sigs) != 1 {
			t.Errorf("expected 1 signature, got %d", len(sigs))
		}
	})

	t.Run("register tool without name fails", func(t *testing.T) {
		tool := Tool{
			Signature: DiscoverySignature{
				Name:        "",
				Description: "A test tool",
			},
			ServerID: "server1",
		}

		err := registry.RegisterTool(ctx, tool)
		if err == nil {
			t.Errorf("RegisterTool() expected error for empty name, got nil")
		}
	})
}

func TestInMemoryToolSchemaRegistry_GetDiscoverySignatures(t *testing.T) {
	registry := NewToolSchemaRegistry().(*inMemoryToolSchemaRegistry)
	ctx := context.Background()

	tools := []Tool{
		{
			Signature: DiscoverySignature{Name: "tool1", Description: "First tool"},
			ServerID:  "server1",
		},
		{
			Signature: DiscoverySignature{Name: "tool2", Description: "Second tool"},
			ServerID:  "server1",
		},
		{
			Signature: DiscoverySignature{Name: "tool3", Description: "Third tool"},
			ServerID:  "server2",
		},
	}

	for _, tool := range tools {
		registry.RegisterTool(ctx, tool)
	}

	sigs := registry.GetDiscoverySignatures()

	if len(sigs) != 3 {
		t.Errorf("expected 3 signatures, got %d", len(sigs))
	}

	for _, sig := range sigs {
		if sig.Name == "" {
			t.Error("signature name should not be empty")
		}
	}
}

func TestInMemoryToolSchemaRegistry_GetFullSchema(t *testing.T) {
	registry := NewToolSchemaRegistry().(*inMemoryToolSchemaRegistry)
	ctx := context.Background()

	fullSchema := json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}}}`)
	tool := Tool{
		Signature: DiscoverySignature{
			Name:        "testTool",
			Description: "A test tool",
		},
		FullSchema: fullSchema,
		ServerID:   "server1",
	}

	registry.RegisterTool(ctx, tool)

	t.Run("get existing schema", func(t *testing.T) {
		schema, err := registry.GetFullSchema(ctx, "testTool")
		if err != nil {
			t.Errorf("GetFullSchema() error = %v", err)
			return
		}

		var parsed map[string]interface{}
		if err := json.Unmarshal(schema, &parsed); err != nil {
			t.Errorf("invalid schema JSON: %v", err)
		}
	})

	t.Run("get non-existent schema", func(t *testing.T) {
		_, err := registry.GetFullSchema(ctx, "nonExistent")
		if err == nil {
			t.Errorf("GetFullSchema() expected error for non-existent tool, got nil")
		}
	})
}

func TestInMemoryToolSchemaRegistry_UnregisterTool(t *testing.T) {
	registry := NewToolSchemaRegistry().(*inMemoryToolSchemaRegistry)
	ctx := context.Background()

	tool := Tool{
		Signature: DiscoverySignature{
			Name:        "testTool",
			Description: "A test tool",
		},
		ServerID: "server1",
	}

	registry.RegisterTool(ctx, tool)

	sigs := registry.GetDiscoverySignatures()
	if len(sigs) != 1 {
		t.Fatalf("expected 1 signature after register, got %d", len(sigs))
	}

	err := registry.UnregisterTool(ctx, "server1", "testTool")
	if err != nil {
		t.Errorf("UnregisterTool() error = %v", err)
	}

	sigs = registry.GetDiscoverySignatures()
	if len(sigs) != 0 {
		t.Errorf("expected 0 signatures after unregister, got %d", len(sigs))
	}
}

func TestDiscoverySignature_Serialization(t *testing.T) {
	sig := DiscoverySignature{
		Name:        "testTool",
		Description: "A test tool description",
	}

	data, err := json.Marshal(sig)
	if err != nil {
		t.Errorf("Marshal() error = %v", err)
		return
	}

	var parsed DiscoverySignature
	err = json.Unmarshal(data, &parsed)
	if err != nil {
		t.Errorf("Unmarshal() error = %v", err)
		return
	}

	if parsed.Name != sig.Name {
		t.Errorf("Name = %v, want %v", parsed.Name, sig.Name)
	}
	if parsed.Description != sig.Description {
		t.Errorf("Description = %v, want %v", parsed.Description, sig.Description)
	}
}

func TestDiscoveryPayloadSize(t *testing.T) {
	registry := NewToolSchemaRegistry().(*inMemoryToolSchemaRegistry)
	ctx := context.Background()

	for i := 0; i < 50; i++ {
		tool := Tool{
			Signature: DiscoverySignature{
				Name:        "tool" + string(rune('0'+i/10)) + string(rune('0'+i%10)),
				Description: "Tool number " + string(rune('0'+i/10)) + string(rune('0'+i%10)),
			},
			FullSchema: json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"value":{"type":"number"}}}`),
			ServerID:   "server1",
		}
		registry.RegisterTool(ctx, tool)
	}

	sigs := registry.GetDiscoverySignatures()

	totalSize := 0
	for _, sig := range sigs {
		data, _ := json.Marshal(sig)
		totalSize += len(data)
	}

	if totalSize > 25000 {
		t.Errorf("50-tool discovery payload %d bytes exceeds 25KB limit", totalSize)
	}

	t.Logf("50-tool discovery payload: %d bytes", totalSize)
}
