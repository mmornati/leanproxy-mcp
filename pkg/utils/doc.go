package utils

import (
	"context"
	"log/slog"
)

func ExampleManifestMerger() {
	logger := slog.Default()
	merger := NewManifestMerger(logger)

	base := &Config{
		Version: "1.0",
		Name:    "base-config",
		Servers: []ServerConfig{
			{ID: "server1", Command: []string{"npx", "server1"}, Port: 8080},
		},
	}

	override := &Config{
		Name: "user-config",
	}

	result, _ := merger.Merge(context.Background(), base, override)
	_ = result
}
