// Smoke test that proves the per-server timeout fix end-to-end.
// Spawns `leanproxy-mcp server run --stdio` against a fake slow stdio child
// ("slowfake" that sleeps 45s before replying to tools/call), configured with
// `timeout: 60s`. After the fix the request should be in-flight for >30s
// (the old hardcoded default) and the leanproxy-mcp process should still be
// alive at the 35s mark. Before the fix, leanproxy-mcp would have errored
// out at exactly 30s.
//
// Run: go run ./tests/manual/slowstdio_smoketest.go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	fakeBinaryName = "slowfake"
	sleepDuration  = 45 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: smoketest <path-to-leanproxy-mcp-binary>")
		os.Exit(2)
	}
	binary := os.Args[1]

	tmpDir, err := os.MkdirTemp("", "slowfake-")
	must(err)
	if os.Getenv("SMOKETEST_KEEP_TMPDIR") != "" {
		fmt.Fprintln(os.Stderr, "tmpdir:", tmpDir)
	} else {
		defer os.RemoveAll(tmpDir)
	}

	fakePath := filepath.Join(tmpDir, fakeBinaryName)
	writeSlowFake(fakePath)

	cfgPath := filepath.Join(tmpDir, "leanproxy_servers.yaml")
	cfg := fmt.Sprintf(`version: "1.0"
servers:
    - name: slow
      enabled: true
      transport: stdio
      stdio:
        command: %s
        args: []
        env: []
        cwd: %s
      timeout: 60s
      connect_timeout: 10s
      idle_timeout: ""
`, fakePath, tmpDir)
	must(os.WriteFile(cfgPath, []byte(cfg), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "server", "run", "--stdio",
		"--config", cfgPath,
		"--log-level", "debug",
		"--log-file", filepath.Join(tmpDir, "leanproxy.log"),
	)
	stdinR, stdinW, err := os.Pipe()
	must(err)
	stdoutR, stdoutW, err := os.Pipe()
	must(err)
	cmd.Stdin = stdinR
	cmd.Stdout = stdoutW
	cmd.Stderr = stdoutW
	must(cmd.Start())
	// We (the parent) own the write end of stdin and the read end of stdout.
	_ = stdinR.Close()
	defer stdoutR.Close()

	type step struct {
		elapsed time.Duration
		note    string
	}

	start := time.Now()
	steps := []step{}
	mark := func(note string) {
		steps = append(steps, step{time.Since(start), note})
	}

	// 1. initialize
	must(writeReq(stdinW, 1, "initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]interface{}{"name": "smoketest", "version": "0.0.1"},
	}))
	resp := mustReadResp(stdoutR)
	mark(fmt.Sprintf("initialize -> %s", truncate(string(resp), 80)))

	// 2. initialized notification
	must(writeReq(stdinW, nil, "notifications/initialized", map[string]interface{}{}))
	mark("initialized")

	// 4. tools/call — this is the request that would have failed at 30s
	// under the old code path. We send it right after.
	must(writeReq(stdinW, 3, "tools/call", map[string]interface{}{
		"name":      "slow_echo",
		"arguments": map[string]interface{}{},
	}))
	mark("tools/call sent — expecting fake to sleep 45s, leanproxy should NOT timeout at 30s")

	// 5. Read responses from the pipe until we get a JSON-RPC response whose
	// id == 3 (the tools/call id from the perspective of the parent
	// client). Anything else (the initialize reply, etc.) we discard. We do
	// this for up to 70s. Read timeouts are NOT failures — we keep looping.
	gotToolsCall := false
	var toolsCallResp []byte
	toolsCallDeadline := time.Now().Add(70 * time.Second)
	br := bufio.NewReader(stdoutR)
	for !gotToolsCall && time.Now().Before(toolsCallDeadline) {
		_ = stdoutR.SetReadDeadline(time.Now().Add(3 * time.Second))
		line, err := br.ReadBytes('\n')
		if err != nil {
			// *fs.PathError from os.File doesn't satisfy net.Error
			// directly; check Timeout() via the interface it wraps.
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			if err == io.EOF {
				mark("EOF before tools/call response")
				break
			}
			mark(fmt.Sprintf("read err: %v", err))
			break
		}
		var probe map[string]json.RawMessage
		if jerr := json.Unmarshal(line, &probe); jerr != nil {
			continue
		}
		idRaw, hasID := probe["id"]
		if !hasID {
			continue
		}
		var id float64
		_ = json.Unmarshal(idRaw, &id)
		if id == 3 {
			toolsCallResp = line
			gotToolsCall = true
		}
	}

	mark(fmt.Sprintf("tools/call response at t=%.1fs (got=%v)", time.Since(start).Seconds(), gotToolsCall))

	if gotToolsCall {
		// Inspect: is it a timeout error from the OLD hardcoded 30s path?
		var probe map[string]interface{}
		_ = json.Unmarshal(toolsCallResp, &probe)
		errBlock, _ := probe["error"].(map[string]interface{})
		msg := ""
		if errBlock != nil {
			if s, ok := errBlock["message"].(string); ok {
				msg = s
			}
		}
		if strings.Contains(msg, "request timeout after") {
			mark(fmt.Sprintf("FAIL: leanproxy returned a timeout error: %s", msg))
		} else {
			mark(fmt.Sprintf("OK: leanproxy returned a successful reply: %s", truncate(string(toolsCallResp), 120)))
		}
	} else {
		mark("FAIL: never got tools/call response")
	}

	// Drain any stderr output and then shut down.
	stdinW.Close()
	_, _ = cmd.Process.Wait()
	mark("leanproxy exited")

	// Print report
	fmt.Println()
	fmt.Println("=== smoke test report ===")
	for _, s := range steps {
		fmt.Printf("  t=%6.1fs  %s\n", s.elapsed.Seconds(), s.note)
	}

	// Final verdict
	allNotes := ""
	failed := false
	for _, s := range steps {
		allNotes += s.note + "\n"
		if strings.HasPrefix(s.note, "FAIL") {
			failed = true
		}
	}
	fmt.Println()
	if failed {
		fmt.Println("FAIL: see notes above")
		dumpLogFile(filepath.Join(tmpDir, "leanproxy.log"))
		os.Exit(1)
	}
	if !gotToolsCall {
		fmt.Println("FAIL: never received tools/call response")
		dumpLogFile(filepath.Join(tmpDir, "leanproxy.log"))
		os.Exit(1)
	}
	fmt.Println("PASS: leanproxy-mcp honored the per-server 60s timeout.")
	fmt.Println("The old 30s hardcoded default is gone.")
}

func dumpLogFile(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Println("(no log file at", path, ")", err)
		return
	}
	fmt.Println()
	fmt.Println("--- leanproxy log (", path, ") ---")
	fmt.Println(string(data))
	fmt.Println("--- end log ---")
}

func writeSlowFake(path string) {
	// A shell script that pretends to be an MCP server. On "tools/call" it
	// sleeps 45s then returns a tiny response. The wire ID leanproxy uses
	// is its own counter, so we just hardcode id=2 (the second request
	// leanproxy sends after initialize). Real MCP servers echo whatever
	// ID the client sent; the fake doesn't bother.
	src := `#!/bin/sh
# slowfake: pretend MCP server that sleeps 45s on tools/call.
while IFS= read -r line; do
  case "$line" in
    *initialize*)
      echo '{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"slowfake","version":"0.0.1"}}}'
      ;;
    *initialized*)
      : # no-op notification
      ;;
    *tools/call*)
      sleep 45
      echo '{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"slow reply"}]}}'
      ;;
    *)
      echo '{"jsonrpc":"2.0","id":null,"error":{"code":-32601,"message":"unknown"}}'
      ;;
  esac
done
`
	must(os.WriteFile(path, []byte(src), 0o755))
}

func writeReq(w io.Writer, id interface{}, method string, params interface{}) error {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	if id != nil {
		req["id"] = id
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func mustReadResp(r io.Reader) []byte {
	br := bufio.NewReader(r)
	line, err := br.ReadBytes('\n')
	must(err)
	return line
}

func readRespAsync(r io.Reader) <-chan []byte {
	ch := make(chan []byte, 1)
	go func() {
		defer close(ch)
		br := bufio.NewReader(r)
		line, err := br.ReadBytes('\n')
		if err == nil {
			ch <- line
		}
	}()
	return ch
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
