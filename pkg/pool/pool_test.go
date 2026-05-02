package pool

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

func TestNewStdioPool(t *testing.T) {
	pool := NewStdioPool(5, 5*time.Minute, nil)

	if pool == nil {
		t.Fatal("expected non-nil pool")
	}

	if pool.maxPerServer != 5 {
		t.Errorf("expected maxPerServer=5, got %d", pool.maxPerServer)
	}

	if pool.idleTimeout != 5*time.Minute {
		t.Errorf("expected idleTimeout=5m, got %v", pool.idleTimeout)
	}
}

func TestStdioPoolServerLifecycle(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}

	err := pool.StartServer(ctx, config)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	count := pool.ServerCount()
	if count != 1 {
		t.Errorf("expected server count=1, got %d", count)
	}

	state, err := pool.GetServerState("test-server")
	if err != nil {
		t.Fatalf("failed to get server state: %v", err)
	}

	if state != StateIdle && state != StateRunning {
		t.Errorf("expected state to be idle or running, got %s", state)
	}

	servers := pool.ListServers()
	if len(servers) != 1 {
		t.Errorf("expected 1 server in list, got %d", len(servers))
	}
}

func TestStdioPoolStartAllServers(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	enabled := true
	disabled := false

	configs := []*migrate.ServerConfig{
		{
			Name:     "server1",
			Enabled:  &enabled,
			Transport: registry.TransportStdio,
			Stdio: &migrate.StdioConfig{
				Command: "sleep",
				Args:    []string{"100"},
			},
			TimeoutValue: 30 * time.Second,
		},
		{
			Name:     "server2",
			Enabled:  &enabled,
			Transport: registry.TransportStdio,
			Stdio: &migrate.StdioConfig{
				Command: "sleep",
				Args:    []string{"100"},
			},
			TimeoutValue: 30 * time.Second,
		},
		{
			Name:     "server3-disabled",
			Enabled:  &disabled,
			Transport: registry.TransportStdio,
			Stdio: &migrate.StdioConfig{
				Command: "sleep",
				Args:    []string{"100"},
			},
			TimeoutValue: 30 * time.Second,
		},
	}

	err := pool.StartAllServers(ctx, configs)
	if err != nil {
		t.Fatalf("StartAllServers failed: %v", err)
	}

	count := pool.ServerCount()
	if count != 2 {
		t.Errorf("expected 2 servers started, got %d", count)
	}
}

func TestStdioPoolGetServer(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	_, err := pool.GetServer("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent server")
	}

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}

	err = pool.StartServer(ctx, config)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	server, err := pool.GetServer("test-server")
	if err != nil {
		t.Fatalf("failed to get server: %v", err)
	}

	if server == nil {
		t.Fatal("expected non-nil server")
	}

	if server.config.Name != "test-server" {
		t.Errorf("expected name=test-server, got %s", server.config.Name)
	}
}

func TestStdioPoolClose(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "sleep",
			Args:    []string{"100"},
		},
		TimeoutValue: 30 * time.Second,
	}

	pool.StartServer(ctx, config)

	err := pool.Close()
	if err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	count := pool.ServerCount()
	if count != 0 {
		t.Errorf("expected 0 servers after close, got %d", count)
	}
}

func TestStdioPoolServerRestart(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}

	err := pool.StartServer(ctx, config)
	if err != nil {
		t.Fatalf("failed to start server: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	err = pool.RestartServer(ctx, "test-server")
	if err != nil {
		t.Fatalf("RestartServer failed: %v", err)
	}
}

func TestServerStateTransitions(t *testing.T) {
	server := &StdioServerV2{
		name:           "test",
		config:         StdioServerConfig{Name: "test"},
		requestCh:      make(chan Request, 5),
		state:          StateIdle,
		maxConcurrent: 5,
		logger:         slog.Default(),
	}

	if !server.isHealthy() {
		t.Error("expected server to be healthy in idle state")
	}

	server.mu.Lock()
	server.state = StateBusy
	server.mu.Unlock()

	if !server.isHealthy() {
		t.Error("expected server to be healthy in busy state")
	}

	server.mu.Lock()
	server.state = StateError
	server.mu.Unlock()

	if server.isHealthy() {
		t.Error("expected server to not be healthy in error state")
	}
}

func TestServerCanAcceptRequest(t *testing.T) {
	server := &StdioServerV2{
		name:           "test",
		config:         StdioServerConfig{Name: "test", MaxConcurrent: 3},
		requestCh:      make(chan Request, 6),
		state:          StateIdle,
		maxConcurrent:  3,
		currentLoad:    0,
		logger:         slog.Default(),
	}

	if !server.canAcceptRequest() {
		t.Error("expected server to accept request when idle")
	}

	server.mu.Lock()
	server.currentLoad = 3
	server.mu.Unlock()

	if server.canAcceptRequest() {
		t.Error("expected server to not accept request when at max")
	}
}

func TestRequestQueue(t *testing.T) {
	queue := NewRequestQueue(5, 30*time.Second, nil)

	if queue.IsFull() {
		t.Error("expected queue to not be full initially")
	}

	if !queue.IsEmpty() {
		t.Error("expected queue to be empty initially")
	}

	req := Request{
		Method:  "test",
		Params:  nil,
		ID:      1,
		Timeout: 30 * time.Second,
	}

	if !queue.Enqueue(req) {
		t.Error("expected enqueue to succeed")
	}

	if queue.IsEmpty() {
		t.Error("expected queue to not be empty after enqueue")
	}

	if queue.Size() != 1 {
		t.Errorf("expected size=1, got %d", queue.Size())
	}
}

func TestServerQueue(t *testing.T) {
	sq := NewServerQueue("test", 3, 30*time.Second, nil)

	for i := 0; i < 3; i++ {
		if !sq.Acquire(1 * time.Second) {
			t.Errorf("expected acquire %d to succeed (within limit)", i+1)
		}
	}

	if sq.Acquire(1 * time.Second) {
		t.Error("expected acquire to fail when at capacity")
	}

	sq.Release()

	if !sq.Acquire(1 * time.Second) {
		t.Error("expected acquire to succeed after release")
	}
}

func TestPoolQueueManager(t *testing.T) {
	qm := NewPoolQueueManager(nil)

	queue1 := qm.GetOrCreateQueue("server1", 5, 30*time.Second)
	queue2 := qm.GetOrCreateQueue("server1", 5, 30*time.Second)

	if queue1 != queue2 {
		t.Error("expected same queue for same name")
	}

	queue3 := qm.GetOrCreateQueue("server2", 5, 30*time.Second)
	if queue3 == queue1 {
		t.Error("expected different queue for different name")
	}

	queues := qm.ListQueues()
	if len(queues) != 2 {
		t.Errorf("expected 2 queues, got %d", len(queues))
	}
}

func TestHealthChecker(t *testing.T) {
	pool := NewStdioPool(5, 5*time.Minute, nil)

	hc := NewHealthChecker(pool, nil)

	health, _ := hc.GetServerHealth("nonexistent")
	if health != HealthUnknown {
		t.Errorf("expected unknown health for nonexistent server, got %s", health)
	}

	hc.RegisterServer("test-server")
	health, _ = hc.GetServerHealth("test-server")
	if health != "" {
		t.Errorf("expected empty health for registered but not checked server, got %s", health)
	}
}

func TestHealthCheckerServerNotFound(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	hc := NewHealthChecker(pool, nil)

	result := hc.CheckServer(ctx, "nonexistent")
	if result.Status != HealthUnhealthy {
		t.Errorf("expected unhealthy status, got %s", result.Status)
	}
}

func TestServerStats(t *testing.T) {
	server := &StdioServerV2{
		name:   "test",
		config: StdioServerConfig{Name: "test"},
		stats:  ServerStats{},
		logger: slog.Default(),
	}

	server.mu.Lock()
	server.stats.RequestCount = 10
	server.stats.ErrorCount = 2
	server.stats.AvgLatencyMs = 50.5
	server.mu.Unlock()

	stats := server.getStats()
	if stats.RequestCount != 10 {
		t.Errorf("expected request count=10, got %d", stats.RequestCount)
	}
	if stats.ErrorCount != 2 {
		t.Errorf("expected error count=2, got %d", stats.ErrorCount)
	}
}

func TestConcurrentRequests(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(2, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}

	pool.StartServer(ctx, config)

	var wg sync.WaitGroup
	requestCount := 3

	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			req := Request{
				Method:  "test",
				Params:  nil,
				ID:      id,
				Timeout: 2 * time.Second,
				ResultCh: make(chan *Response, 1),
			}
			err := pool.PutRequest("test-server", req)
			if err != nil {
				t.Logf("PutRequest failed for id %d: %v", id, err)
			}
		}(i)
	}

	wg.Wait()
}

func TestServerGetState(t *testing.T) {
	server := &StdioServerV2{
		name:   "test",
		config: StdioServerConfig{Name: "test"},
		state:  StateRunning,
		logger: slog.Default(),
	}

	state := server.getState()
	if state != StateRunning {
		t.Errorf("expected state=running, got %s", state)
	}
}

func TestServerEnqueueRequest(t *testing.T) {
	server := &StdioServerV2{
		name:           "test",
		config:         StdioServerConfig{Name: "test", MaxConcurrent: 2},
		requestCh:      make(chan Request, 4),
		state:          StateIdle,
		maxConcurrent:  2,
		currentLoad:    0,
		logger:         slog.Default(),
	}

	req := Request{
		Method:  "test",
		Params:  nil,
		ID:      1,
		Timeout: 30 * time.Second,
	}

	if !server.enqueueRequest(req) {
		t.Error("expected enqueue to succeed")
	}

	server.mu.Lock()
	if server.currentLoad != 1 {
		t.Errorf("expected currentLoad=1, got %d", server.currentLoad)
	}
	server.mu.Unlock()
}

func TestPoolGetServerStats(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}

	pool.StartServer(ctx, config)

	stats, err := pool.GetServerStats("test-server")
	if err != nil {
		t.Fatalf("GetServerStats failed: %v", err)
	}

	if stats.RequestCount != 0 {
		t.Errorf("expected request count=0, got %d", stats.RequestCount)
	}
}

func TestPoolStopServer(t *testing.T) {
	ctx := context.Background()
	pool := NewStdioPool(5, 5*time.Minute, nil)
	defer pool.Close()

	config := &migrate.ServerConfig{
		Name: "test-server",
		Transport: registry.TransportStdio,
		Stdio: &migrate.StdioConfig{
			Command: "cat",
			Args:    []string{},
		},
		TimeoutValue: 30 * time.Second,
	}

	pool.StartServer(ctx, config)

	err := pool.StopServer("test-server")
	if err != nil {
		t.Fatalf("StopServer failed: %v", err)
	}

	_, err = pool.GetServer("test-server")
	if err == nil {
		t.Error("expected error for stopped server")
	}
}