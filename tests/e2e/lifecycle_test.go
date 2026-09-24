package e2e

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for issue #297 (server lifecycle): with one hung, one
// slow and one healthy upstream, both front ends start serving at once, a
// slow or hung server never blocks the others, every server process
// generation gets exactly one MCP initialize, and a killed stdio child is
// respawned with a fresh handshake.
//
// Like the firewall e2e test it builds its own binaries (leanproxy-mcp and
// the fake server from pkg/pool/testdata/concurrentmcp), so it always runs.
// Wall-clock bounds are deliberately generous (CI runners are slow): they
// only separate "answered right away" from "waited for another server".

const (
	// lifecycleFast bounds requests that must not wait for any upstream
	// (the target is well under 200 ms; CI gets a wide margin).
	lifecycleFast = 3 * time.Second
	// lifecycleSlowInit is how long the slow server takes to answer
	// initialize (the issue's real-binary check uses 5 s).
	lifecycleSlowInit = 1500 * time.Millisecond
	// lifecycleHungTimeout is the hung server's configured timeout.
	lifecycleHungTimeout = 2 * time.Second
)

func buildLifecycleBinaries(t *testing.T, dir string) (proxyBin, fakeBin string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not available")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wd, "..", "..")
	proxyBin = filepath.Join(dir, "leanproxy-mcp")
	fakeBin = filepath.Join(dir, "concurrentmcp")
	for _, b := range []struct{ out, pkg string }{
		{proxyBin, "."},
		{fakeBin, "./pkg/pool/testdata/concurrentmcp"},
	} {
		cmd := exec.Command(goBin, "build", "-o", b.out, b.pkg)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", b.pkg, err, out)
		}
	}
	return proxyBin, fakeBin
}

// writeLifecycleConfig writes a config with three concurrentmcp servers:
// "<p>hung" never answers initialize, "<p>slow" answers it after
// lifecycleSlowInit, "<p>ok" is healthy. concurrentmcp answers test-hook
// tools (stats, pid, sleep, ...) it does not advertise in tools/list, so
// the per-tool policy (#314) must let unadvertised tools through.
func writeLifecycleConfig(t *testing.T, dir, prefix, fakeBin string) string {
	t.Helper()
	path := filepath.Join(dir, "leanproxy.yaml")
	cfg := fmt.Sprintf(`version: "1.0"
reconnect:
  restart_backoff: 100ms
policy:
  unknown_tools: allow
servers:
  - name: %[1]shung
    transport: stdio
    enabled: true
    timeout: %[3]s
    stdio:
      command: %[2]q
      args: ["--init-delay-ms", "600000"]
  - name: %[1]sslow
    transport: stdio
    enabled: true
    stdio:
      command: %[2]q
      args: ["--init-delay-ms", "%[4]d"]
  - name: %[1]sok
    transport: stdio
    enabled: true
    stdio:
      command: %[2]q
      args: ["--instructions", "Healthy test server."]
`, prefix, fakeBin, lifecycleHungTimeout, lifecycleSlowInit.Milliseconds())
	writeFile(t, path, cfg)
	return path
}

// initializeCount counts the "sending MCP initialize" log lines for server.
func initializeCount(logs, server string) int {
	re := regexp.MustCompile(`msg="sending MCP initialize" name=` + regexp.QuoteMeta(server) + ` `)
	return len(re.FindAllString(logs, -1))
}

// send writes one request without waiting for its answer.
func (s *stdioSession) send(id int, method string, params interface{}) {
	s.t.Helper()
	req, err := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		s.t.Fatal(err)
	}
	if _, err := s.stdin.Write(append(req, '\n')); err != nil {
		s.t.Fatalf("write request %d: %v", id, err)
	}
}

// recv reads the next response, whatever its id.
func (s *stdioSession) recv(timeout time.Duration) rpcResponse {
	s.t.Helper()
	select {
	case line, ok := <-s.lines:
		if !ok {
			s.t.Fatal("proxy closed stdout")
		}
		var resp rpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			s.t.Fatalf("invalid JSON-RPC response: %v: %s", err, line)
		}
		resp.raw = string(line)
		return resp
	case <-time.After(timeout):
		s.t.Fatal("timed out waiting for a response")
	}
	return rpcResponse{}
}

func timed(f func()) time.Duration {
	start := time.Now()
	f()
	return time.Since(start)
}

// fakePID asks a concurrentmcp server (through the proxy) for its PID.
func fakePID(t *testing.T, result json.RawMessage) int {
	t.Helper()
	var st struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(result, &st); err != nil || st.PID == 0 {
		t.Fatalf("no pid in stats result %s (%v)", result, err)
	}
	return st.PID
}

func TestServerLifecycle_Stdio(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildLifecycleBinaries(t, binDir)
	dir := t.TempDir()
	prefix := fmt.Sprintf("lc%d", os.Getpid())
	hung, slow, ok := prefix+"hung", prefix+"slow", prefix+"ok"
	s := startStdioProxy(t, proxyBin, writeLifecycleConfig(t, dir, prefix, fakeBin))

	var resp rpcResponse
	d := timed(func() {
		resp = s.call(1, "initialize", map[string]interface{}{
			"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{},
			"clientInfo": map[string]string{"name": "e2e", "version": "1"},
		})
	})
	if resp.Error != nil || d > lifecycleFast {
		t.Fatalf("initialize took %v: %s", d, resp.raw)
	}
	t.Logf("initialize answered in %v", d)

	d = timed(func() { resp = s.call(2, "tools/list", map[string]interface{}{}) })
	if resp.Error != nil || d > lifecycleFast {
		t.Fatalf("tools/list took %v: %s", d, resp.raw)
	}
	t.Logf("tools/list answered in %v", d)

	d = timed(func() { resp = s.toolCall(3, "list_tools", map[string]string{"server_name": ok}) })
	if resp.Error != nil || !strings.Contains(resp.raw, ok+" tools (3)") || d > lifecycleFast {
		t.Fatalf("list_tools(%s) took %v: %s", ok, d, resp.raw)
	}
	t.Logf("list_tools(healthy) answered in %v", d)

	// Slow and hung in flight together: the healthy server still answers
	// first, then slow (after its initialize delay), then hung (after its
	// timeout, with an error message).
	s.send(10, "tools/call", map[string]interface{}{"name": "list_tools", "arguments": map[string]string{"server_name": hung}})
	s.send(11, "tools/call", map[string]interface{}{"name": "list_tools", "arguments": map[string]string{"server_name": slow}})
	start := time.Now()
	s.send(12, "tools/call", map[string]interface{}{"name": ok + "_echo", "arguments": map[string]string{"tag": "hi"}})
	first := s.recv(lifecycleFast)
	if fmt.Sprint(first.ID) != "12" || first.Error != nil {
		t.Fatalf("first answer = %s, want the healthy server's call (id 12)", first.raw)
	}
	t.Logf("healthy call answered in %v while slow and hung were pending", time.Since(start))
	seen := map[string]rpcResponse{}
	for len(seen) < 2 {
		r := s.recv(lifecycleHungTimeout + lifecycleSlowInit + 10*time.Second)
		seen[fmt.Sprint(r.ID)] = r
	}
	if r := seen["11"]; r.Error != nil || !strings.Contains(r.raw, slow+" tools (3)") {
		t.Fatalf("list_tools(%s) = %s", slow, r.raw)
	}
	if r := seen["10"]; !strings.Contains(r.raw, "No tools available on server '"+hung+"'") || !strings.Contains(r.raw, "failed") {
		t.Fatalf("list_tools(%s) = %s, want a failure after its timeout", hung, r.raw)
	}

	// list_servers shows the stored serverInfo / instructions.
	resp = s.toolCall(20, "list_servers", map[string]interface{}{})
	if !strings.Contains(resp.raw, ok+" (stdio, healthy, 3 tools) [concurrentmcp 1.0.0] instructions: Healthy test server.") {
		t.Fatalf("list_servers = %s", resp.raw)
	}

	// Kill the healthy child: the pool respawns it and the next call
	// succeeds on the new process after a fresh handshake.
	resp = s.toolCall(30, ok+"_stats", map[string]interface{}{})
	if resp.Error != nil {
		t.Fatalf("stats: %s", resp.raw)
	}
	oldPID := fakePID(t, resp.Result)
	if err := syscall.Kill(oldPID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill %d: %v", oldPID, err)
	}
	newPID := 0
	deadline := time.Now().Add(20 * time.Second)
	for id := 31; newPID == 0 || newPID == oldPID; id++ {
		if time.Now().After(deadline) {
			t.Fatalf("no call succeeded on a respawned child; last response %s", resp.raw)
		}
		resp = s.toolCall(id, ok+"_stats", map[string]interface{}{})
		if resp.Error == nil {
			newPID = fakePID(t, resp.Result)
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}

	logs := s.stderr.String()
	if n := initializeCount(logs, ok); n != 2 {
		t.Fatalf("%s: %d initialize handshakes, want 2 (one per process generation)\n%s", ok, n, logs)
	}
	if !strings.Contains(logs, `msg="sending MCP initialize" name=`+ok+` generation=2`) {
		t.Fatalf("no handshake logged for the respawned generation of %s", ok)
	}
	for _, name := range []string{slow, hung} {
		if n := initializeCount(logs, name); n != 1 {
			t.Fatalf("%s: %d initialize handshakes, want exactly 1", name, n)
		}
	}
}

func TestServerLifecycle_Serve(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildLifecycleBinaries(t, binDir)
	dir := t.TempDir()
	prefix := fmt.Sprintf("lcs%d", os.Getpid())
	ok := prefix + "ok"
	cfg := writeLifecycleConfig(t, dir, prefix, fakeBin)
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	logs := &syncBuffer{}
	cmd := exec.Command(proxyBin, "serve", "--config", cfg, "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+cfg)
	cmd.Stdout = logs
	cmd.Stderr = logs
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		if t.Failed() {
			t.Logf("serve logs:\n%s", logs.String())
		}
	})

	var conn net.Conn
	for {
		c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			conn = c
			break
		}
		if time.Since(start) > lifecycleFast {
			t.Fatalf("serve did not accept connections within %v: %v", lifecycleFast, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer conn.Close()
	t.Logf("serve accepted a connection %v after start", time.Since(start))
	if _, err := conn.Write(serveAuthLine()); err != nil {
		t.Fatalf("authenticate: %v", err)
	}

	reader := bufio.NewReader(conn)
	call := func(id int, tool string, args interface{}) map[string]json.RawMessage {
		t.Helper()
		req, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": "tools/call",
			"params": map[string]interface{}{"name": tool, "arguments": args}})
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		if _, err := conn.Write(append(req, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
		line, err := reader.ReadBytes('\n')
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var resp map[string]json.RawMessage
		if err := json.Unmarshal(line, &resp); err != nil {
			t.Fatalf("invalid response %s: %v", line, err)
		}
		return resp
	}

	// The healthy server's tools become routable once the background
	// refresh has seen them (nothing was cached for this server before).
	var resp map[string]json.RawMessage
	deadline := time.Now().Add(20 * time.Second)
	for id := 1; ; id++ {
		resp = call(id, ok+".stats", map[string]interface{}{})
		if resp["error"] == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never became routable: %s", ok, resp["error"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	oldPID := fakePID(t, resp["result"])
	if err := syscall.Kill(oldPID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill %d: %v", oldPID, err)
	}
	deadline = time.Now().Add(20 * time.Second)
	for id := 100; ; id++ {
		resp = call(id, ok+".stats", map[string]interface{}{})
		if resp["error"] == nil && fakePID(t, resp["result"]) != oldPID {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no call succeeded on a respawned child: %v", resp)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if n := initializeCount(logs.String(), ok); n != 2 {
		t.Fatalf("%s: %d initialize handshakes, want 2 (one per process generation)", ok, n)
	}
}
