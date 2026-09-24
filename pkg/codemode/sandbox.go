//go:build codemode

package codemode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Why a child process and not goja in the proxy: goja can interrupt
// JavaScript, but not its native built-ins, and it has no memory limit.
// `"x".repeat(2**30)` or `new Array(1e9).fill(0)` run in Go code the
// interrupt never reaches, and an out-of-memory in the proxy's own process
// kills every session. In a child process the kernel enforces the limits
// (RLIMIT_AS, RLIMIT_CPU), the proxy kills it on its deadline, and the
// worst a program can do is crash its own sandbox. The child is this same
// binary (the hidden `codemode-worker` command), started with an empty
// environment (no upstream credentials in reach) in the temp directory.

// killGrace is how long past its timeout the proxy lets the sandbox report
// before it kills it.
const killGrace = time.Second

// maxStderr caps what the proxy keeps of the sandbox's stderr (a Go crash
// report), for its logs only: the model never sees it.
const maxStderr = 8 << 10

// WorkerCommand is the hidden command that runs the sandbox process.
const WorkerCommand = "codemode-worker"

// CallFunc makes one tool call on behalf of a program: server, tool and
// the JSON object arguments, returning the MCP tools/call result as the
// client would have received it. It is the program's only capability.
type CallFunc func(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error)

// Options configure one Run.
type Options struct {
	Limits Limits
	// Servers are the names tools.<server> resolves; any other server is
	// refused by the proxy before CallFunc runs.
	Servers []string
	// Command and Args start the sandbox process. Empty Command means
	// this binary with Args {WorkerCommand}.
	Command string
	Args    []string
	// Env is the sandbox's whole environment (nil: empty).
	Env []string
}

// CallRecord is the audit record of one tool call a program made.
type CallRecord struct {
	Server   string
	Tool     string
	Duration time.Duration
	// Err is the error the program got ("" on success).
	Err string
	// ResultBytes is the size of the value handed to the program.
	ResultBytes int
}

// Result is a finished program's answer.
type Result struct {
	Output   string
	Logs     []string
	Calls    []CallRecord
	Duration time.Duration
}

// RunError is a program that did not finish: it threw, or hit a limit.
type RunError struct {
	Kind    string
	Message string
	Logs    []string
	Calls   []CallRecord
}

func (e *RunError) Error() string { return e.Kind + ": " + e.Message }

// Run executes code in a fresh sandbox process and returns its result. A
// program that throws, or hits a limit, is a *RunError.
func Run(ctx context.Context, code string, opts Options, call CallFunc) (*Result, error) {
	l := opts.Limits
	if len(code) > l.MaxCodeBytes {
		return nil, &RunError{Kind: KindError, Message: fmt.Sprintf("the code is %d bytes, over the %d-byte limit", len(code), l.MaxCodeBytes)}
	}
	if strings.TrimSpace(code) == "" {
		return nil, &RunError{Kind: KindError, Message: "no code"}
	}
	start := time.Now()
	s := &session{opts: opts, call: call, servers: make(map[string]bool, len(opts.Servers))}
	for _, name := range opts.Servers {
		s.servers[name] = true
	}
	res, err := s.run(ctx, code)
	if res != nil {
		res.Duration = time.Since(start)
	}
	return res, err
}

type session struct {
	opts    Options
	call    CallFunc
	servers map[string]bool

	writeMu sync.Mutex
	stdin   io.WriteCloser

	mu    sync.Mutex
	calls []CallRecord
	count int
}

func (s *session) run(ctx context.Context, code string) (*Result, error) {
	l := s.opts.Limits
	hardCtx, cancel := context.WithTimeout(ctx, l.Timeout+killGrace)
	defer cancel()

	cmd, err := s.command(hardCtx)
	if err != nil {
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &cappedBuffer{max: maxStderr}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("code mode: start sandbox: %w", err)
	}
	s.stdin = stdin

	// Tool calls run while the program waits; they end with the program.
	callCtx, cancelCalls := context.WithCancel(hardCtx)
	var inflight sync.WaitGroup
	defer func() {
		cancelCalls()
		inflight.Wait()
	}()

	limits := l
	if err := s.send(message{Type: msgRun, Code: code, Servers: s.opts.Servers, Limits: &limits}); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, s.fail(KindCrash, "the sandbox did not start")
	}

	sem := make(chan struct{}, l.MaxConcurrentCalls)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64<<10), maxLine(l))
	var done *message
	for sc.Scan() {
		var m message
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			break
		}
		if m.Type == msgDone {
			done = &m
			break
		}
		if m.Type != msgCall {
			break
		}
		s.mu.Lock()
		s.count++
		over := s.count > l.MaxCalls
		s.mu.Unlock()
		if over { // the worker enforces it too; this is the backstop
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, s.fail(KindCalls, fmt.Sprintf("tool call limit exceeded (%d calls)", l.MaxCalls))
		}
		inflight.Add(1)
		go func(m message) {
			defer inflight.Done()
			s.serveCall(callCtx, sem, m)
		}(m)
	}
	scanErr := sc.Err()
	cancelCalls()
	inflight.Wait()
	_ = stdin.Close()
	if done == nil || scanErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()

	switch {
	case errors.Is(scanErr, bufio.ErrTooLong):
		return nil, s.fail(KindOutput, fmt.Sprintf("output limit exceeded (%d bytes)", l.MaxOutputBytes))
	case done != nil && done.Error != "":
		kind := done.Kind
		if kind == "" {
			kind = KindError
		}
		return nil, s.failLogs(kind, done.Error, done.Logs)
	case done != nil:
		if len(done.Output) > l.MaxOutputBytes {
			return nil, s.fail(KindOutput, fmt.Sprintf("output limit exceeded: the result is %d bytes, the limit is %d", len(done.Output), l.MaxOutputBytes))
		}
		return &Result{Output: done.Output, Logs: done.Logs, Calls: s.records()}, nil
	}
	// No verdict: the sandbox died, or was killed.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	kind := deathKind(hardCtx, waitErr, stderr.String())
	return nil, s.fail(kind, deathMessage(kind, l, stderr.String()))
}

func (s *session) command(ctx context.Context) (*exec.Cmd, error) {
	name, args := s.opts.Command, s.opts.Args
	if name == "" {
		exe, err := workerExecutable()
		if err != nil {
			return nil, fmt.Errorf("code mode: locate the sandbox binary: %w", err)
		}
		name, args = exe, []string{WorkerCommand}
	}
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- the proxy's own binary, fixed arguments
	cmd.Env = append([]string{}, s.opts.Env...)    // never inherit the proxy's environment
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = sysProcAttr()
	cmd.WaitDelay = killGrace
	return cmd, nil
}

// serveCall runs one tool call through CallFunc and answers the sandbox.
func (s *session) serveCall(ctx context.Context, sem chan struct{}, m message) {
	reply := message{Type: msgResult, ID: m.ID}
	rec := CallRecord{Server: m.Server, Tool: m.Tool}
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return
	}
	start := time.Now()
	switch {
	case !s.servers[m.Server]:
		reply.Error = fmt.Sprintf("unknown server %q", m.Server)
	case m.Tool == "":
		reply.Error = "a tool name is required"
	default:
		raw, err := s.call(ctx, m.Server, m.Tool, m.Arguments)
		if err == nil {
			raw, err = toolValue(raw)
		}
		switch {
		case err != nil:
			reply.Error = err.Error()
		case len(raw) > s.opts.Limits.MaxCallResultBytes:
			reply.Error = fmt.Sprintf("the result of %s.%s is %d bytes, over the %d-byte limit of code mode", m.Server, m.Tool, len(raw), s.opts.Limits.MaxCallResultBytes)
		default:
			reply.Value = raw
			rec.ResultBytes = len(raw)
		}
	}
	rec.Duration = time.Since(start)
	rec.Err = reply.Error
	s.mu.Lock()
	s.calls = append(s.calls, rec)
	s.mu.Unlock()
	if ctx.Err() == nil {
		_ = s.send(reply)
	}
}

func (s *session) send(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err = s.stdin.Write(append(data, '\n'))
	return err
}

func (s *session) records() []CallRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CallRecord(nil), s.calls...)
}

func (s *session) fail(kind, msg string) *RunError {
	return &RunError{Kind: kind, Message: msg, Calls: s.records()}
}

func (s *session) failLogs(kind, msg string, logs []string) *RunError {
	e := s.fail(kind, msg)
	e.Logs = logs
	return e
}

// maxLine bounds one line from the sandbox: a done message with its
// output and logs, or a call with its arguments.
func maxLine(l Limits) int {
	n := l.MaxOutputBytes
	if maxArgBytes > n {
		n = maxArgBytes
	}
	// JSON escaping can double a string (and \u escapes make it 6x in
	// the worst case); the logs and the envelope come on top.
	return 6*n + 6*maxLogBytes + 4096
}

// deathKind classifies a sandbox that died without a verdict.
func deathKind(hardCtx context.Context, waitErr error, stderr string) string {
	// RLIMIT_AS refused a mapping: the Go runtime's "out of memory", a
	// cgo build's failed thread stack ("pthread_create failed"), or the
	// race runtime's own failures in a -race build.
	for _, sign := range []string{"out of memory", "cannot allocate memory", "failed to allocate", "pthread_create failed", "address space collisions"} {
		if strings.Contains(stderr, sign) {
			return KindMemory
		}
	}
	if errors.Is(hardCtx.Err(), context.DeadlineExceeded) {
		return KindTimeout
	}
	var ee *exec.ExitError
	if errors.As(waitErr, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() && (ws.Signal() == syscall.SIGKILL || ws.Signal() == syscall.Signal(0x18)) {
			// Killed, and not by the proxy's deadline: the kernel's
			// RLIMIT_CPU hard limit (SIGKILL; 0x18 is SIGXCPU).
			return KindCPU
		}
	}
	return KindCrash
}

func deathMessage(kind string, l Limits, stderr string) string {
	switch kind {
	case KindMemory:
		return fmt.Sprintf("memory limit exceeded (%d MiB): the sandbox was stopped", l.MaxMemoryMB)
	case KindTimeout:
		return fmt.Sprintf("time limit exceeded (%s): the sandbox was killed", l.Timeout)
	case KindCPU:
		return fmt.Sprintf("CPU time limit exceeded (%s): the sandbox was killed", l.CPUTime)
	}
	if first, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n"); first != "" {
		return "the sandbox crashed: " + first
	}
	return "the sandbox crashed"
}

// toolValue is what a program gets for a tools/call result: a result
// flagged isError becomes an error (the stub's promise rejects);
// otherwise its structuredContent, else its text parsed as JSON when it is
// a JSON object or array, else the text as a string. A result with
// neither text nor structured content is handed over whole.
func toolValue(raw json.RawMessage) (json.RawMessage, error) {
	var r struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StructuredContent json.RawMessage `json:"structuredContent"`
		IsError           bool            `json:"isError"`
	}
	if json.Unmarshal(raw, &r) != nil { // not a CallToolResult: hand it over as is
		return raw, nil
	}
	var texts []string
	for _, c := range r.Content {
		if c.Type == "text" {
			texts = append(texts, c.Text)
		}
	}
	text := strings.Join(texts, "\n")
	if r.IsError {
		if text == "" {
			text = "the tool reported an error"
		}
		return nil, errors.New(text)
	}
	if len(r.StructuredContent) > 0 && string(r.StructuredContent) != "null" {
		return r.StructuredContent, nil
	}
	if len(texts) == 0 {
		return raw, nil
	}
	trimmed := bytes.TrimSpace([]byte(text))
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') && json.Valid(trimmed) {
		return trimmed, nil
	}
	return json.Marshal(text)
}

// cappedBuffer keeps the first max bytes written to it.
type cappedBuffer struct {
	mu  sync.Mutex
	max int
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := b.max - b.buf.Len(); room > 0 {
		if len(p) > room {
			b.buf.Write(p[:room])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
