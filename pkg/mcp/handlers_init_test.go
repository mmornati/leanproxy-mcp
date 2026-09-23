package mcp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/pool"
)

// initCountingPool counts initialize handshakes; the handshake is slow so
// concurrent callers overlap.
type initCountingPool struct {
	initCalls   atomic.Int32
	initialized atomic.Bool
}

func (p *initCountingPool) SendRequestToServer(ctx context.Context, name, method string, params json.RawMessage, timeout time.Duration) (*pool.Response, error) {
	return p.SendRequestToServerWithID(ctx, name, method, params, timeout, 0)
}

func (p *initCountingPool) SendRequestToServerWithID(ctx context.Context, _ string, method string, _ json.RawMessage, _ time.Duration, _ int) (*pool.Response, error) {
	if method == MethodInitialize {
		p.initCalls.Add(1)
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return &pool.Response{Result: json.RawMessage(`{"content":[]}`)}, nil
}

func (p *initCountingPool) SendServerNotification(context.Context, string, string, map[string]interface{}) error {
	return nil
}
func (p *initCountingPool) ListServers() []string { return []string{"s"} }
func (p *initCountingPool) GetServerState(string) (pool.ServerState, error) {
	return pool.StateRunning, nil
}
func (p *initCountingPool) GetServerTransport(string) (string, error)   { return "stdio", nil }
func (p *initCountingPool) RestartServer(context.Context, string) error { return nil }
func (p *initCountingPool) IsServerMCPInitialized(string) bool          { return p.initialized.Load() }
func (p *initCountingPool) MarkServerMCPInitialized(string)             { p.initialized.Store(true) }
func (p *initCountingPool) Close() error                                { return nil }

// TestEnsureServerInitializedOnce checks that concurrent requests to a
// not-yet-initialized server perform a single MCP handshake (#292: the stdio
// front end now dispatches requests in parallel).
func TestEnsureServerInitializedOnce(t *testing.T) {
	p := &initCountingPool{}
	h := NewHandler(p, nil)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- h.ensureServerInitialized(context.Background(), "s")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("ensureServerInitialized() error = %v", err)
		}
	}
	if got := p.initCalls.Load(); got != 1 {
		t.Fatalf("initialize handshakes = %d, want 1", got)
	}
}

// TestEnsureServerInitializedHonoursContext checks that a caller waiting for
// another caller's handshake gives up when its own context ends.
func TestEnsureServerInitializedHonoursContext(t *testing.T) {
	p := &initCountingPool{}
	h := NewHandler(p, nil)

	// Hold the per-server lock as if a handshake were in progress.
	v, _ := h.initLocks.LoadOrStore("s", make(chan struct{}, 1))
	v.(chan struct{}) <- struct{}{}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.ensureServerInitialized(ctx, "s"); err == nil {
		t.Fatal("ensureServerInitialized() = nil, want context error while the lock is held")
	}
	if got := p.initCalls.Load(); got != 0 {
		t.Fatalf("initialize handshakes = %d, want 0", got)
	}
}
