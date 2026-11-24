//go:build windows

package cmd

import "context"

// reloadOnSignal does nothing on Windows (no SIGHUP): the config watcher
// still picks up changes.
func reloadOnSignal(ctx context.Context, m *serverManager) { <-ctx.Done() }
