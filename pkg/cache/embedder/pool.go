package embedder

import (
	"context"
	"log/slog"
	"sync"
)

const (
	defaultPoolSize  = 4
	defaultPoolQueue = 256
	maxPoolSize      = 256
	maxPoolQueue     = 4096
)

type PoolConfig struct {
	Size  int `yaml:"size"`
	Queue int `yaml:"queue"`
}

func (p PoolConfig) withDefaults() PoolConfig {
	if p.Size <= 0 {
		p.Size = defaultPoolSize
	}
	if p.Queue <= 0 {
		p.Queue = defaultPoolQueue
	}
	if p.Size > maxPoolSize {
		p.Size = maxPoolSize
	}
	if p.Queue > maxPoolQueue {
		p.Queue = maxPoolQueue
	}
	return p
}

type job struct {
	ctx    context.Context
	req    EmbedRequest
	result chan Embedding
	err    chan error
}

type Pool struct {
	embedder  Embedder
	jobs      chan job
	wg        sync.WaitGroup
	quit      chan struct{}
	closeOnce sync.Once
	logger    *slog.Logger
	size      int
}

func NewPool(embedder Embedder, cfg PoolConfig, logger *slog.Logger) *Pool {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}

	p := &Pool{
		embedder: embedder,
		jobs:     make(chan job, cfg.Queue),
		quit:     make(chan struct{}),
		logger:   logger,
		size:     cfg.Size,
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
			if err := j.ctx.Err(); err != nil {
				j.err <- err
				close(j.result)
				close(j.err)
				continue
			}
			emb, err := p.embedder.Embed(j.ctx, j.req)
			if err != nil {
				j.err <- err
			} else {
				j.result <- emb
			}
			close(j.result)
			close(j.err)
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
		return j.result, j.err
	default:
		j.err <- ErrPoolFull
		close(j.result)
		close(j.err)
		return j.result, j.err
	}
}

var ErrPoolFull = &poolFullError{}

type poolFullError struct{}

func (e *poolFullError) Error() string {
	return "embedder pool queue full"
}

func (e *poolFullError) Unwrap() error {
	return nil
}

func (p *Pool) Size() int {
	return p.size
}

func (p *Pool) Close() error {
	p.closeOnce.Do(func() {
		close(p.quit)
	})
	p.wg.Wait()
	return p.embedder.Close()
}
