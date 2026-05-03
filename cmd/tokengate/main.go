package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if err := RootCmd.Execute(); err != nil {
		slog.Error("command failed", "error", err)
		os.Exit(ExitGeneral)
	}
}