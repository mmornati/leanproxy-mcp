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

- [x] 1. Create `pkg/registry/namespace.go`
  - [x] 1.1 Define Namespace and NamespaceManager
  - [x] 1.2 Implement Load() from config
  - [x] 1.3 Implement GetToolsForNamespace()
  - [x] 1.4 Implement CheckAccess()
- [x] 2. Modify config parsing
- [x] 3. Add CLI commands
- [x] 4. Testing

## Dev Notes

### Market Gap

No tool fully satisfies hierarchical namespaces + 1:many endpoint mapping.

### Success Metrics

- Hierarchy: ✓ Parent includes children
- Access control: ✓ Per-namespace
- Filtering: ✓ Tool name prefixed

## Dev Agent Record

### Implementation Plan

Implemented hierarchical namespace support for multi-team organizations:
- `Namespace` struct with support for nested children, server lists, and access control
- `NamespaceManager` interface for managing namespace hierarchy
- `Load()` method parses YAML configuration for namespace definitions
- `CheckAccess()` validates client access to specific namespaces
- `GetChildNamespaces()` traverses hierarchical structure
- CLI commands for namespace list/add/assign operations

### Completion Notes

✅ Implemented hierarchical namespaces feature supporting:
- Namespace configuration via YAML (leanproxy.yaml)
- Nested namespace hierarchy (parent/child relationships)
- Per-namespace access control (allowed_clients list with wildcard support)
- Server-to-namespace mapping
- CLI commands: `leanproxy namespace list`, `leanproxy namespace add`, `leanproxy namespace assign`
- Comprehensive unit tests covering all ACs

## File List

- pkg/registry/namespace.go (NEW)
- pkg/registry/namespace_test.go (NEW)
- cmd/namespace.go (NEW)

## Change Log

- 2026-05-08: Implement hierarchical namespaces feature (9.2) - Added NamespaceManager with hierarchical support, access control, and CLI commands

## References

- [Source: /planning-artifacts/epics.md#Epic-9-Story-9.2]
- [Source: /planning-artifacts/architecture.md#Epic-9-Enterprise-Transport]

---

**Status:** review