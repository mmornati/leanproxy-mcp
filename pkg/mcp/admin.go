package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"time"
)

// newSessionLabel returns the short random label of a client session.
func newSessionLabel() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "session"
	}
	return hex.EncodeToString(b[:])
}

// SessionInfo describes one connected client, for the admin views.
type SessionInfo struct {
	ID              string    `json:"id"`
	Client          string    `json:"client"`
	ClientVersion   string    `json:"client_version,omitempty"`
	ProtocolVersion string    `json:"protocol_version,omitempty"`
	Exposure        string    `json:"exposure,omitempty"`
	Initialized     bool      `json:"initialized"`
	OpenedAt        time.Time `json:"opened_at"`
	LastCallAt      time.Time `json:"last_call_at,omitzero"`
	Calls           int64     `json:"calls"`
}

// Sessions lists the open client sessions, oldest first.
func (h *Handler) Sessions() []SessionInfo {
	h.sessionsMu.Lock()
	sessions := make([]*ClientSession, 0, len(h.sessions))
	for s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.sessionsMu.Unlock()

	out := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		info := s.ClientInfo()
		si := SessionInfo{
			ID:              s.id,
			Client:          info.Name,
			ClientVersion:   info.Version,
			ProtocolVersion: s.ProtocolVersion(),
			Initialized:     s.Initialized(),
			OpenedAt:        s.openedAt,
			Calls:           s.calls.Load(),
		}
		if si.Initialized {
			si.Exposure = string(s.ExposureMode())
		}
		if ns := s.lastActive.Load(); ns > 0 {
			si.LastCallAt = time.Unix(0, ns)
		}
		out = append(out, si)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OpenedAt.Equal(out[j].OpenedAt) {
			return out[i].OpenedAt.Before(out[j].OpenedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ToolCount is how many tools the named server is known to have, and
// whether its tool list is known at all (from the persistent cache or a
// refresh).
func (h *Handler) ToolCount(server string) (int, bool) {
	h.toolCache.mu.RLock()
	defer h.toolCache.mu.RUnlock()
	tools, ok := h.toolCache.tools[server]
	return len(tools), ok
}

// ServerAdded tells the handler a server joined the pool while it was
// running (a config reload): its tools are fetched in the background and
// listing clients are told when they arrive.
func (h *Handler) ServerAdded(name string) {
	h.startRefresh(name, true)
}

// ServerRemoved tells the handler a server left the pool: its tools leave
// the cache and the search index, and listing clients get a
// tools/list_changed. The persistent tool cache keeps the entry, so a
// server that comes back is served from it at once.
func (h *Handler) ServerRemoved(name string) {
	h.toolCache.mu.Lock()
	_, had := h.toolCache.tools[name]
	delete(h.toolCache.tools, name)
	h.toolCache.mu.Unlock()
	h.searchIndex().RemoveServer(name)

	h.refreshMu.Lock()
	delete(h.refreshErrs, name)
	h.refreshMu.Unlock()

	if had {
		h.exposureChanged(true)
	}
}
