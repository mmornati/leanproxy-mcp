package pool

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type queuedRequest struct {
	req      Request
	enqueued time.Time
	waitCh   chan<- bool
}

type RequestQueue struct {
	queue      chan queuedRequest
	maxSize    int
	timeout    time.Duration
	mu         sync.Mutex
	activeCount int
	logger     *slog.Logger
}

func NewRequestQueue(maxSize int, timeout time.Duration, logger *slog.Logger) *RequestQueue {
	if logger == nil {
		logger = slog.Default()
	}

	return &RequestQueue{
		queue:   make(chan queuedRequest, maxSize),
		maxSize: maxSize,
		timeout: timeout,
		logger:  logger,
	}
}

func (q *RequestQueue) Enqueue(req Request) bool {
	q.mu.Lock()
	select {
	case q.queue <- queuedRequest{req: req, enqueued: time.Now()}:
		q.mu.Unlock()
		return true
	default:
		q.mu.Unlock()
		q.logger.Warn("queue full, rejecting request", "maxSize", q.maxSize)
		return false
	}
}

func (q *RequestQueue) Dequeue(ctx context.Context) (Request, bool) {
	select {
	case qr := <-q.queue:
		return qr.req, true
	case <-ctx.Done():
		return Request{}, false
	default:
		return Request{}, false
	}
}

func (q *RequestQueue) Size() int {
	return len(q.queue)
}

func (q *RequestQueue) IsFull() bool {
	return len(q.queue) >= q.maxSize
}

func (q *RequestQueue) IsEmpty() bool {
	return len(q.queue) == 0
}

type ServerQueue struct {
	name       string
	queue      *RequestQueue
	maxConcurrent int
	active     int
	mu         sync.Mutex
	logger     *slog.Logger
}

func NewServerQueue(name string, maxConcurrent int, queueTimeout time.Duration, logger *slog.Logger) *ServerQueue {
	if logger == nil {
		logger = slog.Default()
	}

	return &ServerQueue{
		name:          name,
		queue:         NewRequestQueue(maxConcurrent*2, queueTimeout, logger),
		maxConcurrent: maxConcurrent,
		logger:        logger,
	}
}

func (sq *ServerQueue) Acquire(timeout time.Duration) bool {
	sq.mu.Lock()
	if sq.active >= sq.maxConcurrent {
		sq.mu.Unlock()
		return false
	}
	sq.active++
	sq.mu.Unlock()
	return true
}

func (sq *ServerQueue) Release() {
	sq.mu.Lock()
	sq.active--
	if sq.active < 0 {
		sq.active = 0
	}
	sq.mu.Unlock()
}

func (sq *ServerQueue) Enqueue(req Request) bool {
	return sq.queue.Enqueue(req)
}

func (sq *ServerQueue) Dequeue(ctx context.Context) (Request, bool) {
	return sq.queue.Dequeue(ctx)
}

func (sq *ServerQueue) PendingCount() int {
	return sq.queue.Size()
}

func (sq *ServerQueue) IsAtCapacity() bool {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return sq.active >= sq.maxConcurrent && sq.queue.IsFull()
}

type PoolQueueManager struct {
	queues   map[string]*ServerQueue
	mu       sync.RWMutex
	logger   *slog.Logger
}

func NewPoolQueueManager(logger *slog.Logger) *PoolQueueManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &PoolQueueManager{
		queues: make(map[string]*ServerQueue),
		logger: logger,
	}
}

func (qm *PoolQueueManager) GetOrCreateQueue(name string, maxConcurrent int, queueTimeout time.Duration) *ServerQueue {
	qm.mu.Lock()
	defer qm.mu.Unlock()

	if queue, exists := qm.queues[name]; exists {
		return queue
	}

	queue := NewServerQueue(name, maxConcurrent, queueTimeout, qm.logger)
	qm.queues[name] = queue
	return queue
}

func (qm *PoolQueueManager) RemoveQueue(name string) {
	qm.mu.Lock()
	defer qm.mu.Unlock()
	delete(qm.queues, name)
}

func (qm *PoolQueueManager) GetQueueStats(name string) (active int, pending int, atCapacity bool) {
	qm.mu.RLock()
	queue, exists := qm.queues[name]
	qm.mu.RUnlock()

	if !exists {
		return 0, 0, true
	}

	queue.mu.Lock()
	active = queue.active
	queue.mu.Unlock()

	pending = queue.PendingCount()
	atCapacity = queue.IsAtCapacity()

	return active, pending, atCapacity
}

func (qm *PoolQueueManager) ListQueues() []string {
	qm.mu.RLock()
	defer qm.mu.RUnlock()

	names := make([]string, 0, len(qm.queues))
	for name := range qm.queues {
		names = append(names, name)
	}
	return names
}