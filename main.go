package main

import (
	"log/slog"
	"os"

	"github.com/mmornati/leanproxy-mcp/cmd"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := cmd.RootCmd.Execute(); err != nil {
		slog.Error("failed to execute command", "error", err)
		os.Exit(1)
	}
}
