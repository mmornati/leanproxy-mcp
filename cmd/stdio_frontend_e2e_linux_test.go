//go:build linux

package cmd

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/mcp"
	"github.com/mmornati/leanproxy-mcp/pkg/migrate"
	"github.com/mmornati/leanproxy-mcp/pkg/pool"
	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

var (
	fakeMCPOnce sync.Once
	fakeMCPPath string
	fakeMCPErr  error
)

// concurrentMCPBinary builds pkg/pool/testdata/concurrentmcp (a fake stdio
// MCP server that handles requests concurrently) once per test binary.
func concurrentMCPBinary(t *testing.T) string {
	t.Helper()
	fakeMCPOnce.Do(func() {
		dir, err := os.MkdirTemp("", "concurrentmcp-cmd-")
		if err != nil {
			fakeMCPErr = err
			return
		}
		fakeMCPPath = filepath.Join(dir, "concurrentmcp")
		out, err := exec.Command("go", "build", "-o", fakeMCPPath, "../pkg/pool/testdata/concurrentmcp").CombinedOutput()
		if err != nil {
			fakeMCPErr = errors.New(string(out))
		}
	})
	if fakeMCPErr != nil {
		t.Fatalf("build concurrentmcp: %v", fakeMCPErr)
	}
	return fakeMCPPath
}

func invokeTool(id int, server, tool string, args string) string {
	return `{"jsonrpc":"2.0","id":` + strconv.Itoa(id) + `,"method":"tools/call","params":{"name":"invoke_tool","arguments":{"server":"` + server + `","tool":"` + tool + `","arguments":` + args + `}}}`
}

// TestStdioFrontend_EndToEnd drives the real handler and stdio pool (two
// fake servers) through serveStdio:
//   - a call to another server and a ping are answered while a 2s call runs;
//   - a cancel notification for an in-flight call cancels it upstream and
//     it gets no response;
//   - EOF drains, and closing the pool leaves no orphan processes.
func TestStdioFrontend_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and spawns fake MCP servers")
	}
	bin := concurrentMCPBinary(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	stdioPool := pool.NewStdioPool(5, 0, logger)
	unified := pool.NewUnifiedPool(stdioPool, pool.NewHTTPClientPool(logger), pool.NewSSEPool(logger), logger)
	var closeOnce sync.Once
	closePools := func() { closeOnce.Do(func() { _ = unified.Close() }) }
	t.Cleanup(closePools)

	childPIDFile := filepath.Join(t.TempDir(), "child.pid")
	for _, srv := range []struct {
		name string
		args []string
	}{
		{"slow", []string{"--spawn-child", childPIDFile}},
		{"fast", nil},
	} {
		if err := stdioPool.StartServer(context.Background(), &migrate.ServerConfig{
			Name:         srv.name,
			Transport:    registry.TransportStdio,
			Stdio:        &migrate.StdioConfig{Command: bin, Args: srv.args},
			TimeoutValue: 30 * time.Second,
		}); err != nil {
			t.Fatalf("start %s: %v", srv.name, err)
		}
	}

	handler := mcp.NewHandler(unified, logger)
	fh := startFrontend(t, handler, stdioFrontendOptions{})

	// Warm both servers up (lazy MCP handshake) so timings below measure
	// the front end, not process start-up.
	fh.send(invokeTool(100, "slow", "echo", `{"tag":"warm"}`))
	fh.send(invokeTool(101, "fast", "echo", `{"tag":"warm"}`))
	for i := 0; i < 2; i++ {
		if r := fh.next(); r.Error != nil {
			t.Fatalf("warm-up failed: %+v", r.Error)
		}
	}

	// A 2s call to "slow", then an instant call to "fast" and a ping.
	fh.send(invokeTool(1, "slow", "sleep", `{"ms":2000,"tag":"slow"}`))
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	fh.send(invokeTool(2, "fast", "echo", `{"tag":"fast"}`))
	fh.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)

	got := map[string]time.Duration{}
	for len(got) < 2 {
		r := fh.next()
		if string(r.ID) == "1" {
			t.Fatal("the 2s call finished before the instant requests: requests are serialized")
		}
		if r.Error != nil {
			t.Fatalf("response %s error: %+v", r.ID, r.Error)
		}
		got[string(r.ID)] = time.Since(start)
	}
	t.Logf("while a 2s call runs: other-server call answered in %v, ping in %v", got["2"], got["3"])
	for id, d := range got {
		// The issue's target is < 100ms; the bound is loose for slow CI.
		if d > time.Second {
			t.Errorf("request %s took %v while the 2s call was running", id, d)
		}
	}

	// Cancel an in-flight call that would never answer.
	fh.send(invokeTool(4, "slow", "hang", `{}`))
	time.Sleep(100 * time.Millisecond)
	cancelStart := time.Now()
	fh.send(`{"jsonrpc":"2.0","method":"` + methodCancelled + `","params":{"requestId":4,"reason":"test"}}`)

	// The upstream server must see a cancel notification.
	deadline := time.Now().Add(frontendWait)
	statsID := 500
	for {
		fh.send(invokeTool(statsID, "slow", "stats", `{}`))
		var r frontendResp
		for {
			r = fh.next()
			if string(r.ID) == "4" {
				t.Fatal("the canceled request was answered")
			}
			if string(r.ID) == strconv.Itoa(statsID) {
				break
			}
			if string(r.ID) != "1" {
				t.Fatalf("unexpected response %s", r.ID)
			}
		}
		if r.Error != nil {
			t.Fatalf("stats error: %+v", r.Error)
		}
		if strings.Contains(string(r.Result), `"reason":"`) {
			t.Logf("upstream saw a cancel notification %v after the client's", time.Since(cancelStart))
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream never received a cancel notification; stats: %s", r.Result)
		}
		statsID++
		time.Sleep(20 * time.Millisecond)
	}

	// EOF: the 2s call (id 1) is drained, then the pools are closed.
	for _, r := range fh.finish() {
		if string(r.ID) == "4" {
			t.Fatal("the canceled request was answered")
		}
	}
	closePools()

	pidBytes, err := os.ReadFile(childPIDFile)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("parse child pid: %v", err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for pidRunning(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild process %d still alive after the pool closed", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// pidRunning reports whether pid is alive and not a zombie.
func pidRunning(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows "(comm) "; Z is a zombie.
	if i := bytes.LastIndexByte(data, ')'); i >= 0 && i+2 < len(data) {
		return data[i+2] != 'Z'
	}
	return true
}
