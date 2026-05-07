---
story_id: 8.4
story_key: 8-4-cost-attribution
epic_num: 8
story_num: 4
story_title: "Implement Cost Attribution Layer"
status: ready-for-dev
created: 2026-05-07
source: market-research-2026-05-07
priority: HIGH
kpi_impact: "Per-tool token visibility (market differentiator)"
---

## Story

**As a** User of LeanProxy-MCP,
**I want to** track token usage per tool and per server,
**So that** I can see which tools consume the most tokens.

## Acceptance Criteria

### AC1: Per-Tool Tracking
**Given** a session is active
**When** tools are invoked
**Then** token counts are tracked per tool name
**And** per-MCP-server totals are accumulated

### AC2: Cost Command Output
**Given** the user runs `leanproxy cost`
**When** the command executes
**Then** a breakdown is shown:
- Token count per tool
- Token count per server  
- Total session tokens

### AC3: Socket API Access
**Given** cost attribution is enabled
**When** detailed tracking is available
**Then** the data is also available via the status socket

## Technical Requirements

### Implementation Location
- **Package:** `pkg/reporter/cost.go` (NEW FILE)
- **Integration:** Modify existing reporter for cost tracking

### Data Structures

```go
// CostTracker tracks token usage per tool/server
type CostTracker struct {
    mu sync.RWMutex
    // toolName -> token count
    byTool map[string]int64
    // serverName -> token count
    byServer map[string]int64
    // Total session
    total int64
    // Session start time
    startTime time.Time
}

// CostBreakdown formatted cost report
type CostBreakdown struct {
    ByTool   []ToolCost   `json:"by_tool"`
    ByServer []ServerCost `json:"by_server"`
    Total    int64       `json:"total"`
    Duration time.Duration `json:"duration"`
}
```

### CLI Command

```bash
# Show cost breakdown
leanproxy cost

# Show cost by server
leanproxy cost --by-server

# Show cost by tool  
leanproxy cost --by-tool

# Export JSON
leanproxy cost --json
```

## Implementation Tasks

- [ ] 1. Create `pkg/reporter/cost.go`
  - [ ] 1.1 Define CostTracker and CostBreakdown
  - [ ] 1.2 Implement Track() method
  - [ ] 1.3 Implement GetBreakdown()
  - [ ] 1.4 Implement FormatCLI()
- [ ] 2. Add CLI command in `cmd/leanproxy/cost.go`
- [ ] 3. Integrate with proxy for automatic tracking
- [ ] 4. Testing
  - [ ] 4.1 Unit tests
  - [ ] 4.2 Integration test

## Dev Notes

### Market Gap (No Proxy-Level Visibility)

Current MCP has no built-in cost attribution. This is a **differentiation opportunity**.

### Success Metrics

- Per-tool breakdown: ✓ Required
- Per-server breakdown: ✓ Required  
- CLI output: ✓ Required
- Socket API: ✓ Bonus

## References

- [Source: /planning-artifacts/epics.md#Epic-8-Story-8.4]
- [Source: /planning-artifacts/architecture.md#Epic-8-Token-Optimization]

---

**Status:** ready-for-dev