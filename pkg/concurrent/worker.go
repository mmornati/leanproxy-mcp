package concurrent

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type WorkerPool struct {
	workers    int
	queueSize  int
	logger     *slog.Logger
	workCh     chan workItem
	wg         sync.WaitGroup
	ctx        context.Context
	cancel     context.CancelFunc
	shutdown   atomic.Bool
	metrics    WorkerPoolMetrics
	metricsMu  sync.RWMutex
}

type workItem struct {
	request    Request
	resultCh   chan *Response
	errorCh    chan error
	serverName string
}

type WorkerPoolMetrics struct {
	SubmittedTasks  int64
	CompletedTasks  int64
	FailedTasks     int64
	QueuedTasks     int64
	RejectedTasks   int64
	AverageWaitTime time.Duration
}

func NewWorkerPool(workers, queueSize int, logger *slog.Logger) *WorkerPool {
	if logger == nil {
		logger = slog.Default()
	}

	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		workers:   workers,
		queueSize: queueSize,
		logger:    logger,
		workCh:    make(chan workItem, queueSize),
		ctx:       ctx,
		cancel:    cancel,
	}

	for i := 0; i < workers; i++ {
		pool.wg.Add(1)
		go pool.worker(i)
	}

	pool.logger.Info("worker pool started", "workers", workers, "queue_size", queueSize)
	return pool
}

func (p *WorkerPool) worker(id int) {
	defer p.wg.Done()

	p.logger.Debug("worker started", "id", id)

	for {
		select {
		case item, ok := <-p.workCh:
			if !ok {
				p.logger.Debug("worker shutting down", "id", id)
				return
			}
			p.processWorkItem(item)

		case <-p.ctx.Done():
			return
		}
	}
}

func (p *WorkerPool) processWorkItem(item workItem) {
	atomic.AddInt64(&p.metrics.CompletedTasks, 1)

	select {
	case item.resultCh <- &Response{ID: item.request.ID, Result: []byte(`{}`)}:
	default:
	}
}

func (p *WorkerPool) Submit(req Request, resultCh chan *Response, errorCh chan error) error {
	if p.shutdown.Load() {
		return &ConcurrentError{Code: ErrCodeInternalError, Message: "pool shutdown"}
	}

	submitTime := time.Now()
	atomic.AddInt64(&p.metrics.SubmittedTasks, 1)

	item := workItem{
		request:    req,
		resultCh:   resultCh,
		errorCh:    errorCh,
		serverName: req.ServerName,
	}

	select {
	case p.workCh <- item:
		waitTime := time.Since(submitTime)
		p.updateWaitTime(waitTime)
		atomic.StoreInt64(&p.metrics.QueuedTasks, int64(len(p.workCh)))
		return nil
	default:
		atomic.AddInt64(&p.metrics.RejectedTasks, 1)
		return &ConcurrentError{Code: ErrCodeInternalError, Message: "queue full"}
	}
}

func (p *WorkerPool) updateWaitTime(waitTime time.Duration) {
	p.metricsMu.Lock()
	completed := p.metrics.CompletedTasks
	if completed > 0 {
		totalWait := p.metrics.AverageWaitTime * time.Duration(completed-1)
		p.metrics.AverageWaitTime = (totalWait + waitTime) / time.Duration(completed)
	} else {
		p.metrics.AverageWaitTime = waitTime
	}
	p.metricsMu.Unlock()
}

func (p *WorkerPool) QueueSize() int {
	return len(p.workCh)
}

func (p *WorkerPool) Metrics() WorkerPoolMetrics {
	p.metricsMu.RLock()
	metrics := p.metrics
	p.metricsMu.RUnlock()
	metrics.QueuedTasks = int64(len(p.workCh))
	return metrics
}

func (p *WorkerPool) Shutdown() {
	if p.shutdown.Swap(true) {
		return
	}

	p.logger.Info("shutting down worker pool")
	p.cancel()

	close(p.workCh)

	p.wg.Wait()
	p.logger.Info("worker pool shutdown complete")
}

func (p *WorkerPool) GetActiveWorkers() int {
	return p.workers
}

func (p *WorkerPool) GetQueueCapacity() int {
	return p.queueSize
}