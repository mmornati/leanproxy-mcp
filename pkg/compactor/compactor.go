package compactor

import (
	"context"
	"fmt"
	"log/slog"
)

type Compactor struct {
	client    LLMClient
	cache     Cache
	processor *ManifestProcessor
	logger    *slog.Logger
	enabled   bool
}

type CompactorConfig struct {
	Enabled  bool
	CacheDir string
}

func NewCompactor(client LLMClient, cache Cache, cfg CompactorConfig, logger *slog.Logger) *Compactor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Compactor{
		client:    client,
		cache:     cache,
		processor: NewManifestProcessor(logger),
		logger:    logger,
		enabled:   cfg.Enabled,
	}
}

func (c *Compactor) Compact(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
	if !c.enabled {
		c.logger.Info("compactor disabled, using original manifest")
		return nil, nil
	}

	originalHash := manifest.Hash()

	cached, err := c.cache.Get(ctx, manifest.Name, originalHash)
	if err != nil {
		c.logger.Warn("cache lookup failed, proceeding with distillation", "error", err)
	}
	if cached != nil {
		c.logger.Info("using cached distilled manifest", "server", manifest.Name)
		return cached, nil
	}

	if c.client == nil {
		return nil, fmt.Errorf("compactor: no LLM client configured")
	}

	distilled, err := c.client.Distill(ctx, manifest)
	if err != nil {
		return nil, fmt.Errorf("compactor: distillation failed: %w", err)
	}

	if err := c.cache.Set(ctx, manifest.Name, distilled); err != nil {
		c.logger.Warn("failed to cache distilled manifest", "error", err)
	}

	return distilled, nil
}

func (c *Compactor) CompactWithFallback(ctx context.Context, manifest RawManifest) (*DistilledManifest, error) {
	distilled, err := c.Compact(ctx, manifest)
	if err != nil {
		c.logger.Warn("compaction failed, using original manifest", "error", err)
		return c.processor.Process(ctx, manifest)
	}
	return distilled, nil
}

func (c *Compactor) InvalidateCache(ctx context.Context, serverName string) error {
	return c.cache.Invalidate(ctx, serverName)
}

func (c *Compactor) IsEnabled() bool {
	return c.enabled
}
