---
story_id: 9.2
story_key: 9-2-hierarchical-namespaces
epic_num: 9
story_num: 2
story_title: "Implement Hierarchical Namespaces"
status: ready-for-dev
created: 2026-05-07
source: market-research-2026-05-07
priority: MEDIUM
kpi_impact: "Multi-team organization support"
---

## Story

**As an** Enterprise User,
**I want to** organize MCP servers into hierarchical namespaces,
**So that** multi-team organizations can manage access cleanly.

## Acceptance Criteria

### AC1: Namespace Configuration
**Given** namespace configuration in leanproxy.yaml
**When** the proxy starts
**Then** servers are grouped under their namespaces
**And** tools are namespaced accordingly

### AC2: Namespace Filtering
**Given** a client requests tools from namespace "engineering"
**When** the request arrives
**Then** only servers in the engineering namespace are included
**And** other namespaces are excluded

### AC3: Nested Namespace Hierarchy
**Given** nested namespaces are configured
**When** the proxy processes requests
**Then** the hierarchy is respected (parent includes child namespaces)

### AC4: Namespace Access Control
**Given** namespace-level access control is configured
**When** a client connects
**Then** access is restricted to their assigned namespaces

## Technical Requirements

### Configuration Schema

```yaml
# leanproxy.yaml
namespaces:
  engineering:
    description: "Engineering team tools"
    servers:
      - github
      - jira
    children:
      frontend:
        servers:
          - storybook
  ops:
    servers:
      - aws
      - kubernetes
```

### Data Structures

```go
// Namespace represents a namespace hierarchy
type Namespace struct {
    Name        string               `yaml:"name"`
    Description string               `yaml:"description,omitempty"`
    Servers    []string             `yaml:"servers"`
    Children   map[string]*Namespace `yaml:"children,omitempty"`
    // Access control
    AllowedClients []string         `yaml:"allowed_clients,omitempty"`
}

// NamespaceManager manages namespace hierarchy
type NamespaceManager struct {
    mu sync.RWMutex
    namespaces map[string]*Namespace
    // Flat lookup: toolName -> namespace
    toolNamespace map[string]string
}
```

### CLI Commands

```bash
# List namespaces
leanproxy namespace list

# Add namespace
leanproxy namespace add <name> --servers=<servers>

# Assign server to namespace
leanproxy namespace assign <namespace> <server>
```

## Implementation Tasks

- [ ] 1. Create `pkg/registry/namespace.go`
  - [ ] 1.1 Define Namespace and NamespaceManager
  - [ ] 1.2 Implement Load() from config
  - [ ] 1.3 Implement GetToolsForNamespace()
  - [ ] 1.4 Implement CheckAccess()
- [ ] 2. Modify config parsing
- [ ] 3. Add CLI commands
- [ ] 4. Testing

## Dev Notes

### Market Gap

No tool fully satisfies hierarchical namespaces + 1:many endpoint mapping.

### Success Metrics

- Hierarchy: ✓ Parent includes children
- Access control: ✓ Per-namespace
- Filtering: ✓ Tool name prefixed

## References

- [Source: /planning-artifacts/epics.md#Epic-9-Story-9.2]
- [Source: /planning-artifacts/architecture.md#Epic-9-Enterprise-Transport]

---

**Status:** ready-for-dev