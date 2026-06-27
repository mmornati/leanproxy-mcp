package bouncer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mmornati/leanproxy-mcp/pkg/cache/embedder"
)

var globalEmbedPool *embedder.Pool

func SetGlobalEmbedPool(p *embedder.Pool) {
	globalEmbedPool = p
}

func GlobalEmbedPool() *embedder.Pool {
	return globalEmbedPool
}

type EmbedRequest struct {
	ToolName string
	Args     json.RawMessage
}

type EmbedResult struct {
	Vector []float32 `json:"vector"`
	Model  string    `json:"model"`
}

func EmbedToolCall(ctx context.Context, req EmbedRequest) (<-chan embedder.Embedding, <-chan error) {
	emptyEmb := make(chan embedder.Embedding, 1)
	close(emptyEmb)
	emptyErr := make(chan error, 1)
	emptyErr <- fmt.Errorf("embedder: no global embed pool configured")
	close(emptyErr)

	pool := globalEmbedPool
	if pool == nil {
		return emptyEmb, emptyErr
	}

	embReq := embedder.EmbedRequest{
		ToolName: req.ToolName,
		Args:     req.Args,
	}

	return pool.Embed(ctx, embReq)
}

func MustSetupEmbedder(cfg embedder.Config, poolCfg embedder.PoolConfig) {
	logger := slog.With("component", "bouncer.embedder")

	eng, err := newEmbedderFromConfig(cfg, logger)
	if err != nil {
		slog.Error("embedder setup failed", "error", err)
		return
	}

	pool := embedder.NewPool(eng, poolCfg, logger)
	SetGlobalEmbedPool(pool)
	slog.Info("embedder pool initialized",
		"provider", cfg.Provider,
		"pool_size", poolCfg.Size,
	)
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
