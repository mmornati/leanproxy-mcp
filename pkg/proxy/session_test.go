package proxy

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionState_Serialize(t *testing.T) {
	state := &SessionState{
		ServerName: "test-server",
		ClientID:   "client-123",
		Capabilities: []string{"tools", "resources"},
		InitializeParams: json.RawMessage(`{"key":"value"}`),
		CreatedAt:   time.Now(),
		LastUsedAt: time.Now(),
	}

	data, err := state.Serialize()
	require.NoError(t, err)
	assert.NotEmpty(t, data)

	restored, err := DeserializeSessionState(data)
	require.NoError(t, err)
	assert.Equal(t, state.ServerName, restored.ServerName)
	assert.Equal(t, state.ClientID, restored.ClientID)
	assert.Equal(t, state.Capabilities, restored.Capabilities)
}

func TestSessionState_SerializeNil(t *testing.T) {
	var state *SessionState

	data, err := state.Serialize()
	require.Error(t, err)
	assert.Empty(t, data)
}

func TestDesearializeSessionState_Empty(t *testing.T) {
	_, err := DeserializeSessionState(nil)
	require.Error(t, err)

	_, err = DeserializeSessionState([]byte{})
	require.Error(t, err)
}

func TestNewSessionCache(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 100)
	assert.NotNil(t, cache)
	assert.Equal(t, 0, cache.Size())
}

func TestSessionCache_GetOrCreateSession(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	session1, err := cache.GetOrCreateSession("server-1")
	require.NoError(t, err)
	assert.NotNil(t, session1)
	assert.Equal(t, "server-1", session1.ServerName)
	assert.NotEmpty(t, session1.ClientID)
	assert.True(t, time.Since(session1.CreatedAt) < time.Second)

	session2, err := cache.GetOrCreateSession("server-1")
	require.NoError(t, err)
	assert.Equal(t, session1.ClientID, session2.ClientID)
}

func TestSessionCache_GetOrCreateSession_Eviction(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 2)

	_, err := cache.GetOrCreateSession("server-1")
	require.NoError(t, err)

	_, err = cache.GetOrCreateSession("server-2")
	require.NoError(t, err)

	_, err = cache.GetOrCreateSession("server-3")
	require.NoError(t, err)

	assert.LessOrEqual(t, cache.Size(), 2)
}

func TestSessionCache_GetSession(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	_, ok := cache.GetSession("server-1")
	assert.False(t, ok)

	_, err := cache.GetOrCreateSession("server-1")
	require.NoError(t, err)

	session, ok := cache.GetSession("server-1")
	assert.True(t, ok)
	assert.Equal(t, "server-1", session.ServerName)
}

func TestSessionCache_GetSession_Expired(t *testing.T) {
	cache := NewSessionCache(50*time.Millisecond, 10)

	_, err := cache.GetOrCreateSession("server-1")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	_, ok := cache.GetSession("server-1")
	assert.False(t, ok)
}

func TestSessionCache_RemoveSession(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	_, err := cache.GetOrCreateSession("server-1")
	require.NoError(t, err)
	assert.Equal(t, 1, cache.Size())

	cache.RemoveSession("server-1")
	assert.Equal(t, 0, cache.Size())
}

func TestSessionCache_Clear(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	cache.GetOrCreateSession("server-1")
	cache.GetOrCreateSession("server-2")
	assert.Equal(t, 2, cache.Size())

	cache.Clear()
	assert.Equal(t, 0, cache.Size())
}

func TestSessionCache_ListSessions(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	sessions := cache.ListSessions()
	assert.Empty(t, sessions)

	cache.GetOrCreateSession("server-1")
	cache.GetOrCreateSession("server-2")

	sessions = cache.ListSessions()
	assert.Len(t, sessions, 2)
}

func TestSessionCache_RestoreSession(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	original := &SessionState{
		ServerName:  "restored-server",
		ClientID:   "restored-client",
		CreatedAt:  time.Now().Add(-5 * time.Minute),
		LastUsedAt: time.Now().Add(-5 * time.Minute),
	}

	err := cache.RestoreSession(original)
	require.NoError(t, err)

	session, ok := cache.GetSession("restored-server")
	require.True(t, ok)
	assert.Equal(t, original.ClientID, session.ClientID)
}

func TestSessionCache_RestoreSession_Nil(t *testing.T) {
	cache := NewSessionCache(5*time.Minute, 10)

	err := cache.RestoreSession(nil)
	require.Error(t, err)
}

func BenchmarkSessionCache_GetOrCreateSession(b *testing.B) {
	cache := NewSessionCache(5*time.Minute, 100)
	serverName := "bench-server"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cache.GetOrCreateSession(serverName)
	}
}

func BenchmarkSessionCache_GetOrCreateSession_Multiple(b *testing.B) {
	cache := NewSessionCache(5*time.Minute, 100)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		serverName := fmt.Sprintf("server-%d", i%10)
		cache.GetOrCreateSession(serverName)
	}
}