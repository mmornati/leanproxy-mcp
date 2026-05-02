package concurrent

import (
	"sync"
	"sync/atomic"
	"time"
)

type RateLimiter struct {
	mu       sync.RWMutex
	max      int
	window   time.Duration
	requests []time.Time
	blocked  int64
}

func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	if max <= 0 {
		max = 10
	}
	if window <= 0 {
		window = time.Second
	}

	rl := &RateLimiter{
		max:      max,
		window:   window,
		requests: make([]time.Time, 0, max),
	}

	go rl.cleanupLoop()

	return rl
}

func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	windowStart := now.Add(-rl.window)

	for i := 0; i < len(rl.requests); {
		if rl.requests[i].Before(windowStart) {
			rl.requests = append(rl.requests[:i], rl.requests[i+1:]...)
		} else {
			i++
		}
	}

	if len(rl.requests) >= rl.max {
		atomic.AddInt64(&rl.blocked, 1)
		return false
	}

	rl.requests = append(rl.requests, now)
	return true
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.window)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		windowStart := now.Add(-rl.window)

		filtered := make([]time.Time, 0, len(rl.requests))
		for _, t := range rl.requests {
			if !t.Before(windowStart) {
				filtered = append(filtered, t)
			}
		}
		rl.requests = filtered
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) GetUsage() (current int, max int) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return len(rl.requests), rl.max
}

func (rl *RateLimiter) GetBlockedCount() int64 {
	return atomic.LoadInt64(&rl.blocked)
}

func (rl *RateLimiter) Reset() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.requests = rl.requests[:0]
	atomic.StoreInt64(&rl.blocked, 0)
}

type RateLimiterConfig struct {
	MaxRequests int
	Window     time.Duration
}

type MultiServerRateLimiter struct {
	limiters map[string]*RateLimiter
	mu       sync.RWMutex
	config   RateLimiterConfig
}

func NewMultiServerRateLimiter(config RateLimiterConfig) *MultiServerRateLimiter {
	if config.MaxRequests <= 0 {
		config.MaxRequests = 10
	}
	if config.Window <= 0 {
		config.Window = time.Second
	}

	return &MultiServerRateLimiter{
		limiters: make(map[string]*RateLimiter),
		config:   config,
	}
}

func (m *MultiServerRateLimiter) Allow(serverName string) bool {
	limiter := m.GetLimiter(serverName)
	return limiter.Allow()
}

func (m *MultiServerRateLimiter) GetLimiter(serverName string) *RateLimiter {
	m.mu.RLock()
	limiter, exists := m.limiters[serverName]
	m.mu.RUnlock()

	if exists {
		return limiter
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if limiter, exists = m.limiters[serverName]; exists {
		return limiter
	}

	limiter = NewRateLimiter(m.config.MaxRequests, m.config.Window)
	m.limiters[serverName] = limiter
	return limiter
}

func (m *MultiServerRateLimiter) Reset(serverName string) {
	m.mu.RLock()
	limiter, exists := m.limiters[serverName]
	m.mu.RUnlock()

	if exists {
		limiter.Reset()
	}
}

func (m *MultiServerRateLimiter) ResetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, limiter := range m.limiters {
		limiter.Reset()
	}
}

func (m *MultiServerRateLimiter) GetStats(serverName string) RateLimiterStats {
	m.mu.RLock()
	limiter, exists := m.limiters[serverName]
	m.mu.RUnlock()

	if !exists {
		return RateLimiterStats{}
	}

	current, max := limiter.GetUsage()
	return RateLimiterStats{
		ServerName:     serverName,
		CurrentRequests: current,
		MaxRequests:    max,
		BlockedCount:   limiter.GetBlockedCount(),
	}
}

type RateLimiterStats struct {
	ServerName      string
	CurrentRequests int
	MaxRequests     int
	BlockedCount    int64
}

type QueueManager struct {
	queues    map[string]*RequestQueue
	mu        sync.RWMutex
	maxSize   int
	timeout   time.Duration
	overflow  int64
	overflowMu sync.RWMutex
}

type RequestQueue struct {
	items    []QueuedRequest
	mu       sync.RWMutex
	maxSize  int
	timeout  time.Duration
	enqueued int64
	dequeued int64
	timeouted int64
}

type QueuedRequest struct {
	Request    Request
	ResultCh   chan *Response
	ErrorCh    chan error
	EnqueuedAt time.Time
}

func NewQueueManager(maxSize int, timeout time.Duration) *QueueManager {
	if maxSize <= 0 {
		maxSize = 10000
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &QueueManager{
		queues:  make(map[string]*RequestQueue),
		maxSize: maxSize,
		timeout: timeout,
	}
}

func (qm *QueueManager) Enqueue(serverName string, req Request, resultCh chan *Response, errorCh chan error) error {
	q := qm.GetOrCreateQueue(serverName)

	q.mu.Lock()
	if len(q.items) >= q.maxSize {
		q.mu.Unlock()
		atomic.AddInt64(&qm.overflow, 1)
		return &ConcurrentError{Code: ErrCodeRateLimited, Message: "queue full"}
	}

	q.items = append(q.items, QueuedRequest{
		Request:    req,
		ResultCh:   resultCh,
		ErrorCh:    errorCh,
		EnqueuedAt: time.Now(),
	})
	q.enqueued++
	q.mu.Unlock()

	go qm.processQueue(serverName)

	return nil
}

func (qm *QueueManager) GetOrCreateQueue(serverName string) *RequestQueue {
	qm.mu.RLock()
	q, exists := qm.queues[serverName]
	qm.mu.RUnlock()

	if exists {
		return q
	}

	qm.mu.Lock()
	defer qm.mu.Unlock()

	if q, exists = qm.queues[serverName]; exists {
		return q
	}

	q = &RequestQueue{
		items:   make([]QueuedRequest, 0, 100),
		maxSize: qm.maxSize,
		timeout: qm.timeout,
	}
	qm.queues[serverName] = q
	return q
}

func (qm *QueueManager) processQueue(serverName string) {
	q := qm.GetOrCreateQueue(serverName)

	q.mu.Lock()
	if len(q.items) == 0 {
		q.mu.Unlock()
		return
	}

	item := q.items[0]
	q.items = q.items[1:]
	q.dequeued++
	q.mu.Unlock()

	elapsed := time.Since(item.EnqueuedAt)
	if elapsed > q.timeout {
		select {
		case item.ErrorCh <- &ConcurrentError{Code: ErrCodeTimeout, Message: "queue timeout"}:
		default:
		}
		atomic.AddInt64(&q.timeouted, 1)
		return
	}

	select {
	case item.ResultCh <- &Response{ID: item.Request.ID, Result: []byte(`{}`)}:
	default:
	}
}

func (qm *QueueManager) GetQueueSize(serverName string) int {
	qm.mu.RLock()
	q, exists := qm.queues[serverName]
	qm.mu.RUnlock()

	if !exists {
		return 0
	}

	q.mu.RLock()
	size := len(q.items)
	q.mu.RUnlock()

	return size
}

func (qm *QueueManager) GetOverflowCount() int64 {
	return atomic.LoadInt64(&qm.overflow)
}

func (qm *QueueManager) ClearQueue(serverName string) {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	if q, exists := qm.queues[serverName]; exists {
		q.mu.Lock()
		q.items = nil
		q.mu.Unlock()
	}
}