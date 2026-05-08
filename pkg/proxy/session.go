package proxy

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type SessionState struct {
	ServerName       string          `json:"server_name"`
	ClientID         string          `json:"client_id"`
	Capabilities     []string        `json:"capabilities,omitempty"`
	InitializeParams json.RawMessage `json:"init_params,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	LastUsedAt       time.Time       `json:"last_used_at"`
}

type SessionCache struct {
	mu       sync.RWMutex
	sessions map[string]*SessionState
	ttl      time.Duration
	maxSize  int
}

func NewSessionCache(ttl time.Duration, maxSize int) *SessionCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &SessionCache{
		sessions: make(map[string]*SessionState),
		ttl:      ttl,
		maxSize:  maxSize,
	}
}

func (sc *SessionCache) GetOrCreateSession(serverName string) (*SessionState, error) {
	sc.mu.Lock()
	defer sc.mu.Unlock()

	session, exists := sc.sessions[serverName]

	if exists && sc.isValidLocked(session) {
		session.LastUsedAt = time.Now()
		return session, nil
	}

	if exists {
		delete(sc.sessions, serverName)
	}

	if len(sc.sessions) >= sc.maxSize {
		sc.evictOldestLocked()
	}

	newSession := &SessionState{
		ServerName: serverName,
		ClientID:   fmt.Sprintf("%s-%d", serverName, time.Now().UnixNano()),
		CreatedAt:  time.Now(),
		LastUsedAt: time.Now(),
	}

	sc.sessions[serverName] = newSession

	return newSession, nil
}

func (sc *SessionCache) isValidLocked(session *SessionState) bool {
	if session == nil {
		return false
	}
	return time.Since(session.LastUsedAt) < sc.ttl
}

func (sc *SessionCache) evictOldestLocked() {
	var oldestKey string
	var oldestTime time.Time

	for key, session := range sc.sessions {
		if oldestTime.IsZero() || session.LastUsedAt.Before(oldestTime) {
			oldestKey = key
			oldestTime = session.LastUsedAt
		}
	}

	if oldestKey != "" {
		delete(sc.sessions, oldestKey)
	}
}

func (sc *SessionCache) RestoreSession(state *SessionState) error {
	if state == nil {
		return fmt.Errorf("session: cannot restore nil state")
	}

	sc.mu.Lock()
	defer sc.mu.Unlock()

	if len(sc.sessions) >= sc.maxSize {
		sc.evictOldestLocked()
	}

	state.LastUsedAt = time.Now()
	sc.sessions[state.ServerName] = state

	return nil
}

func (ss *SessionState) Serialize() ([]byte, error) {
	if ss == nil {
		return nil, fmt.Errorf("session: cannot serialize nil state")
	}
	return json.Marshal(ss)
}

func DeserializeSessionState(data []byte) (*SessionState, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("session: empty data")
	}
	var state SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("session: deserialize: %w", err)
	}
	return &state, nil
}

func (sc *SessionCache) GetSession(serverName string) (*SessionState, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	session, exists := sc.sessions[serverName]
	if !exists {
		return nil, false
	}
	if time.Since(session.LastUsedAt) >= sc.ttl {
		return nil, false
	}
	return session, true
}

func (sc *SessionCache) RemoveSession(serverName string) {
	sc.mu.Lock()
	delete(sc.sessions, serverName)
	sc.mu.Unlock()
}

func (sc *SessionCache) Clear() {
	sc.mu.Lock()
	sc.sessions = make(map[string]*SessionState)
	sc.mu.Unlock()
}

func (sc *SessionCache) Size() int {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return len(sc.sessions)
}

func (sc *SessionCache) ListSessions() []string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	names := make([]string, 0, len(sc.sessions))
	for name := range sc.sessions {
		names = append(names, name)
	}
	return names
}
