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

// EmbedOutcome is what pool.Embed sends back: either a successful Embedding
// (Err == nil) or an error (Embedding zero-valued). One value, one close —
// callers can read with `case out := <-ch:` and never have to race between
// two channels.
type EmbedOutcome struct {
	Embedding Embedding
	Err       error
}

type job struct {
	ctx    context.Context
	cancel context.CancelFunc
	req    EmbedRequest
	out    chan EmbedOutcome
}

type Pool struct {
	embedder  Embedder
	jobs      chan job
	wg        sync.WaitGroup
	quit      chan struct{}
	closeOnce sync.Once
	shutdown  context.Context
	cancelAll context.CancelFunc
	logger    *slog.Logger
	size      int
}

func NewPool(embedder Embedder, cfg PoolConfig, logger *slog.Logger) *Pool {
	cfg = cfg.withDefaults()
	if logger == nil {
		logger = slog.Default()
	}

	shutdownCtx, cancelAll := context.WithCancel(context.Background())

	p := &Pool{
		embedder:  embedder,
		jobs:      make(chan job, cfg.Queue),
		quit:      make(chan struct{}),
		shutdown:  shutdownCtx,
		cancelAll: cancelAll,
		logger:    logger,
		size:      cfg.Size,
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
			p.handleJob(j)
		case <-p.quit:
			p.logger.Debug("embedder pool worker stopped", "worker_id", id)
			return
		}
	}
}

func (p *Pool) handleJob(j job) {
	defer j.cancel()
	defer close(j.out)

	if err := j.ctx.Err(); err != nil {
		j.out <- EmbedOutcome{Err: err}
		return
	}

	emb, err := p.embedder.Embed(j.ctx, j.req)
	j.out <- EmbedOutcome{Embedding: emb, Err: err}
}

func (p *Pool) Embed(ctx context.Context, req EmbedRequest) <-chan EmbedOutcome {
	embedCtx, cancel := context.WithCancel(ctx)
	j := job{
		ctx:    embedCtx,
		cancel: cancel,
		req:    req,
		out:    make(chan EmbedOutcome, 1),
	}

	select {
	case p.jobs <- j:
		// Job accepted by queue. Tie embedCtx to pool shutdown so Close()
		// interrupts in-flight HTTP calls. Spawn AFTER push to keep the
		// pool-full path goroutine-free.
		go func() {
			select {
			case <-p.shutdown.Done():
				cancel()
			case <-embedCtx.Done():
			}
		}()
		return j.out
	default:
		// Pool full: cancel the local ctx and deliver ErrPoolFull synchronously.
		cancel()
		j.out <- EmbedOutcome{Err: ErrPoolFull}
		close(j.out)
		return j.out
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

// Provider returns the underlying embedder's provider (ollama, openai, ...).
func (p *Pool) Provider() Provider {
	if p == nil || p.embedder == nil {
		return ""
	}
	return p.embedder.Provider()
}

func (p *Pool) Close() error {
	p.closeOnce.Do(func() {
		// Cancel all in-flight jobs first; their HTTP calls will return ctx.Err().
		p.cancelAll()
		// Then signal workers to stop accepting new jobs.
		close(p.quit)
	})
	p.wg.Wait()
	return p.embedder.Close()
}
