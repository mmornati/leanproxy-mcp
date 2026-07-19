//go:build mlx

// This file implements the MLX / Apple Silicon sidecar client. It is gated by
// the `mlx` build tag because real MLX inference requires cgo bindings to
// mlx-c (or an equivalent Apple Silicon runtime) which is not available in
// this build environment.
//
// What this stub provides today:
//   - the full build-tag plumbing, config plumbing and dispatcher wiring that
//     a real mlx-c implementation will plug into;
//   - platform detection (darwin/arm64 only);
//   - model-file presence checks;
//   - nil-safe Redact/Healthy/Close that satisfy the RedactClient interface
//     by returning the placeholder redaction literal.
//
// What it does NOT do (TODO: real cgo binding to mlx-c):
//   - load weights, run inference, or stream tokens;
//   - Redact is currently a no-op string substitution. Treat any redaction
//     performed by this client as best-effort and prefer Ollama for
//     production traffic until the cgo wiring lands.
//
// Remove or update this file when the binding is in place; the build-tag
// contract in mlx_disabled.go and the dispatcher in ollama.go's NewClient
// must continue to behave identically when the tag is absent.

package sidecar

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
)

type MLXClient struct {
	modelPath string
	modelName string
	logger    *slog.Logger
	fallback  atomic.Int64
}

func newMLXClient(cfg Config, logger *slog.Logger) (RedactClient, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, fmt.Errorf(
			"MLX requires Apple Silicon (darwin/arm64), got %s/%s",
			runtime.GOOS, runtime.GOARCH,
		)
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("sidecar: MLX model must be configured (e.g. model: %s)", defaultMLXModel)
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("MLX: cannot determine user config dir: %w", err)
	}
	modelsDir := filepath.Join(configDir, "leanproxy", "models")
	modelPath := filepath.Join(modelsDir, cfg.Model)

	if info, err := os.Stat(modelPath); err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf(
				"MLX model not found at %s: download a MLX-format model from https://huggingface.co/mlx-community and place it at this path (e.g. via 'huggingface-cli download mlx-community/%s --local-dir %s')",
				modelPath, cfg.Model, modelsDir,
			)
		}
		return nil, fmt.Errorf("MLX: cannot stat model path %s: %w", modelPath, err)
	} else if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("MLX model path %s is not a regular file", modelPath)
	}

	logger.Info("MLX client initialized (placeholder; real mlx-c binding TODO)",
		"model", cfg.Model,
		"path", modelPath,
	)

	return &MLXClient{
		modelPath: modelPath,
		modelName: cfg.Model,
		logger:    logger,
	}, nil
}

func (m *MLXClient) Redact(ctx context.Context, content string) string {
	if m == nil {
		return content
	}
	if err := ctx.Err(); err != nil {
		return content
	}
	if content == "" {
		return content
	}
	return m.aggressiveRedact(content)
}

// aggressiveRedact is the placeholder redaction path used until the real
// mlx-c cgo binding is wired in. See file-level doc comment.
func (m *MLXClient) aggressiveRedact(content string) string {
	if len(content) == 0 {
		return content
	}
	return PlaceholderRedacted
}

func (m *MLXClient) FallbackCount() int64 {
	if m == nil {
		return 0
	}
	return m.fallback.Load()
}

func (m *MLXClient) Provider() string {
	if m == nil {
		return ""
	}
	return ProviderMLX
}

func (m *MLXClient) Model() string {
	if m == nil {
		return ""
	}
	return m.modelName
}

func (m *MLXClient) Healthy(ctx context.Context) bool {
	if m == nil || m.modelPath == "" {
		return false
	}
	if err := ctx.Err(); err != nil {
		return false
	}
	info, err := os.Stat(m.modelPath)
	if err != nil {
		return false
	}
	return info.Mode().IsRegular()
}

func (m *MLXClient) Close() error {
	if m == nil {
		return nil
	}
	return nil
}
