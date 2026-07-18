package bouncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
)

var globalEmbedPool atomic.Pointer[embedder.Pool]

func SetGlobalEmbedPool(p *embedder.Pool) {
	if p == nil {
		globalEmbedPool.Store(nil)
		return
	}
	globalEmbedPool.Store(p)
}

func GlobalEmbedPool() *embedder.Pool {
	return globalEmbedPool.Load()
}

type EmbedRequest struct {
	ToolName string
	Args     json.RawMessage
}

type EmbedOutcome struct {
	Request  EmbedRequest `json:"request"`
	Provider string       `json:"provider"`
	Model    string       `json:"model,omitempty"`
	Vector   []float32    `json:"vector,omitempty"`
	Err      string       `json:"error,omitempty"`
}

var EmbedResultHandler func(EmbedOutcome)

func EmbedToolCall(ctx context.Context, req EmbedRequest) {
	pool := globalEmbedPool.Load()
	if pool == nil {
		return
	}
	if req.ToolName == "" {
		return
	}
	if len(req.Args) > embedder.MaxPayloadBytes {
		slog.Warn("embedder: payload too large, skipping",
			"tool", req.ToolName,
			"bytes", len(req.Args))
		return
	}

	embReq := embedder.EmbedRequest{
		ToolName: req.ToolName,
		Args:     req.Args,
	}

	outCh := pool.Embed(ctx, embReq)

	go func(req EmbedRequest) {
		outcome := EmbedOutcome{Request: req, Provider: string(pool.Provider())}
		// Single outcome channel: either Embedding is set (success) or Err is set.
		o, ok := <-outCh
		if !ok {
			return
		}
		if o.Err != nil {
			outcome.Err = o.Err.Error()
			handleEmbedError(req, o.Err)
			recordEmbedFailure()
		} else {
			outcome.Vector = o.Embedding.Vector
			outcome.Model = o.Embedding.Model
			slog.Debug("embedder: success",
				"tool", req.ToolName,
				"model", o.Embedding.Model,
				"dims", len(o.Embedding.Vector))
			recordEmbedSuccess()
		}
		if EmbedResultHandler != nil {
			EmbedResultHandler(outcome)
		}
	}(req)
}

func handleEmbedError(req EmbedRequest, err error) {
	switch {
	case errors.Is(err, embedder.ErrPoolFull):
		slog.Warn("embedder: pool full, falling back to exact-match",
			"tool", req.ToolName)
	case errors.Is(err, embedder.ErrEmbedderUnavailable):
		slog.Warn("embedder: provider unreachable, falling back to exact-match",
			"tool", req.ToolName,
			"error", err)
	case errors.Is(err, embedder.ErrPayloadTooLarge):
		slog.Warn("embedder: payload too large, falling back to exact-match",
			"tool", req.ToolName,
			"error", err)
	default:
		slog.Warn("embedder: failure, falling back to exact-match",
			"tool", req.ToolName,
			"error", err)
	}
}

var (
	embedSuccessCount atomic.Uint64
	embedFailureCount atomic.Uint64
)

func recordEmbedSuccess() { embedSuccessCount.Add(1) }
func recordEmbedFailure() { embedFailureCount.Add(1) }

func EmbedSuccessCount() uint64 { return embedSuccessCount.Load() }
func EmbedFailureCount() uint64 { return embedFailureCount.Load() }

func SetupEmbedder(cfg embedder.Config, poolCfg embedder.PoolConfig) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("embedder config: %w", err)
	}

	logger := slog.With("component", "bouncer.embedder")

	eng, err := newEmbedderFromConfig(cfg, logger)
	if err != nil {
		return fmt.Errorf("embedder create: %w", err)
	}

	poolCfg = embedder.PoolConfig{
		Size:  poolCfg.Size,
		Queue: poolCfg.Queue,
	}
	if poolCfg.Size <= 0 {
		poolCfg.Size = 4
	}
	if poolCfg.Queue <= 0 {
		poolCfg.Queue = 256
	}
	pool := embedder.NewPool(eng, poolCfg, logger)
	if old := globalEmbedPool.Swap(pool); old != nil {
		_ = old.Close()
	}
	logger.Info("embedder pool initialized",
		"provider", cfg.Provider,
		"pool_size", poolCfg.Size,
	)
	return nil
}

func newEmbedderFromConfig(cfg embedder.Config, logger *slog.Logger) (embedder.Embedder, error) {
	switch cfg.Provider {
	case embedder.ProviderOllama:
		return embedder.NewOllamaEmbedder(*cfg.Ollama, logger)
	case embedder.ProviderOpenAI:
		return embedder.NewOpenAIEmbedder(*cfg.OpenAI, logger)
	default:
		return nil, fmt.Errorf("unsupported embedder provider: %s", cfg.Provider)
	}
}
