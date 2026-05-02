package concurrent

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type BatchConfig struct {
	WindowMs       int
	MaxBatchSize   int
	MaxWaitTime    time.Duration
	EnableBatching bool
}

type BatchRequest struct {
	Request  Request
	ResultCh chan *Response
	ErrorCh  chan error
	Arrived  time.Time
}

type Batcher struct {
	config    BatchConfig
	logger    *slog.Logger
	batches   map[string][]BatchRequest
	batchMu   sync.RWMutex
	flushCh   chan string
	doneCh    chan struct{}
	window    time.Duration
}

func NewBatcher(config BatchConfig, logger *slog.Logger) *Batcher {
	if logger == nil {
		logger = slog.Default()
	}

	if config.WindowMs <= 0 {
		config.WindowMs = 10
	}

	if config.MaxBatchSize <= 0 {
		config.MaxBatchSize = 100
	}

	if config.MaxWaitTime <= 0 {
		config.MaxWaitTime = 100 * time.Millisecond
	}

	batcher := &Batcher{
		config:    config,
		logger:    logger,
		batches:   make(map[string][]BatchRequest),
		flushCh:   make(chan string, 100),
		doneCh:    make(chan struct{}),
		window:    time.Duration(config.WindowMs) * time.Millisecond,
	}

	if config.EnableBatching {
		go batcher.batchLoop()
	}

	return batcher
}

func (b *Batcher) AddRequest(serverName string, req Request, resultCh chan *Response, errorCh chan error) bool {
	if !b.config.EnableBatching {
		return false
	}

	b.batchMu.Lock()
	defer b.batchMu.Unlock()

	batch, exists := b.batches[serverName]
	if !exists {
		b.batches[serverName] = []BatchRequest{
			{Request: req, ResultCh: resultCh, ErrorCh: errorCh, Arrived: time.Now()},
		}
		return true
	}

	if len(batch) >= b.config.MaxBatchSize {
		return false
	}

	batch = append(batch, BatchRequest{Request: req, ResultCh: resultCh, ErrorCh: errorCh, Arrived: time.Now()})
	b.batches[serverName] = batch

	if len(batch) >= b.config.MaxBatchSize {
		b.processBatchLocked(serverName)
	}

	return true
}

func (b *Batcher) processBatchLocked(serverName string) {
	batch, exists := b.batches[serverName]
	if !exists || len(batch) == 0 {
		return
	}

	delete(b.batches, serverName)

	b.logger.Debug("processing batch", "server", serverName, "size", len(batch))

	for _, item := range batch {
		select {
		case item.ResultCh <- &Response{ID: item.Request.ID, Result: []byte(`{}`)}:
		default:
		}
	}
}

func (b *Batcher) batchLoop() {
	for {
		select {
		case serverName := <-b.flushCh:
			b.Flush(serverName)
		case <-b.doneCh:
			return
		}
	}
}

func (b *Batcher) Flush(serverName string) {
	b.batchMu.Lock()
	batch, exists := b.batches[serverName]
	if !exists || len(batch) == 0 {
		b.batchMu.Unlock()
		return
	}

	delete(b.batches, serverName)
	b.batchMu.Unlock()

	b.processBatch(serverName, batch)
}

func (b *Batcher) flushServer(serverName string) {
	b.batchMu.Lock()
	batch, exists := b.batches[serverName]
	if !exists || len(batch) == 0 {
		b.batchMu.Unlock()
		return
	}

	delete(b.batches, serverName)
	b.batchMu.Unlock()

	b.processBatch(serverName, batch)
}

func (b *Batcher) processBatch(serverName string, batch []BatchRequest) {
	if len(batch) == 0 {
		return
	}

	b.logger.Debug("processing batch", "server", serverName, "size", len(batch))

	for _, item := range batch {
		select {
		case item.ResultCh <- &Response{ID: item.Request.ID, Result: []byte(`{}`)}:
		default:
		}
	}
}

func (b *Batcher) GetPendingCount(serverName string) int {
	b.batchMu.RLock()
	defer b.batchMu.RUnlock()

	batch, exists := b.batches[serverName]
	if !exists {
		return 0
	}
	return len(batch)
}

func (b *Batcher) Close() {
	close(b.doneCh)
}

type BatchProcessor interface {
	ProcessBatch(ctx context.Context, serverName string, requests []Request) []Response
}

type DefaultBatchProcessor struct {
	logger *slog.Logger
}

func NewDefaultBatchProcessor(logger *slog.Logger) *DefaultBatchProcessor {
	if logger == nil {
		logger = slog.Default()
	}
	return &DefaultBatchProcessor{logger: logger}
}

func (p *DefaultBatchProcessor) ProcessBatch(ctx context.Context, serverName string, requests []Request) []Response {
	responses := make([]Response, 0, len(requests))

	for _, req := range requests {
		resp := Response{ID: req.ID, Result: []byte(`{}`)}
		responses = append(responses, resp)
	}

	return responses
}