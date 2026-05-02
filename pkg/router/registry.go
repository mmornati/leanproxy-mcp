package router

import (
	"context"
	"sync"
)

type ToolEntry struct {
	Name      string
	Namespace string
	ServerID  string
}

type ToolRegistry interface {
	RegisterTool(ctx context.Context, tool ToolEntry) error
	UnregisterTool(ctx context.Context, name string) error
	FindByNamespace(ctx context.Context, namespace string) ([]string, error)
	FindByToolName(ctx context.Context, toolName string) ([]string, error)
	FindServerForTool(ctx context.Context, toolName string) (string, error)
	ListTools(ctx context.Context) ([]ToolEntry, error)
}

type inMemoryToolRegistry struct {
	tools       map[string]ToolEntry
	byNamespace map[string][]string
	byToolName  map[string][]string
	mu          sync.RWMutex
}

func NewToolRegistry() ToolRegistry {
	return &inMemoryToolRegistry{
		tools:       make(map[string]ToolEntry),
		byNamespace: make(map[string][]string),
		byToolName:  make(map[string][]string),
	}
}

func (r *inMemoryToolRegistry) RegisterTool(ctx context.Context, tool ToolEntry) error {
	if tool.Name == "" {
		return NewRouterError(ErrCodeInternalError, "tool name is required", ErrInvalidMethod)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.tools[tool.Name] = tool
	r.byNamespace[tool.Namespace] = append(r.byNamespace[tool.Namespace], tool.Name)
	r.byToolName[tool.Name] = append(r.byToolName[tool.Name], tool.ServerID)

	return nil
}

func (r *inMemoryToolRegistry) UnregisterTool(ctx context.Context, name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	tool, exists := r.tools[name]
	if !exists {
		return NewRouterError(ErrCodeMethodNotFound, "tool not found: "+name, ErrToolNotFound)
	}

	delete(r.tools, name)

	if names, ok := r.byNamespace[tool.Namespace]; ok {
		newNames := make([]string, 0, len(names))
		for _, n := range names {
			if n != name {
				newNames = append(newNames, n)
			}
		}
		r.byNamespace[tool.Namespace] = newNames
	}

	delete(r.byToolName, name)

	return nil
}

func (r *inMemoryToolRegistry) FindByNamespace(ctx context.Context, namespace string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	serverIDs := make(map[string]struct{})
	for _, toolName := range r.byNamespace[namespace] {
		if tool, ok := r.tools[toolName]; ok {
			serverIDs[tool.ServerID] = struct{}{}
		}
	}

	result := make([]string, 0, len(serverIDs))
	for id := range serverIDs {
		result = append(result, id)
	}
	return result, nil
}

func (r *inMemoryToolRegistry) FindByToolName(ctx context.Context, toolName string) ([]string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	serverIDs := make([]string, 0)
	for _, tool := range r.tools {
		if tool.Name == toolName {
			serverIDs = append(serverIDs, tool.ServerID)
		}
	}
	return serverIDs, nil
}

func (r *inMemoryToolRegistry) FindServerForTool(ctx context.Context, toolName string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var serverID string
	var found bool

	for _, tool := range r.tools {
		if tool.Name == toolName {
			if found {
				return "", NewRouterError(ErrCodeInvalidParams, "ambiguous tool: "+toolName, ErrAmbiguousTool)
			}
			serverID = tool.ServerID
			found = true
		}
	}

	if !found {
		return "", NewRouterError(ErrCodeMethodNotFound, "tool not found: "+toolName, ErrToolNotFound)
	}

	return serverID, nil
}

func (r *inMemoryToolRegistry) ListTools(ctx context.Context) ([]ToolEntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ToolEntry, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result, nil
}
