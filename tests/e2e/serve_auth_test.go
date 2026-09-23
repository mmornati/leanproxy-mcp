package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// End-to-end coverage for issue #298 (secure the `serve` TCP listener),
// driving the real binary: a browser-style cross-protocol POST never reaches
// the upstream, an unauthenticated connection is closed silently, an
// authenticated client works, and --no-auth is refused on a non-loopback
// address. Like the lifecycle test it builds its own binaries, so it always
// runs.

func TestServeAuth_RealBinary(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, fakeBin := buildLifecycleBinaries(t, binDir)
	dir := t.TempDir()
	server := fmt.Sprintf("auth%d", os.Getpid())
	cfg := filepath.Join(dir, "leanproxy.yaml")
	writeFile(t, cfg, fmt.Sprintf(`version: "1.0"
reconnect:
  enabled: false
servers:
  - name: %s
    transport: stdio
    enabled: true
    stdio:
      command: %q
`, server, fakeBin))
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	logs := &syncBuffer{}
	cmd := exec.Command(proxyBin, "serve", "--config", cfg, "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Env = append(os.Environ(), "LEANPROXY_CONFIG="+cfg, "HOME="+dir)
	cmd.Stdout = logs
	cmd.Stderr = logs
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
	waitForPort(t, addr)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(serveAuthLine()); err != nil {
		t.Fatal(err)
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
	// received reports how many requests the fake upstream has seen; the
	// "stats" call itself is one of them.
	received := func(id int) int {
		t.Helper()
		var resp map[string]json.RawMessage
		deadline := time.Now().Add(20 * time.Second)
		for {
			resp = call(id, server+".stats", map[string]interface{}{})
			if resp["error"] == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s never became routable: %s", server, resp["error"])
			}
			id += 1000
			time.Sleep(50 * time.Millisecond)
		}
		var stats struct {
			Received int `json:"received"`
		}
		if err := json.Unmarshal(resp["result"], &stats); err != nil {
			t.Fatalf("stats result %s: %v", resp["result"], err)
		}
		return stats.Received
	}

	before := received(1)

	// The audit PoC: a text/plain POST (a CORS "simple request", no
	// preflight) whose body is a JSON-RPC tools/call line.
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"%s.echo","arguments":{"tag":"pwned"}}}`+"\n", server)
	client := &http.Client{Timeout: 15 * time.Second}
	if resp, err := client.Post("http://"+addr+"/", "text/plain", strings.NewReader(body)); err == nil {
		resp.Body.Close()
		t.Fatalf("serve answered an HTTP request: %s", resp.Status)
	}

	// An unauthenticated raw connection: wrong token, then a request.
	bad, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	_, _ = bad.Write([]byte(`{"jsonrpc":"2.0","method":"auth","params":{"token":"not-the-token-0123456789"}}` + "\n" + body))
	_ = bad.SetReadDeadline(time.Now().Add(15 * time.Second))
	if data, _ := io.ReadAll(bad); len(data) != 0 {
		t.Fatalf("unauthenticated connection got an answer: %q", data)
	}

	after := received(2)
	if after != before+1 {
		t.Fatalf("upstream received %d requests between the two stats calls, want 1 (the second stats call): the rejected requests reached the upstream", after-before)
	}
	if !strings.Contains(logs.String(), "rejected HTTP request on the serve JSON-RPC port") {
		t.Error("no log line for the rejected HTTP request")
	}
	if !strings.Contains(logs.String(), "serve connection failed authentication") {
		t.Error("no log line for the failed authentication")
	}
	if strings.Contains(logs.String(), e2eServeToken) {
		t.Error("the auth token was logged")
	}
	if _, err := os.Stat(filepath.Join(dir, ".config", "leanproxy", "serve.token")); err == nil {
		t.Error("serve wrote a token file although LEANPROXY_SERVE_TOKEN was set")
	}

	// A healthy authenticated call still works.
	resp := call(3, server+".echo", map[string]interface{}{"tag": "hello"})
	if resp["error"] != nil || !bytes.Contains(resp["result"], []byte("hello")) {
		t.Fatalf("authenticated echo: %v", resp)
	}
}

func TestServeAuth_NoAuthRefusedOnNonLoopback(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, _ := buildLifecycleBinaries(t, binDir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, proxyBin, "serve", "--no-auth", "--listen", "0.0.0.0:9000", "--metrics-bind", "off", "--dashboard-bind", "off")
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("serve --no-auth --listen 0.0.0.0:9000 kept running instead of refusing:\n%s", out)
	}
	if err == nil {
		t.Fatalf("serve --no-auth --listen 0.0.0.0:9000 exited 0:\n%s", out)
	}
	if !strings.Contains(string(out), "--no-auth is only allowed on a loopback address") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}

func TestServeAuth_TokenFileGenerated(t *testing.T) {
	binDir := t.TempDir()
	proxyBin, _ := buildLifecycleBinaries(t, binDir)
	home := t.TempDir()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	cmd := exec.Command(proxyBin, "serve", "--config", filepath.Join(home, "missing.yaml"), "--listen", addr, "--metrics-bind", "off", "--dashboard-bind", "off")
	env := []string{"HOME=" + home}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "LEANPROXY_SERVE_TOKEN=") && !strings.HasPrefix(kv, "HOME=") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	logs := &syncBuffer{}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
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
	waitForPort(t, addr)

	path := filepath.Join(home, ".config", "leanproxy", "serve.token")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("token file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token file mode %v, want 0600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(raw))

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","method":"auth","params":{"token":"` + token + `"}}` + "\n" +
		`{"jsonrpc":"2.0","id":1,"method":"list_servers"}` + "\n"))
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("authenticated with the generated token: %v", err)
	}
	if !bytes.Contains(line, []byte(`"id":1`)) {
		t.Fatalf("unexpected answer %s", line)
	}
	if strings.Contains(logs.String(), token) {
		t.Error("the generated token was logged")
	}
}
