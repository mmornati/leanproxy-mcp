---
story_id: 9.3
story_key: 9-3-simple-federation
epic_num: 9
story_num: 3
story_title: "Implement Simple Federation"
status: ready-for-dev
created: 2026-05-07
source: market-research-2026-05-07
priority: MEDIUM
kpi_impact: "Multi-organization tool sharing"
---

## Story

**As an** Enterprise User,
**I want to** connect multiple LeanProxy instances,
**So that** servers can be federated across organizations.

## Acceptance Criteria

### AC1: Peer Discovery
**Given** federation configuration is defined
**When** the proxy starts
**Then** it can discover and connect to other LeanProxy instances

### AC2: Cross-Instance Routing
**Given** a tool request for an unknown tool
**When** the proxy processes it
**Then** it looks up the tool in federated instances
**And** routes to the instance that has the tool

### AC3: Failover Handling
**Given** a federated instance goes offline
**When** requests are pending
**Then** the proxy detects the failure
**And** routes to backup instances if available

## Technical Requirements

### Configuration

```yaml
# leanproxy.yaml
federation:
  enabled: true
  peers:
    - url: "https://proxy.company-a.internal:8080"
      name: "company-a"
      auth_token: "..."  # Optional
    - url: "https://proxy.company-b.internal:8080"
      name: "company-b"
```

### Data Structures

```go
// Peer represents a federated LeanProxy instance
type Peer struct {
    Name     string    `yaml:"name"`
    URL      string    `yaml:"url"`
    AuthToken string   `yaml:"auth_token,omitempty"`
    // State
    Status   PeerStatus `yaml:"-"`
    LastCheck time.Time `yaml:"-"`
}

// PeerManager manages federated peer connections
type PeerManager struct {
    mu sync.RWMutex
    peers map[string]*Peer
    // Local tool index: toolName -> peerName
    toolIndex map[string]string
}
```

### Federation Protocol

```
# Tool discovery via federation
POST /federation/list-tools
Response: { "tools": ["tool1@server", "tool2@server", ...] }

# Tool invocation via federation  
POST /federation/invoke
Body: { "server": "github", "tool": "create_issue", "params": {...} }
Response: { "result": {...} }
```

## Implementation Tasks

- [x] 1. Create `pkg/federation/peer.go`
  - [x] 1.1 Define Peer and PeerManager
  - [x] 1.2 Implement Connect() to peer
  - [x] 1.3 Implement DiscoverTools()
  - [x] 1.4 Implement Invoke()
- [x] 2. Create `pkg/federation/router.go`
  - [x] 2.1 Tool index management
  - [x] 2.2 Routing with failover
- [x] 3. Configuration parsing
- [x] 4. Testing

## Dev Notes

### Scope: Simple Federation

Not full mesh networking - just configured peer connections with:
- Tool discovery (look up what peers have)
- Failover (if peer down, try next)

### Success Metrics

- Peer discovery: ✓ mDNS or configured
- Tool routing: ✓ Cross-instance
- Failover: ✓ On peer failure

## References

- [Source: /planning-artifacts/epics.md#Epic-9-Story-9.3]
- [Source: /planning-artifacts/architecture.md#Epic-9-Enterprise-Transport]

---

## Dev Agent Record

### Implementation Plan

Simple Federation implementation with configured peer connections:
- Added `FederationConfig` to `pkg/migrate/config.go` for YAML config parsing
- Created `pkg/federation/peer.go` with Peer struct, PeerManager with Connect, DiscoverTools, Invoke methods
- Created `pkg/federation/router.go` with FederationRouter for cross-instance tool routing with failover

### Completion Notes

- All 8 unit tests pass for federation package
- All 894 tests pass in the entire project (no regressions)
- PeerManager supports: Connect, DiscoverTools, Invoke operations
- FederationRouter handles tool routing with automatic failover to backup peers
- Configuration via `federation.enabled` and `federation.peers[]` in leanproxy_servers.yaml

### File List

- pkg/federation/doc.go (new)
- pkg/federation/peer.go (new)
- pkg/federation/router.go (new)
- pkg/federation/peer_test.go (new)
- pkg/migrate/config.go (modified - added FederationConfig)

### Change Log

- Added FederationConfig struct for YAML config parsing (Date: 2026-05-08)
- Created federation package with Peer, PeerManager, and FederationRouter (Date: 2026-05-08)
- Added comprehensive unit tests for federation functionality (Date: 2026-05-08)

---

**Status:** review