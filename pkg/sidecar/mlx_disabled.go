//go:build !mlx

package sidecar

import (
	"fmt"
	"log/slog"
)

func newMLXClient(cfg Config, logger *slog.Logger) (RedactClient, error) {
	return nil, fmt.Errorf(
		"MLX support not compiled in: rebuild with -tags mlx (requires macOS Apple Silicon)",
	)
}
