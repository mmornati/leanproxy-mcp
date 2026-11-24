package mcp

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshBackoff(t *testing.T) {
	base := DefaultToolRefreshRetryInterval
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		assert.Equal(t, w, refreshBackoff(base, i+1), "failure %d", i+1)
	}
	assert.Equal(t, MaxToolRefreshRetryInterval, refreshBackoff(base, 1000), "no overflow")
	assert.Equal(t, time.Hour, refreshBackoff(time.Hour, 3), "a base above the cap is kept")
}

// TestToolRefreshBackoffAndStatus: a server that keeps failing is retried
// with a growing delay and warned about once, not on every attempt; an
// explicit refresh restarts the backoff and a success clears it.
func TestToolRefreshBackoffAndStatus(t *testing.T) {
	const base = time.Minute
	p := newLifecyclePool("auth")
	p.hung["auth"] = true
	var buf bytes.Buffer
	var mu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &buf, mu: &mu}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	h := NewHandler(p, logger)
	h.SetTimeout("auth", 10*time.Millisecond)
	h.SetToolRefreshRetryInterval(base)
	logged := func(level string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, line := range strings.Split(buf.String(), "\n") {
			if strings.Contains(line, "level="+level) && strings.Contains(line, `msg="failed to refresh tools"`) {
				n++
			}
		}
		return n
	}
	periodic := func() {
		t.Helper()
		select {
		case <-h.startRefreshWith("auth", false, false).done:
		case <-time.After(5 * time.Second):
			t.Fatal("refresh did not finish")
		}
	}

	assert.Equal(t, ToolRefreshStatus{}, h.ToolRefreshStatus("auth"), "never refreshed")

	// Three periodic retries fail: 1m, 2m, then 4m before the next one.
	for i := 1; i <= 3; i++ {
		periodic()
		st := h.ToolRefreshStatus("auth")
		assert.Equal(t, i, st.Failures)
		assert.Contains(t, st.LastError, "timeout")
		assert.False(t, st.LastErrorAt.IsZero())
		assert.True(t, st.LastSuccessAt.IsZero())
		assert.Equal(t, refreshBackoff(base, i), st.NextRetryAt.Sub(st.LastErrorAt))
	}
	st := h.ToolRefreshStatus("auth")
	assert.Equal(t, 4*base, st.NextRetryAt.Sub(st.LastErrorAt))
	assert.False(t, h.retryDue("auth", st.LastErrorAt.Add(base), base), "the retry loop skips the server until its backoff ends")
	assert.True(t, h.retryDue("auth", st.NextRetryAt, base))
	assert.True(t, h.retryDue("other", time.Now(), base), "a server never refreshed is due")
	assert.Equal(t, 1, logged("WARN"), "the same error is warned about once")
	assert.Equal(t, 2, logged("DEBUG"))

	// An explicit refresh (a caller, a server event) restarts the backoff.
	require.Error(t, h.RefreshServerTools(context.Background(), "auth"))
	st = h.ToolRefreshStatus("auth")
	assert.Equal(t, 4, st.Failures)
	assert.Equal(t, base, st.NextRetryAt.Sub(st.LastErrorAt))
	assert.Equal(t, 1, logged("WARN"))

	// Success clears the failure count and the pending retry; the last
	// error stays as history.
	p.mu.Lock()
	p.hung["auth"] = false
	p.mu.Unlock()
	p.setTools("auth", "x")
	require.NoError(t, h.RefreshServerTools(context.Background(), "auth"))
	st = h.ToolRefreshStatus("auth")
	assert.Equal(t, 0, st.Failures)
	assert.True(t, st.NextRetryAt.IsZero())
	assert.False(t, st.LastSuccessAt.Before(st.LastErrorAt))
	assert.NotEmpty(t, st.LastError)

	// A failure after a success is a first failure again: warned about,
	// with the shortest delay.
	p.mu.Lock()
	p.hung["auth"] = true
	p.mu.Unlock()
	periodic()
	st = h.ToolRefreshStatus("auth")
	assert.Equal(t, 1, st.Failures)
	assert.Equal(t, base, st.NextRetryAt.Sub(st.LastErrorAt))
	assert.Equal(t, 2, logged("WARN"))
}
