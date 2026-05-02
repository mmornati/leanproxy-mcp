package registry

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

type ToolEntry struct {
	Name      string
	Namespace string
	ServerID  string
}

type ToolMatch struct {
	Tool     ToolEntry
	Score    float64
	MatchOn  string
}

type ToolRegistry interface {
	RegisterTool(ctx context.Context, serverID, toolName string) error
	UnregisterTool(ctx context.Context, serverID, toolName string) error
	GetToolServer(ctx context.Context, toolName string) (string, error)
	SearchTools(ctx context.Context, query string) []ToolMatch
	ListAllTools(ctx context.Context) []ToolEntry
	SubscribeTools(ch chan<- ToolEvent) func()
}

type ToolEvent struct {
	Type     ToolEventType
	ServerID string
	ToolName string
	Tool     ToolEntry
}

type ToolEventType int

const (
	ToolEventRegistered ToolEventType = iota
	ToolEventUnregistered
)

type inMemoryToolRegistry struct {
	tools       map[string]ToolEntry
	byNamespace map[string][]string
	byToolName  map[string][]string
	byServer    map[string][]string
	mu          sync.RWMutex
	subMu       sync.RWMutex
	subs        []chan<- ToolEvent
	logger      interface{ Debug(msg string, args ...any) }
}

func NewToolRegistry(logger interface{ Debug(msg string, args ...any) }) ToolRegistry {
	return &inMemoryToolRegistry{
		tools:       make(map[string]ToolEntry),
		byNamespace: make(map[string][]string),
		byToolName:  make(map[string][]string),
		byServer:    make(map[string][]string),
		logger:      logger,
	}
}

func (r *inMemoryToolRegistry) toolKey(serverID, toolName string) string {
	return serverID + "::" + toolName
}

func (r *inMemoryToolRegistry) RegisterTool(ctx context.Context, serverID, toolName string) error {
	if toolName == "" {
		return fmt.Errorf("registry: tool name is required: %w", contextToErr(ctx))
	}
	if serverID == "" {
		return fmt.Errorf("registry: server ID is required: %w", contextToErr(ctx))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := r.toolKey(serverID, toolName)
	namespace := extractNamespace(toolName)

	entry := ToolEntry{
		Name:      toolName,
		Namespace: namespace,
		ServerID:  serverID,
	}

	r.tools[key] = entry
	r.byNamespace[namespace] = append(r.byNamespace[namespace], key)
	r.byToolName[toolName] = append(r.byToolName[toolName], serverID)
	r.byServer[serverID] = append(r.byServer[serverID], key)

	r.emitToolEvent(ToolEvent{Type: ToolEventRegistered, ServerID: serverID, ToolName: toolName, Tool: entry})

	if r.logger != nil {
		r.logger.Debug("tool registered", "server", serverID, "tool", toolName, "namespace", namespace)
	}

	return nil
}

func (r *inMemoryToolRegistry) UnregisterTool(ctx context.Context, serverID, toolName string) error {
	if toolName == "" {
		return fmt.Errorf("registry: tool name is required: %w", contextToErr(ctx))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	key := r.toolKey(serverID, toolName)
	entry, exists := r.tools[key]
	if !exists {
		return fmt.Errorf("registry: tool not found: %s:%s: %w", serverID, toolName, contextToErr(ctx))
	}

	delete(r.tools, key)

	if keys, ok := r.byNamespace[entry.Namespace]; ok {
		newKeys := make([]string, 0, len(keys))
		for _, k := range keys {
			if k != key {
				newKeys = append(newKeys, k)
			}
		}
		r.byNamespace[entry.Namespace] = newKeys
	}

	if servers, ok := r.byToolName[toolName]; ok {
		newServers := make([]string, 0, len(servers))
		for _, sid := range servers {
			if sid != serverID {
				newServers = append(newServers, sid)
			}
		}
		r.byToolName[toolName] = newServers
	}

	if keys, ok := r.byServer[serverID]; ok {
		newKeys := make([]string, 0, len(keys))
		for _, k := range keys {
			if k != key {
				newKeys = append(newKeys, k)
			}
		}
		r.byServer[serverID] = newKeys
	}

	r.emitToolEvent(ToolEvent{Type: ToolEventUnregistered, ServerID: serverID, ToolName: toolName, Tool: entry})

	if r.logger != nil {
		r.logger.Debug("tool unregistered", "server", serverID, "tool", toolName)
	}

	return nil
}

func (r *inMemoryToolRegistry) GetToolServer(ctx context.Context, toolName string) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	servers := r.byToolName[toolName]
	if len(servers) == 0 {
		return "", fmt.Errorf("registry: tool not found: %s: %w", toolName, contextToErr(ctx))
	}
	if len(servers) > 1 {
		return "", fmt.Errorf("registry: ambiguous tool: %s (found in %d servers): %w", toolName, len(servers), contextToErr(ctx))
	}

	return servers[0], nil
}

func (r *inMemoryToolRegistry) SearchTools(ctx context.Context, query string) []ToolMatch {
	r.mu.RLock()
	defer r.mu.RUnlock()

	query = strings.ToLower(query)
	var matches []ToolMatch

	for key, entry := range r.tools {
		score := 0.0
		matchOn := ""

		nameLower := strings.ToLower(entry.Name)
		if nameLower == query {
			score = 100.0
			matchOn = "exact"
		} else if strings.Contains(nameLower, query) {
			score = 50.0
			matchOn = "contains"
		} else {
			nameParts := strings.Split(nameLower, ".")
			for _, part := range nameParts {
				if strings.HasPrefix(part, query) {
					score = 30.0
					matchOn = "prefix"
					break
				}
			}
		}

		if score > 0 {
			matches = append(matches, ToolMatch{
				Tool:    entry,
				Score:   score,
				MatchOn: matchOn,
			})
		}

		_ = key
	}

	return matches
}

func (r *inMemoryToolRegistry) ListAllTools(ctx context.Context) []ToolEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ToolEntry, 0, len(r.tools))
	for _, entry := range r.tools {
		result = append(result, entry)
	}

	return result
}

func (r *inMemoryToolRegistry) SubscribeTools(ch chan<- ToolEvent) func() {
	r.subMu.Lock()
	defer r.subMu.Unlock()

	r.subs = append(r.subs, ch)

	return func() {
		r.subMu.Lock()
		defer r.subMu.Unlock()

		for i, sub := range r.subs {
			if sub == ch {
				r.subs = append(r.subs[:i], r.subs[i+1:]...)
				break
			}
		}
	}
}

func (r *inMemoryToolRegistry) emitToolEvent(event ToolEvent) {
	r.subMu.RLock()
	defer r.subMu.RUnlock()

	for _, sub := range r.subs {
		select {
		case sub <- event:
		default:
			if r.logger != nil {
				r.logger.Debug("tool event channel full, dropping event", "type", event.Type)
			}
		}
	}
}

func extractNamespace(toolName string) string {
	dotIdx := strings.Index(toolName, ".")
	if dotIdx == -1 {
		return ""
	}
	return toolName[:dotIdx]
}
