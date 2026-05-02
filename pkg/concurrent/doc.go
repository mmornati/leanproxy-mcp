package concurrent

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type RequestHandler interface {
	Handle(ctx context.Context, req *Request) (*Response, error)
}

type Request struct {
	Method     string          `json:"method"`
	Params     json.RawMessage `json:"params,omitempty"`
	ID         interface{}     `json:"id"`
	ServerName string          `json:"server_name"`
	Timeout    time.Duration
	ResultCh   chan *Response
	ErrorCh    chan error
}

type Response struct {
	Result json.RawMessage  `json:"result,omitempty"`
	Error  *ConcurrentError `json:"error,omitempty"`
	ID     interface{}      `json:"id"`
}

type ConcurrentError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *ConcurrentError) Error() string {
	return e.Message
}

const (
	ErrCodeInternalError = -32603
	ErrCodeTimeout       = -32001
	ErrCodeCircuitOpen   = -32002
	ErrCodeRateLimited   = -32003
)

type ServerState string

const (
	StateIdle     ServerState = "idle"
	StateRunning  ServerState = "running"
	StateBusy     ServerState = "busy"
	StateStopping ServerState = "stopping"
	StateStopped  ServerState = "stopped"
	StateError    ServerState = "error"
)

type PoolConfig struct {
	MaxConcurrent   int
	MaxQueueSize    int
	QueueTimeout    time.Duration
	WorkerCount     int
	BatchWindowMs   int
	RateLimitMax    int
	RateLimitWindow time.Duration
}

type StdioPool struct {
	servers        map[string]*ServerHandle
	mu             sync.RWMutex
	config         PoolConfig
	logger         *slog.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	workerPool     *WorkerPool
	circuitBreakers map[string]*CircuitBreaker
	rateLimiters   map[string]*RateLimiter
	serverStates   map[string]ServerState
	stats          PoolStats
	statsMu        sync.RWMutex
}

type ServerHandle struct {
	Name           string
	State          ServerState
	CurrentLoad    int32
	MaxConcurrent  int
	RequestCount   int64
	ErrorCount     int64
	AvgLatencyMs   float64
	LastRequestAt  time.Time
	RestartCount   int
	CurrentBackoff  time.Duration
}

type PoolStats struct {
	TotalRequests   int64
	ActiveRequests  int64
	QueuedRequests  int64
	FailedRequests  int64
	SuccessRequests int64
	AverageLatency  time.Duration
}

func NewStdioPool(config PoolConfig, logger *slog.Logger) *StdioPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &StdioPool{
		servers:         make(map[string]*ServerHandle),
		config:          config,
		logger:          logger,
		ctx:             ctx,
		cancel:          cancel,
		circuitBreakers: make(map[string]*CircuitBreaker),
		rateLimiters:    make(map[string]*RateLimiter),
		serverStates:    make(map[string]ServerState),
	}

	if config.WorkerCount > 0 {
		pool.workerPool = NewWorkerPool(config.WorkerCount, config.MaxQueueSize, logger)
	}

	return pool
}

func (p *StdioPool) RegisterServer(name string, maxConcurrent int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.servers[name] = &ServerHandle{
		Name:          name,
		State:         StateIdle,
		MaxConcurrent: maxConcurrent,
	}
	p.circuitBreakers[name] = NewCircuitBreaker(5, 50*time.Second, 10*time.Second)
	if p.config.RateLimitMax > 0 {
		p.rateLimiters[name] = NewRateLimiter(p.config.RateLimitMax, p.config.RateLimitWindow)
	} else {
		p.rateLimiters[name] = NewRateLimiter(10, time.Second)
	}
	p.serverStates[name] = StateIdle
}

func (p *StdioPool) SendRequest(ctx context.Context, serverName string, req *Request) (*Response, error) {
	p.mu.RLock()
	_, exists := p.servers[serverName]
	p.mu.RUnlock()

	if !exists {
		return nil, &ConcurrentError{Code: ErrCodeInternalError, Message: "server not found"}
	}

	cb := p.getCircuitBreaker(serverName)
	if cb.State() == StateOpen {
		return nil, &ConcurrentError{Code: ErrCodeCircuitOpen, Message: "circuit breaker open"}
	}

	rl := p.getRateLimiter(serverName)
	if !rl.Allow() {
		return nil, &ConcurrentError{Code: ErrCodeRateLimited, Message: "rate limit exceeded"}
	}

	atomic.AddInt64(&p.stats.TotalRequests, 1)
	atomic.AddInt64(&p.stats.ActiveRequests, 1)
	defer atomic.AddInt64(&p.stats.ActiveRequests, -1)

	startTime := time.Now()
	resultCh := make(chan *Response, 1)
	errorCh := make(chan error, 1)

	timeout := req.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}

	poolReq := Request{
		Method:     req.Method,
		Params:     req.Params,
		ID:         req.ID,
		ServerName: serverName,
		Timeout:    timeout,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
	}

	if p.workerPool != nil {
		if err := p.workerPool.Submit(poolReq, resultCh, errorCh); err != nil {
			atomic.AddInt64(&p.stats.FailedRequests, 1)
			cb.RecordFailure()
			return nil, err
		}
	} else {
		go p.executeRequest(serverName, poolReq, resultCh, errorCh)
	}

	select {
	case resp := <-resultCh:
		latency := time.Since(startTime)
		p.recordSuccess(serverName, latency)
		return resp, nil
	case err := <-errorCh:
		atomic.AddInt64(&p.stats.FailedRequests, 1)
		cb.RecordFailure()
		return nil, err
	case <-time.After(timeout):
		atomic.AddInt64(&p.stats.FailedRequests, 1)
		cb.RecordFailure()
		return nil, &ConcurrentError{Code: ErrCodeTimeout, Message: "request timeout"}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (p *StdioPool) executeRequest(serverName string, req Request, resultCh chan *Response, errorCh chan error) {
	atomic.AddInt64(&p.stats.ActiveRequests, 1)
	defer atomic.AddInt64(&p.stats.ActiveRequests, -1)

	p.mu.RLock()
	server := p.servers[serverName]
	p.mu.RUnlock()

	if server == nil {
		errorCh <- &ConcurrentError{Code: ErrCodeInternalError, Message: "server not found"}
		return
	}

	atomic.AddInt32(&server.CurrentLoad, 1)
	defer atomic.AddInt32(&server.CurrentLoad, -1)

	p.mu.Lock()
	if p.serverStates[serverName] != StateStopping {
		p.serverStates[serverName] = StateBusy
	}
	p.mu.Unlock()

	result := &Response{ID: req.ID}
	_, err := p.forwardToServer(serverName, req)

	if err != nil {
		result.Error = &ConcurrentError{Code: ErrCodeInternalError, Message: err.Error()}
		atomic.AddInt64(&server.ErrorCount, 1)
	} else {
		result.Result = json.RawMessage(`{}`)
		atomic.AddInt64(&server.RequestCount, 1)
	}

	p.mu.Lock()
	if p.serverStates[serverName] != StateStopping {
		p.serverStates[serverName] = StateIdle
	}
	p.mu.Unlock()

	select {
	case resultCh <- result:
	default:
	}
	if err != nil {
		select {
		case errorCh <- err:
		default:
		}
	}
}

func (p *StdioPool) forwardToServer(serverName string, req Request) (json.RawMessage, error) {
	time.Sleep(1 * time.Millisecond)
	return nil, nil
}

func (p *StdioPool) recordSuccess(serverName string, latency time.Duration) {
	p.mu.RLock()
	server := p.servers[serverName]
	p.mu.RUnlock()

	if server != nil {
		atomic.AddInt64(&p.stats.SuccessRequests, 1)
		server.LastRequestAt = time.Now()

		count := atomic.LoadInt64(&server.RequestCount)
		if count > 0 {
			avgLatency := server.AvgLatencyMs
			newLatency := float64(latency.Milliseconds())
			server.AvgLatencyMs = (avgLatency*float64(count-1) + newLatency) / float64(count)
		}
	}

	p.statsMu.Lock()
	successCount := p.stats.SuccessRequests
	if successCount > 0 {
		totalLatency := p.stats.AverageLatency * time.Duration(successCount-1)
		p.stats.AverageLatency = (totalLatency + latency) / time.Duration(successCount)
	} else {
		p.stats.AverageLatency = latency
	}
	p.statsMu.Unlock()
}

func (p *StdioPool) getCircuitBreaker(serverName string) *CircuitBreaker {
	p.mu.RLock()
	cb, exists := p.circuitBreakers[serverName]
	p.mu.RUnlock()

	if !exists {
		return NewCircuitBreaker(5, 50*time.Second, 10*time.Second)
	}
	return cb
}

func (p *StdioPool) getRateLimiter(serverName string) *RateLimiter {
	p.mu.RLock()
	rl, exists := p.rateLimiters[serverName]
	p.mu.RUnlock()

	if !exists {
		return NewRateLimiter(10, time.Second)
	}
	return rl
}

func (p *StdioPool) GetServerStats(serverName string) (*ServerHandle, error) {
	p.mu.RLock()
	_, exists := p.servers[serverName]
	p.mu.RUnlock()

	if !exists {
		return nil, &ConcurrentError{Code: ErrCodeInternalError, Message: "server not found"}
	}

	stats := &ServerHandle{
		Name: serverName,
	}
	p.mu.RLock()
	if s, ok := p.servers[serverName]; ok {
		stats = s
	}
	p.mu.RUnlock()

	return stats, nil
}

func (p *StdioPool) GetPoolStats() PoolStats {
	p.statsMu.RLock()
	stats := p.stats
	p.statsMu.RUnlock()

	stats.QueuedRequests = int64(p.workerPool.QueueSize())
	return stats
}

func (p *StdioPool) Close() error {
	p.cancel()

	if p.workerPool != nil {
		p.workerPool.Shutdown()
	}

	p.mu.Lock()
	for name := range p.servers {
		p.serverStates[name] = StateStopped
	}
	p.mu.Unlock()

	return nil
}

func (p *StdioPool) ServerCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.servers)
}