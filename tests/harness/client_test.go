//go:build harness

package harness

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This file holds the plumbing: building the binaries, an isolated
// environment, and a pipelining JSON-RPC client over a child's stdio.

// binaries are the executables the harness drives.
type binaries struct {
	proxy    string // leanproxy-mcp, built from the repo root
	catalog  string // tests/harness/catalogmcp
	codemode string // leanproxy-mcp built with -tags codemode (#325 spike)
}

// repoRoot is two levels above this package (tests/harness).
func repoRoot(t testing.TB) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func buildBinaries(t testing.TB, dir string) binaries {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatal("go toolchain not found")
	}
	b := binaries{proxy: filepath.Join(dir, "leanproxy-mcp"), catalog: filepath.Join(dir, "catalogmcp"), codemode: filepath.Join(dir, "leanproxy-mcp-codemode")}
	for _, target := range []struct{ out, pkg, tags string }{
		{b.proxy, ".", ""},
		{b.catalog, "./tests/harness/catalogmcp", ""},
		{b.codemode, ".", "codemode"},
	} {
		// Same flags as the release build (Makefile LDFLAGS minus the
		// version stamp), so the reported binary size is the shipped one.
		cmd := exec.Command(goBin, "build", "-trimpath", "-ldflags=-s -w", "-tags="+target.tags, "-o", target.out, target.pkg)
		cmd.Dir = repoRoot(t)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go build %s: %v\n%s", target.pkg, err, out)
		}
	}
	return b
}

// isolatedEnv returns the environment for every child process: the caller's
// environment with HOME and the tool cache pointed at scratch directories,
// so the harness never reads or writes the real home (config, tool cache,
// status file, quarantine).
func isolatedEnv(t testing.TB, home string) []string {
	t.Helper()
	toolcache := filepath.Join(home, "toolcache")
	if err := os.MkdirAll(toolcache, 0o700); err != nil {
		t.Fatal(err)
	}
	env := make([]string, 0, len(os.Environ())+2)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "LEANPROXY_") || strings.HasPrefix(kv, "XDG_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+home, "LEANPROXY_TOOLCACHE_DIR="+toolcache)
}

// rpcError is a JSON-RPC error object.
type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// rpcMsg is any incoming JSON-RPC message.
type rpcMsg struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

// reply is one response, with its raw line (no trailing newline) and the
// time it was read.
type reply struct {
	line []byte
	msg  rpcMsg
	at   time.Time
}

// proc is a child process spoken to over newline-delimited JSON-RPC.
// Requests can be pipelined: every request gets a unique numeric id and its
// own reply channel.
type proc struct {
	t       testing.TB
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex
	nextID  atomic.Int64

	mu      sync.Mutex
	pending map[string]chan reply
	closed  chan struct{}
	stderr  *lockedBuffer
}

type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() < 1<<20 { // keep the first MiB only
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startProc(t testing.TB, env []string, bin string, args ...string) *proc {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	p := &proc{t: t, cmd: cmd, stdin: stdin, pending: make(map[string]chan reply), closed: make(chan struct{}), stderr: &lockedBuffer{}}
	cmd.Stderr = p.stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", bin, err)
	}
	go p.readLoop(stdout)
	t.Cleanup(p.stop)
	return p
}

func (p *proc) readLoop(stdout io.Reader) {
	defer close(p.closed)
	r := bufio.NewReaderSize(stdout, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			p.deliver(line)
		}
		if err != nil {
			return
		}
	}
}

func (p *proc) deliver(line []byte) {
	at := time.Now()
	line = []byte(strings.TrimRight(string(line), "\r\n"))
	var msg rpcMsg
	if err := json.Unmarshal(line, &msg); err != nil {
		return
	}
	if msg.Method != "" {
		if len(msg.ID) > 0 {
			// A request to us (the client): we implement no client
			// features, so answer "method not found" like a minimal client.
			_ = p.write(map[string]interface{}{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]interface{}{"code": -32601, "message": "method not found"}})
		}
		return
	}
	key := string(msg.ID)
	p.mu.Lock()
	ch, ok := p.pending[key]
	delete(p.pending, key)
	p.mu.Unlock()
	if ok {
		ch <- reply{line: line, msg: msg, at: at}
	}
}

func (p *proc) write(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.stdin.Write(append(data, '\n'))
	return err
}

// pendingCall is a request that has been written and awaits its reply.
type pendingCall struct {
	sent    time.Time
	request []byte
	ch      chan reply
}

// send writes one request and returns its pending call.
func (p *proc) send(method string, params interface{}) (*pendingCall, error) {
	id := p.nextID.Add(1)
	req := map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	ch := make(chan reply, 1)
	p.mu.Lock()
	p.pending[strconv.FormatInt(id, 10)] = ch
	p.mu.Unlock()
	pc := &pendingCall{request: data, ch: ch}
	p.writeMu.Lock()
	pc.sent = time.Now()
	_, err = p.stdin.Write(append(data, '\n'))
	p.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write %s: %w", method, err)
	}
	return pc, nil
}

var errTimeout = errors.New("timed out waiting for a response")

// wait blocks for the reply, the child's exit, or the timeout.
func (pc *pendingCall) wait(p *proc, timeout time.Duration) (reply, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-pc.ch:
		return r, nil
	case <-p.closed:
		select {
		case r := <-pc.ch:
			return r, nil
		default:
		}
		return reply{}, errors.New("process closed stdout")
	case <-timer.C:
		return reply{}, errTimeout
	}
}

// call sends one request and waits for its reply. It returns the reply and
// the round-trip time.
func (p *proc) call(method string, params interface{}, timeout time.Duration) (reply, time.Duration, error) {
	pc, err := p.send(method, params)
	if err != nil {
		return reply{}, 0, err
	}
	r, err := pc.wait(p, timeout)
	if err != nil {
		return reply{}, 0, fmt.Errorf("%s: %w", method, err)
	}
	return r, r.at.Sub(pc.sent), nil
}

// mustCall is call that fails the test on a transport error or a JSON-RPC
// error response.
func (p *proc) mustCall(method string, params interface{}) reply {
	p.t.Helper()
	r, _, err := p.call(method, params, 30*time.Second)
	if err != nil {
		p.t.Fatalf("%v\nstderr:\n%s", err, p.stderr.String())
	}
	if r.msg.Error != nil {
		p.t.Fatalf("%s returned an error: %s", method, r.line)
	}
	return r
}

// initialize performs the MCP handshake.
func (p *proc) initialize() {
	p.t.Helper()
	p.mustCall("initialize", map[string]interface{}{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]interface{}{},
		"clientInfo":      map[string]string{"name": "leanproxy-harness", "version": "1"},
	})
	if err := p.write(map[string]string{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		p.t.Fatal(err)
	}
}

func (p *proc) pid() int { return p.cmd.Process.Pid }

// stop closes stdin (EOF is the MCP shutdown signal for stdio) and waits,
// killing the process if it does not exit.
func (p *proc) stop() {
	_ = p.stdin.Close()
	done := make(chan struct{})
	go func() { _ = p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = p.cmd.Process.Kill()
		<-done
	}
}

// rssKB returns the resident set size of pid in KiB from /proc, or -1 where
// /proc is unavailable (non-Linux).
func rssKB(pid int) int {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				if n, err := strconv.Atoi(fields[0]); err == nil {
					return n
				}
			}
		}
	}
	return -1
}

// toolText returns the concatenated text blocks of a tools/call result.
func toolText(r reply) string {
	var res struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if json.Unmarshal(r.msg.Result, &res) != nil {
		return ""
	}
	var sb strings.Builder
	for _, c := range res.Content {
		sb.WriteString(c.Text)
	}
	return sb.String()
}
