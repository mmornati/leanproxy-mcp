package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/usage"
)

// usageStore is the process-wide JSONL usage store (issue #324): every
// front end (`server run --stdio`, `server run --http`, `serve`) appends the
// same kind of snapshot to it, from pkg/metrics.Snapshot(), so `report`
// behaves identically no matter which front end produced the numbers.
var (
	usageStoreOnce sync.Once
	usageStore     *usage.Store
	usageSessionID string
)

// usageRetentionEnv overrides the default 90-day retention (see the
// acceptance criteria: "old files are rotated and pruned (default 90 days,
// configurable)"). Accepts a positive integer number of days.
const usageRetentionEnv = "LEANPROXY_USAGE_RETENTION_DAYS"

// usageRetention returns the configured retention window, defaulting to
// usage.DefaultRetentionDays.
func usageRetention() time.Duration {
	if v := os.Getenv(usageRetentionEnv); v != "" {
		if days, err := strconv.Atoi(v); err == nil && days > 0 {
			return time.Duration(days) * 24 * time.Hour
		}
		slog.Warn("usage: ignoring invalid retention override", "env", usageRetentionEnv, "value", v)
	}
	return usage.DefaultRetentionDays * 24 * time.Hour
}

// initUsageStore opens the usage store once per process (idempotent: safe
// to call from every front end's startup path) and prunes old files.
// frontend labels the session id ("stdio", "http", "serve") so a report
// reading multiple days can tell which front end produced a record,
// without ever storing a payload.
func initUsageStore(frontend string) {
	usageStoreOnce.Do(func() {
		s, err := usage.NewStore()
		if err != nil {
			slog.Warn("usage: failed to open usage store, savings will not be recorded to disk", "error", err)
			return
		}
		usageStore = s
		usageSessionID = fmt.Sprintf("%s-%d-%d", frontend, os.Getpid(), time.Now().UTC().Unix())
		if removed, err := s.Prune(usageRetention()); err != nil {
			slog.Warn("usage: prune failed", "error", err)
		} else if removed > 0 {
			slog.Info("usage: pruned old usage files", "removed", removed, "dir", s.Dir())
		}
	})
}

// flushUsageSnapshot appends one Record built from the current process-wide
// counters (pkg/metrics.Snapshot). It is called on the same cadence the
// status file already updates on, plus once at startup and once at
// shutdown, so even a short-lived session (e.g. the benchmark harness)
// leaves at least one record behind for `report` to read.
func flushUsageSnapshot() {
	if usageStore == nil {
		return
	}
	if err := usageStore.Append(usage.NewRecord(usageSessionID)); err != nil {
		slog.Warn("usage: failed to append snapshot", "error", err)
	}
}
