package embedder

import (
	"context"
	"log/slog"
	"sync"
)

const (
	defaultPoolSize  = 4
	defaultPoolQueue = 256
)

type PoolConfig struct {
	Size  int `yaml:"size"`
	Queue int `yaml:"queue"`
}

type job struct {
	ctx    context.Context
	req    EmbedRequest
	result chan Embedding
	err    chan error
}

type Pool struct {
	embedder Embedder
	jobs     chan job
	wg       sync.WaitGroup
	quit     chan struct{}
	logger   *slog.Logger
}

func NewPool(embedder Embedder, cfg PoolConfig, logger *slog.Logger) *Pool {
	if cfg.Size <= 0 {
		cfg.Size = defaultPoolSize
	}
	if cfg.Queue <= 0 {
		cfg.Queue = defaultPoolQueue
	}
	if logger == nil {
		logger = slog.Default()
	}

	p := &Pool{
		embedder: embedder,
		jobs:     make(chan job, cfg.Queue),
		quit:     make(chan struct{}),
		logger:   logger,
	}

	for i := 0; i < cfg.Size; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	return p
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()
	p.logger.Debug("embedder pool worker started", "worker_id", id)
	for {
		select {
		case j := <-p.jobs:
			emb, err := p.embedder.Embed(j.ctx, j.req)
			if err != nil {
				j.err <- err
			} else {
				j.result <- emb
			}
		case <-p.quit:
			p.logger.Debug("embedder pool worker stopped", "worker_id", id)
			return
		}
	}
}

func (p *Pool) Embed(ctx context.Context, req EmbedRequest) (<-chan Embedding, <-chan error) {
	j := job{
		ctx:    ctx,
		req:    req,
		result: make(chan Embedding, 1),
		err:    make(chan error, 1),
	}
	select {
	case p.jobs <- j:
	default:
		err := make(chan error, 1)
		err <- ErrPoolFull
		close(err)
		result := make(chan Embedding, 1)
		close(result)
		return result, err
	}
	return j.result, j.err
}

var ErrPoolFull = &poolFullError{}

type poolFullError struct{}

func (e *poolFullError) Error() string {
	return "embedder pool queue full"
}

func (e *poolFullError) Unwrap() error {
	return nil
}

func (p *Pool) Close() error {
	select {
	case <-p.quit:
		return nil
	default:
		close(p.quit)
	}
	p.wg.Wait()
	return p.embedder.Close()
}
