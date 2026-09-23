package toolpin

import (
	"sync"
	"time"
)

// EventKind is the kind of a pinning event.
type EventKind string

const (
	// EventServerPinned: a server was seen for the first time and its
	// tools pinned (trust on first use).
	EventServerPinned EventKind = "server_pinned"
	// EventToolAdded: a server lists a tool that is not pinned.
	EventToolAdded EventKind = "tool_added"
	// EventToolChanged: a tool's definition differs from the approved one.
	EventToolChanged EventKind = "tool_changed"
	// EventToolRemoved: a pinned tool is no longer listed.
	EventToolRemoved EventKind = "tool_removed"
	// EventToolReverted: a changed tool is back to its approved definition.
	EventToolReverted EventKind = "tool_reverted"
	// EventServerIdentityChanged: the serverInfo name differs from the
	// pinned one.
	EventServerIdentityChanged EventKind = "server_identity_changed"
	// EventToolFlagged: the scanner reported a medium or high finding.
	EventToolFlagged EventKind = "tool_flagged"
	// EventToolShadowed: two servers expose tools whose normalized names
	// collide.
	EventToolShadowed EventKind = "tool_shadowed"
)

// Event is one pinning event, as logged, counted and shown by the
// dashboard.
type Event struct {
	Time     time.Time `json:"time"`
	Kind     EventKind `json:"kind"`
	Server   string    `json:"server"`
	Tool     string    `json:"tool,omitempty"`
	Severity Severity  `json:"severity,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	// Diff is the unified diff of the approved and the new definition
	// (tool_added, tool_changed).
	Diff string `json:"diff,omitempty"`
}

// recentCap bounds the in-process event history.
const recentCap = 200

var recent struct {
	mu     sync.Mutex
	events []Event
}

func recordRecent(ev Event) {
	recent.mu.Lock()
	defer recent.mu.Unlock()
	if len(recent.events) >= recentCap {
		copy(recent.events, recent.events[1:])
		recent.events = recent.events[:recentCap-1]
	}
	recent.events = append(recent.events, ev)
}

// RecentEvents returns this process's latest pinning events, newest first
// (at most 200; diffs left out).
func RecentEvents() []Event {
	recent.mu.Lock()
	defer recent.mu.Unlock()
	out := make([]Event, len(recent.events))
	for i, ev := range recent.events {
		ev.Diff = ""
		out[len(out)-1-i] = ev
	}
	return out
}

// resetRecentEvents clears the history (tests).
func resetRecentEvents() {
	recent.mu.Lock()
	recent.events = nil
	recent.mu.Unlock()
}
