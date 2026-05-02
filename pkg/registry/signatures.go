package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
)

type DiscoverySignature struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type Tool struct {
	Signature  DiscoverySignature `json:"signature"`
	FullSchema json.RawMessage    `json:"full_schema,omitempty"`
	ServerID   string             `json:"server_id"`
}

type ToolSchemaRegistry interface {
	RegisterTool(ctx context.Context, tool Tool) error
	UnregisterTool(ctx context.Context, serverID, toolName string) error
	GetDiscoverySignatures() []DiscoverySignature
	GetFullSchema(ctx context.Context, toolName string) (json.RawMessage, error)
	RefreshManifest(ctx context.Context) error
}

type inMemoryToolSchemaRegistry struct {
	signatures map[string]DiscoverySignature
	schemaCache map[string]json.RawMessage
	byServer   map[string][]string
	mu          sync.RWMutex
}

func NewToolSchemaRegistry() ToolSchemaRegistry {
	return &inMemoryToolSchemaRegistry{
		signatures: make(map[string]DiscoverySignature),
		schemaCache: make(map[string]json.RawMessage),
		byServer:   make(map[string][]string),
	}
}

func NewDiscoverySignature(name, description string, fullSchema json.RawMessage) (*DiscoverySignature, error) {
	sig := DiscoverySignature{
		Name:        name,
		Description: description,
	}

	data, err := json.Marshal(sig)
	if err != nil {
		return nil, fmt.Errorf("discovery signature: marshal: %w", err)
	}

	if len(data) > 500 {
		return nil, fmt.Errorf("discovery signature: exceeds 500 byte limit (%d bytes)", len(data))
	}

	return &sig, nil
}

func (r *inMemoryToolSchemaRegistry) RegisterTool(ctx context.Context, tool Tool) error {
	if tool.Signature.Name == "" {
		return fmt.Errorf("registry: tool name is required: %w", contextToErr(ctx))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.signatures[tool.Signature.Name] = tool.Signature

	if tool.FullSchema != nil {
		r.schemaCache[tool.Signature.Name] = tool.FullSchema
	}

	if tool.ServerID != "" {
		r.byServer[tool.ServerID] = append(r.byServer[tool.ServerID], tool.Signature.Name)
	}

	return nil
}

func (r *inMemoryToolSchemaRegistry) UnregisterTool(ctx context.Context, serverID, toolName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	entry, exists := r.signatures[toolName]
	if !exists {
		return fmt.Errorf("registry: tool not found: %s:%s: %w", serverID, toolName, contextToErr(ctx))
	}

	delete(r.signatures, toolName)
	delete(r.schemaCache, toolName)

	if keys, ok := r.byServer[serverID]; ok {
		newKeys := make([]string, 0, len(keys))
		for _, k := range keys {
			if k != toolName {
				newKeys = append(newKeys, k)
			}
		}
		r.byServer[serverID] = newKeys
	}

	_ = entry
	return nil
}

func (r *inMemoryToolSchemaRegistry) GetDiscoverySignatures() []DiscoverySignature {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]DiscoverySignature, 0, len(r.signatures))
	for _, sig := range r.signatures {
		result = append(result, sig)
	}

	return result
}

func (r *inMemoryToolSchemaRegistry) GetFullSchema(ctx context.Context, toolName string) (json.RawMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schema, exists := r.schemaCache[toolName]
	if !exists {
		return nil, fmt.Errorf("registry: full schema not found for tool: %s: %w", toolName, contextToErr(ctx))
	}

	return schema, nil
}

func (r *inMemoryToolSchemaRegistry) RefreshManifest(ctx context.Context) error {
	return nil
}
