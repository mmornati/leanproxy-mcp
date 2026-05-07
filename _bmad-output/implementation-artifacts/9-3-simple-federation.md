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

- [ ] 1. Create `pkg/federation/peer.go`
  - [ ] 1.1 Define Peer and PeerManager
  - [ ] 1.2 Implement Connect() to peer
  - [ ] 1.3 Implement DiscoverTools()
  - [ ] 1.4 Implement Invoke()
- [ ] 2. Create `pkg/federation/router.go`
  - [ ] 2.1 Tool index management
  - [ ] 2.2 Routing with failover
- [ ] 3. Configuration parsing
- [ ] 4. Testing

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

**Status:** ready-for-dev