package pool

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
)

// TestCrashMidRequestIsNotReportedHealthy: a child that exits while a
// request is in flight must end up in the error state (and be eligible for
// restart), not be flipped back to idle by the request's own cleanup.
func TestCrashMidRequestIsNotReportedHealthy(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	p := NewStdioPool(5, 0, logger)
	defer p.Close()
	ctx := context.Background()

	const iters = 10
	for i := 0; i < iters; i++ {
		name := "crash-" + string(rune('a'+i))
		cfg := &migrate.ServerConfig{
			Name:      name,
			Transport: "stdio",
			Stdio:     &migrate.StdioConfig{Command: "sh", Args: []string{"-c", "read l; exit 1"}},
		}
		if err := p.StartServer(ctx, cfg); err != nil {
			t.Fatalf("StartServer: %v", err)
		}

		// The crash surfaces either as a transport error or as a JSON-RPC
		// error in the response; which one depends on timing and is not
		// what this test is about.
		resp, err := p.SendRequestToServer(ctx, name, "tools/list", nil, 2*time.Second)
		if err == nil && (resp == nil || resp.Error == nil) {
			t.Fatalf("iter %d: expected an error from a child that exited", i)
		}

		// Poll briefly: waitForExit runs asynchronously after the request
		// returns, but well within this window. Stay under the restart
		// backoff so we observe the post-crash state, not a fresh child.
		deadline := time.Now().Add(500 * time.Millisecond)
		var state ServerState
		for time.Now().Before(deadline) {
			state, _ = p.GetServerState(name)
			if state == StateError || state == StateStopped {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if state == StateIdle || state == StateRunning || state == StateBusy {
			t.Fatalf("iter %d: crashed child reported %v", i, state)
		}
		if _, err := p.GetServer(name); err == nil {
			t.Fatalf("iter %d: GetServer returned a crashed server as healthy", i)
		}
	}
}
