package registry

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

type Namespace struct {
	Name           string               `yaml:"name"`
	Description    string               `yaml:"description,omitempty"`
	Servers        []string             `yaml:"servers"`
	Children       map[string]*Namespace `yaml:"children,omitempty"`
	AllowedClients []string             `yaml:"allowed_clients,omitempty"`
}

type NamespaceConfig struct {
	Namespaces map[string]*Namespace `yaml:"namespaces,omitempty"`
}

type NamespaceManager interface {
	Load(ctx context.Context, r io.Reader) error
	GetNamespace(ctx context.Context, name string) (*Namespace, error)
	GetToolsForNamespace(ctx context.Context, ns string) ([]string, error)
	CheckAccess(ctx context.Context, ns, clientID string) error
	GetAllNamespaces(ctx context.Context) []*Namespace
	GetServerNamespace(ctx context.Context, serverID string) (string, error)
	GetChildNamespaces(ctx context.Context, parentName string) ([]string, error)
	ListToolsInNamespace(ctx context.Context, nsName string) ([]ToolEntry, error)
}

type inMemoryNamespaceManager struct {
	mu           sync.RWMutex
	namespaces   map[string]*Namespace
	toolNS       map[string]string
	serverNS     map[string]string
	logger       *slog.Logger
}

func NewNamespaceManager(logger *slog.Logger) NamespaceManager {
	return &inMemoryNamespaceManager{
		namespaces: make(map[string]*Namespace),
		toolNS:     make(map[string]string),
		serverNS:   make(map[string]string),
		logger:     logger,
	}
}

func (m *inMemoryNamespaceManager) Load(ctx context.Context, r io.Reader) error {
	if r == nil {
		return nil
	}

	var cfg NamespaceConfig
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return fmt.Errorf("namespace config: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.namespaces = make(map[string]*Namespace)
	m.toolNS = make(map[string]string)
	m.serverNS = make(map[string]string)

	for name, ns := range cfg.Namespaces {
		ns.Name = name
		m.namespaces[name] = ns
		m.buildToolMapping(ns, name)
	}

	m.logger.Info("namespace config loaded", "count", len(m.namespaces))
	return nil
}

func (m *inMemoryNamespaceManager) buildToolMapping(ns *Namespace, nsName string) {
	for _, serverID := range ns.Servers {
		m.serverNS[serverID] = nsName
	}

	for childName, child := range ns.Children {
		fullName := nsName + "." + childName
		child.Name = fullName
		m.namespaces[fullName] = child
		m.buildToolMapping(child, fullName)
	}
}

func (m *inMemoryNamespaceManager) GetNamespace(ctx context.Context, name string) (*Namespace, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, exists := m.namespaces[name]
	if !exists {
		return nil, fmt.Errorf("namespace not found: %s", name)
	}

	return m.deepCopyNamespace(ns), nil
}

func (m *inMemoryNamespaceManager) deepCopyNamespace(ns *Namespace) *Namespace {
	if ns == nil {
		return nil
	}
	result := &Namespace{
		Name:           ns.Name,
		Description:    ns.Description,
		Servers:        make([]string, len(ns.Servers)),
		Children:       make(map[string]*Namespace),
		AllowedClients: make([]string, len(ns.AllowedClients)),
	}
	copy(result.Servers, ns.Servers)
	copy(result.AllowedClients, ns.AllowedClients)
	for k, v := range ns.Children {
		result.Children[k] = m.deepCopyNamespace(v)
	}
	return result
}

func (m *inMemoryNamespaceManager) GetToolsForNamespace(ctx context.Context, nsName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, exists := m.namespaces[nsName]
	if !exists {
		return nil, fmt.Errorf("namespace not found: %s", nsName)
	}

	var serverIDs []string
	m.collectServers(ns, &serverIDs)

	tools := make([]string, 0, len(serverIDs))
	for _, sid := range serverIDs {
		tools = append(tools, nsName+"."+sid)
	}

	return tools, nil
}

func (m *inMemoryNamespaceManager) collectServers(ns *Namespace, servers *[]string) {
	*servers = append(*servers, ns.Servers...)
	for _, child := range ns.Children {
		m.collectServers(child, servers)
	}
}

func (m *inMemoryNamespaceManager) CheckAccess(ctx context.Context, nsName, clientID string) error {
	ns, err := m.GetNamespace(ctx, nsName)
	if err != nil {
		return err
	}

	if len(ns.AllowedClients) == 0 {
		return nil
	}

	for _, allowed := range ns.AllowedClients {
		if allowed == clientID || allowed == "*" {
			return nil
		}
	}

	return fmt.Errorf("client %s not allowed in namespace %s", clientID, nsName)
}

func (m *inMemoryNamespaceManager) GetAllNamespaces(ctx context.Context) []*Namespace {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Namespace, 0, len(m.namespaces))
	for _, ns := range m.namespaces {
		result = append(result, ns)
	}
	return result
}

func (m *inMemoryNamespaceManager) GetServerNamespace(ctx context.Context, serverID string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, exists := m.serverNS[serverID]
	if !exists {
		return "", fmt.Errorf("server %s not in any namespace", serverID)
	}
	return ns, nil
}

func (m *inMemoryNamespaceManager) GetChildNamespaces(ctx context.Context, parentName string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var children []string
	for name := range m.namespaces {
		if strings.HasPrefix(name, parentName+".") {
			parts := strings.Split(name[len(parentName)+1:], ".")
			if len(parts) == 1 || (len(parts) > 1 && !strings.Contains(parts[0], ".")) {
				children = append(children, name)
			}
		}
	}
	return children, nil
}

func (m *inMemoryNamespaceManager) ListToolsInNamespace(ctx context.Context, nsName string) ([]ToolEntry, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ns, exists := m.namespaces[nsName]
	if !exists {
		return nil, fmt.Errorf("namespace not found: %s", nsName)
	}

	var serverIDs []string
	m.collectServers(ns, &serverIDs)

	var toolSet = make(map[string]bool)
	for _, sid := range serverIDs {
		toolSet[sid] = true
	}

	var results []ToolEntry
	for serverID := range toolSet {
		results = append(results, ToolEntry{
			Name:      nsName + "." + serverID,
			Namespace: nsName,
			ServerID:  serverID,
		})
	}

	return results, nil
}