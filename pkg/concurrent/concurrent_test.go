package concurrent

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func NewTestLogger() *slog.Logger {
	return slog.Default()
}

func TestWorkerPoolSubmit(t *testing.T) {
	pool := NewWorkerPool(4, 100, NewTestLogger())
	defer pool.Shutdown()

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	req := Request{
		Method:     "test_method",
		ServerName: "test_server",
		ID:         1,
		Timeout:    5 * time.Second,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
	}

	err := pool.Submit(req, resultCh, errorCh)
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	select {
	case resp := <-resultCh:
		if resp == nil {
			t.Error("Received nil response")
		}
		if resp.ID != 1 {
			t.Errorf("Expected ID 1, got %v", resp.ID)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for response")
	}
}

func TestWorkerPoolQueueFull(t *testing.T) {
	pool := NewWorkerPool(1, 2, NewTestLogger())
	defer pool.Shutdown()

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	for i := 0; i < 2; i++ {
		req := Request{
			Method:     "test_method",
			ServerName: "test_server",
			ID:         i,
			Timeout:    5 * time.Second,
			ResultCh:   resultCh,
			ErrorCh:    errorCh,
		}
		err := pool.Submit(req, resultCh, errorCh)
		if err != nil {
			t.Fatalf("Submit %d failed: %v", i, err)
		}
	}

	req3 := Request{
		Method:     "test_method",
		ServerName: "test_server",
		ID:         3,
		Timeout:    5 * time.Second,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
	}
	err := pool.Submit(req3, resultCh, errorCh)
	if err == nil {
		t.Error("Expected error when queue is full")
	}
}

func TestWorkerPoolMetrics(t *testing.T) {
	pool := NewWorkerPool(2, 50, NewTestLogger())
	defer pool.Shutdown()

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	for i := 0; i < 5; i++ {
		req := Request{
			Method:     "test_method",
			ServerName: "test_server",
			ID:         i,
			Timeout:    5 * time.Second,
			ResultCh:   resultCh,
			ErrorCh:    errorCh,
		}
		pool.Submit(req, resultCh, errorCh)
	}

	time.Sleep(100 * time.Millisecond)

	metrics := pool.Metrics()
	if metrics.SubmittedTasks < 5 {
		t.Errorf("Expected at least 5 submitted tasks, got %d", metrics.SubmittedTasks)
	}
}

func TestCircuitBreakerClosedState(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Second, 10*time.Second)

	if cb.State() != StateClosed {
		t.Errorf("Expected state closed, got %v", cb.State())
	}

	if !cb.Allow() {
		t.Error("Expected Allow() to return true in closed state")
	}
}

func TestCircuitBreakerOpenAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Second, 10*time.Second)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	if cb.State() != StateOpen {
		t.Errorf("Expected state open after 3 failures, got %v", cb.State())
	}

	if cb.Allow() {
		t.Error("Expected Allow() to return false in open state")
	}
}

func TestCircuitBreakerHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond, 10*time.Second)

	for i := 0; i < 2; i++ {
		cb.RecordFailure()
	}

	time.Sleep(100 * time.Millisecond)

	state := cb.State()
	if state != StateHalfOpen {
		t.Errorf("Expected state half-open after cooldown, got %v", state)
	}
}

func TestCircuitBreakerSuccessCloses(t *testing.T) {
	cb := NewCircuitBreaker(2, 50*time.Millisecond, 10*time.Second)

	for i := 0; i < 2; i++ {
		cb.RecordFailure()
	}

	time.Sleep(100 * time.Millisecond)

	cb.State()

	cb.RecordSuccess()
	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Errorf("Expected state closed after successful requests, got %v", cb.State())
	}
}

func TestCircuitBreakerReset(t *testing.T) {
	cb := NewCircuitBreaker(3, 50*time.Second, 10*time.Second)

	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}

	cb.Reset()

	if cb.State() != StateClosed {
		t.Errorf("Expected state closed after reset, got %v", cb.State())
	}
}

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(3, 100*time.Millisecond)

	for i := 0; i < 3; i++ {
		if !rl.Allow() {
			t.Errorf("Request %d should be allowed", i)
		}
	}

	if rl.Allow() {
		t.Error("Request should be blocked after reaching limit")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := NewRateLimiter(2, 50*time.Millisecond)

	rl.Allow()
	rl.Allow()

	if rl.Allow() {
		t.Error("Should be blocked immediately after reaching limit")
	}

	time.Sleep(60 * time.Millisecond)

	if !rl.Allow() {
		t.Error("Should be allowed after window passes")
	}
}

func TestRateLimiterReset(t *testing.T) {
	rl := NewRateLimiter(2, 100*time.Millisecond)

	rl.Allow()
	rl.Allow()

	rl.Reset()

	current, max := rl.GetUsage()
	if current != 0 || max != 2 {
		t.Errorf("Expected usage 0/2, got %d/%d", current, max)
	}
}

func TestMultiServerRateLimiter(t *testing.T) {
	config := RateLimiterConfig{MaxRequests: 2, Window: 100 * time.Millisecond}
	msrl := NewMultiServerRateLimiter(config)

	if !msrl.Allow("server1") {
		t.Error("server1 request should be allowed")
	}
	if !msrl.Allow("server1") {
		t.Error("server1 second request should be allowed")
	}
	if msrl.Allow("server1") {
		t.Error("Third request to server1 should be blocked")
	}

	if !msrl.Allow("server2") {
		t.Error("server2 should have its own limit")
	}
}

func TestQueueManagerEnqueue(t *testing.T) {
	qm := NewQueueManager(10, time.Second)

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	req := Request{
		Method:     "test_method",
		ServerName: "test_server",
		ID:         1,
		Timeout:    5 * time.Second,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
	}

	err := qm.Enqueue("test_server", req, resultCh, errorCh)
	if err != nil {
		t.Fatalf("Enqueue failed: %v", err)
	}

	size := qm.GetQueueSize("test_server")
	if size != 1 {
		t.Errorf("Expected queue size 1, got %d", size)
	}
}

func TestQueueManagerOverflow(t *testing.T) {
	qm := NewQueueManager(2, time.Second)

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	for i := 0; i < 2; i++ {
		req := Request{
			Method:     "test_method",
			ServerName: "test_server",
			ID:         i,
			Timeout:    5 * time.Second,
			ResultCh:   resultCh,
			ErrorCh:    errorCh,
		}
		qm.Enqueue("test_server", req, resultCh, errorCh)
	}

	req3 := Request{
		Method:     "test_method",
		ServerName: "test_server",
		ID:         3,
		Timeout:    5 * time.Second,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
	}
	err := qm.Enqueue("test_server", req3, resultCh, errorCh)
	if err == nil {
		t.Error("Expected error when queue is full")
	}

	overflow := qm.GetOverflowCount()
	if overflow != 1 {
		t.Errorf("Expected overflow count 1, got %d", overflow)
	}
}

func TestBatcherBasic(t *testing.T) {
	config := BatchConfig{
		WindowMs:       10,
		MaxBatchSize:   5,
		EnableBatching: true,
	}
	batcher := NewBatcher(config, NewTestLogger())
	defer batcher.Close()

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	req := Request{
		Method:     "test_method",
		ServerName: "test_server",
		ID:         1,
		Timeout:    5 * time.Second,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
	}

	added := batcher.AddRequest("test_server", req, resultCh, errorCh)
	if !added {
		t.Error("Request should be added to batch")
	}

	count := batcher.GetPendingCount("test_server")
	if count != 1 {
		t.Errorf("Expected pending count 1, got %d", count)
	}
}

func TestBatcherMaxBatchSize(t *testing.T) {
	config := BatchConfig{
		WindowMs:       10,
		MaxBatchSize:   2,
		EnableBatching: true,
	}
	batcher := NewBatcher(config, NewTestLogger())
	defer batcher.Close()

	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	for i := 0; i < 3; i++ {
		req := Request{
			Method:     "test_method",
			ServerName: "test_server",
			ID:         i,
			Timeout:    5 * time.Second,
			ResultCh:   resultCh,
			ErrorCh:    errorCh,
		}
		batcher.AddRequest("test_server", req, resultCh, errorCh)
	}

	count := batcher.GetPendingCount("test_server")
	if count > 2 {
		t.Errorf("Expected batch size at most 2, got %d", count)
	}
}

func TestStdioPoolRegisterServer(t *testing.T) {
	config := PoolConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  100,
		WorkerCount:   4,
	}
	pool := NewStdioPool(config, NewTestLogger())
	defer pool.Close()

	pool.RegisterServer("test_server", 5)

	stats, err := pool.GetServerStats("test_server")
	if err != nil {
		t.Fatalf("GetServerStats failed: %v", err)
	}

	if stats.Name != "test_server" {
		t.Errorf("Expected name 'test_server', got '%s'", stats.Name)
	}
	if stats.MaxConcurrent != 5 {
		t.Errorf("Expected max concurrent 5, got %d", stats.MaxConcurrent)
	}
}

func TestStdioPoolSendRequest(t *testing.T) {
	config := PoolConfig{
		MaxConcurrent: 5,
		MaxQueueSize:  100,
		WorkerCount:   2,
	}
	pool := NewStdioPool(config, NewTestLogger())
	defer pool.Close()

	pool.RegisterServer("test_server", 5)

	req := &Request{
		Method:     "test_method",
		Params:     nil,
		ID:         1,
		ServerName: "test_server",
		Timeout:    5 * time.Second,
	}

	ctx := context.Background()
	resp, err := pool.SendRequest(ctx, "test_server", req)
	if err != nil {
		t.Fatalf("SendRequest failed: %v", err)
	}

	if resp == nil {
		t.Fatal("Response is nil")
	}

	if resp.ID != 1 {
		t.Errorf("Expected ID 1, got %v", resp.ID)
	}
}

func TestStdioPoolCircuitBreaker(t *testing.T) {
	config := PoolConfig{
		MaxConcurrent: 1,
		MaxQueueSize:  10,
		WorkerCount:   1,
	}
	pool := NewStdioPool(config, NewTestLogger())
	defer pool.Close()

	pool.RegisterServer("failing_server", 1)

	cb := pool.getCircuitBreaker("failing_server")
	for i := 0; i < 6; i++ {
		cb.RecordFailure()
	}

	state := cb.State()
	if state != StateOpen {
		t.Errorf("Expected circuit open, got %v", state)
	}

	req := &Request{
		Method:     "test",
		ServerName: "failing_server",
		ID:         1,
		Timeout:    time.Second,
	}

	ctx := context.Background()
	_, err := pool.SendRequest(ctx, "failing_server", req)
	if err == nil {
		t.Error("Expected error when circuit breaker is open")
	}
}

func TestConcurrentStress(t *testing.T) {
	config := PoolConfig{
		MaxConcurrent: 10,
		MaxQueueSize:  1000,
		WorkerCount:   8,
	}
	pool := NewStdioPool(config, NewTestLogger())
	defer pool.Close()

	pool.RegisterServer("stress_server", 10)

	var wg sync.WaitGroup
	requestCount := 100

	for i := 0; i < requestCount; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			req := &Request{
				Method:     "test_method",
				ServerName: "stress_server",
				ID:         id,
				Timeout:    10 * time.Second,
			}

			ctx := context.Background()
			pool.SendRequest(ctx, "stress_server", req)
		}(i)
	}

	wg.Wait()

	time.Sleep(100 * time.Millisecond)

	stats := pool.GetPoolStats()
	if stats.TotalRequests < int64(requestCount/10) {
		t.Errorf("Expected at least %d total requests, got %d", requestCount/10, stats.TotalRequests)
	}
}

func TestCircuitBreakerGroup(t *testing.T) {
	group := NewCircuitBreakerGroup()

	cb1 := group.Get("server1")
	cb2 := group.Get("server2")

	if cb1 == cb2 {
		t.Error("Different servers should have different circuit breakers")
	}

	cb1.RecordFailure()
	cb1.RecordFailure()
	cb1.RecordFailure()

	if cb1.State() == StateOpen {
		t.Error("Single server failure should not affect other servers")
	}

	group.ResetAll()

	if cb1.State() != StateClosed {
		t.Error("ResetAll should reset all circuit breakers")
	}
}

func TestStdioPoolRateLimit(t *testing.T) {
	config := PoolConfig{
		MaxConcurrent:   5,
		MaxQueueSize:    100,
		WorkerCount:     1,
		RateLimitMax:    2,
		RateLimitWindow: 100 * time.Millisecond,
	}
	pool := NewStdioPool(config, NewTestLogger())
	defer pool.Close()

	pool.RegisterServer("rate_limited_server", 5)

	ctx := context.Background()

	for i := 0; i < 2; i++ {
		req := &Request{
			Method:     "test",
			ServerName: "rate_limited_server",
			ID:         i,
			Timeout:    time.Second,
		}
		_, err := pool.SendRequest(ctx, "rate_limited_server", req)
		if err != nil {
			t.Fatalf("Request %d failed: %v", i, err)
		}
	}

	req3 := &Request{
		Method:     "test",
		ServerName: "rate_limited_server",
		ID:         3,
		Timeout:    time.Second,
	}
	_, err := pool.SendRequest(ctx, "rate_limited_server", req3)
	if err == nil {
		t.Error("Expected rate limit error")
	}
}