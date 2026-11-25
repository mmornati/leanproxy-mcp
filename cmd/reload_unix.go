//go:build !windows

package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// reloadOnSignal reloads the server list on SIGHUP until ctx ends.
func reloadOnSignal(ctx context.Context, m *serverManager) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	defer signal.Stop(ch)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ch:
			slog.Info("SIGHUP: reloading the config")
			_ = m.Reload(ctx)
		}
	}
}
