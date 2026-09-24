//go:build codemode

package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The test binary doubles as the sandbox process: started with
// testWorkerEnv set, it serves the worker side instead of running tests.
const testWorkerEnv = "LEANPROXY_CODEMODE_TEST_WORKER"

func TestMain(m *testing.M) {
	if os.Getenv(testWorkerEnv) == "1" {
		os.Exit(ServeWorker(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

func testLimits() Limits {
	l := (*Config)(nil).Limits()
	l.Timeout = 10 * time.Second
	l.CPUTime = 5 * time.Second
	l.MaxMemoryMB = 128 + raceMemoryMB
	return l
}

func testOptions(t *testing.T, l Limits) Options {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Limits:  l,
		Servers: []string{"github", "my-server"},
		Command: exe,
		Args:    []string{"-test.run=^$"},
		Env:     []string{testWorkerEnv + "=1"},
	}
}

// textResult is a CallToolResult with one text block.
func textResult(text string) json.RawMessage {
	data, _ := json.Marshal(map[string]interface{}{"content": []map[string]string{{"type": "text", "text": text}}})
	return data
}

// fakeIssues answers github.list_issues with n issues of the repo in the
// arguments, and records every call.
type fakeUpstream struct {
	mu    sync.Mutex
	calls []string
	delay time.Duration
	live  atomic.Int32
	peak  atomic.Int32
}

func (f *fakeUpstream) call(ctx context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	f.mu.Lock()
	f.calls = append(f.calls, server+"."+tool+" "+string(args))
	f.mu.Unlock()
	n := f.live.Add(1)
	defer f.live.Add(-1)
	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	switch server + "." + tool {
	case "github.list_issues":
		var a struct {
			Repo string `json:"repo"`
		}
		_ = json.Unmarshal(args, &a)
		issues := make([]map[string]interface{}, 0, 5)
		for i := 0; i < 5; i++ {
			issues = append(issues, map[string]interface{}{"repo": a.Repo, "number": i, "comments": (i * 7) % 5, "body": strings.Repeat("x", 100)})
		}
		data, _ := json.Marshal(issues)
		return textResult(string(data)), nil
	case "github.fail":
		data, _ := json.Marshal(map[string]interface{}{"isError": true, "content": []map[string]string{{"type": "text", "text": "boom"}}})
		return data, nil
	case "github.denied":
		return nil, errors.New("Call refused by leanproxy-mcp policy: denied")
	case "github.structured":
		return json.RawMessage(`{"content":[{"type":"text","text":"ignored"}],"structuredContent":{"n":42}}`), nil
	case "my-server.echo":
		return textResult("echo " + string(args)), nil
	}
	return textResult("ok"), nil
}

func run(t *testing.T, l Limits, code string, up *fakeUpstream) (*Result, *RunError) {
	t.Helper()
	if up == nil {
		up = &fakeUpstream{}
	}
	res, err := Run(context.Background(), code, testOptions(t, l), up.call)
	if err == nil {
		return res, nil
	}
	var re *RunError
	if !errors.As(err, &re) {
		t.Fatalf("Run: want a *RunError, got %T %v", err, err)
	}
	return nil, re
}

func mustRun(t *testing.T, code string, up *fakeUpstream) *Result {
	t.Helper()
	res, re := run(t, testLimits(), code, up)
	if re != nil {
		t.Fatalf("Run failed: %v (logs %v)", re, re.Logs)
	}
	return res
}

func wantKind(t *testing.T, re *RunError, kind string) {
	t.Helper()
	if re == nil {
		t.Fatalf("want a %s failure, the program succeeded", kind)
	}
	if re.Kind != kind {
		t.Fatalf("want kind %s, got %s: %s", kind, re.Kind, re.Message)
	}
}

func TestRun_ReturnValues(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{`return "hello"`, "hello"},
		{`return 1 + 2`, "3"},
		{`return {a: [1, 2], b: null}`, `{"a":[1,2],"b":null}`},
		{`const x = 1`, ""},
		{`console.log("seen", {k: 1}); return "ok"`, "ok"},
	} {
		res := mustRun(t, tc.code, nil)
		if res.Output != tc.want {
			t.Errorf("%s: output %q, want %q", tc.code, res.Output, tc.want)
		}
	}
	res := mustRun(t, `console.log("seen", {k: 1}); return 1`, nil)
	if len(res.Logs) != 1 || res.Logs[0] != `seen {"k":1}` {
		t.Fatalf("logs %q", res.Logs)
	}
}

// The motivating use: several calls, filtered in the sandbox, one small
// answer back.
func TestRun_MultiCallFilter(t *testing.T) {
	up := &fakeUpstream{delay: 50 * time.Millisecond}
	code := `
const repos = ["a", "b", "c", "d", "e"];
const lists = await Promise.all(repos.map(repo => tools.github.list_issues({owner: "octo", repo})));
return lists.flat().sort((x, y) => y.comments - x.comments).slice(0, 3).map(i => i.repo + "#" + i.number);`
	res := mustRun(t, code, up)
	var top []string
	if err := json.Unmarshal([]byte(res.Output), &top); err != nil || len(top) != 3 {
		t.Fatalf("output %q: %v", res.Output, err)
	}
	if len(res.Calls) != 5 || len(up.calls) != 5 {
		t.Fatalf("want 5 audited calls, got %d (%d upstream)", len(res.Calls), len(up.calls))
	}
	for _, c := range res.Calls {
		if c.Server != "github" || c.Tool != "list_issues" || c.Err != "" || c.ResultBytes == 0 {
			t.Fatalf("call record %+v", c)
		}
	}
	if peak := up.peak.Load(); peak < 2 || peak > int32(testLimits().MaxConcurrentCalls) {
		t.Fatalf("peak concurrency %d, want 2..%d", peak, testLimits().MaxConcurrentCalls)
	}
}

func TestRun_ToolResults(t *testing.T) {
	res := mustRun(t, `return (await tools.github.structured()).n`, nil)
	if res.Output != "42" {
		t.Fatalf("structuredContent: %q", res.Output)
	}
	res = mustRun(t, `return await tools["my-server"].echo({x: 1})`, nil)
	if res.Output != `echo {"x":1}` {
		t.Fatalf("text result: %q", res.Output)
	}
	// A tool error and a proxy refusal both reject; the program can catch.
	res = mustRun(t, `
const out = [];
for (const t of ["fail", "denied"]) {
  try { await tools.github[t]({}); out.push("no error"); } catch (e) { out.push(e.message); }
}
return out;`, nil)
	if res.Output != `["boom","Call refused by leanproxy-mcp policy: denied"]` {
		t.Fatalf("errors: %s", res.Output)
	}
	if res.Calls[0].Err == "" || res.Calls[1].Err == "" {
		t.Fatalf("errors must be audited: %+v", res.Calls)
	}
	// Uncaught, the refusal fails the run.
	_, re := run(t, testLimits(), `return await tools.github.denied({})`, nil)
	wantKind(t, re, KindError)
	if !strings.Contains(re.Message, "Call refused by leanproxy-mcp policy") {
		t.Fatalf("message: %s", re.Message)
	}
}

func TestRun_UnknownServerAndBadArguments(t *testing.T) {
	up := &fakeUpstream{}
	res := mustRun(t, `
const out = [typeof tools.nope, Object.keys(tools).sort().join(","), Object.keys(tools.github).length];
for (const args of [[1], "x", 3]) {
  try { await tools.github.list_issues(args); out.push("called"); } catch (e) { out.push(e.name); }
}
return out;`, up)
	if res.Output != `["undefined","github,my-server",0,"TypeError","TypeError","TypeError"]` {
		t.Fatalf("output %s", res.Output)
	}
	if len(up.calls) != 0 {
		t.Fatalf("no call may reach the upstream: %v", up.calls)
	}
}

// No ambient authority: nothing beyond the ECMAScript built-ins, tools and
// console.
func TestRun_NoAmbientAuthority(t *testing.T) {
	probes := []string{
		"require", "process", "module", "exports", "global", "Buffer",
		"fetch", "XMLHttpRequest", "WebSocket", "setTimeout", "setInterval",
		"setImmediate", "queueMicrotask", "Deno", "Bun", "os", "fs", "net", "child_process",
		"importScripts", "Worker", "SharedArrayBuffer", "Atomics", "WebAssembly",
	}
	var sb strings.Builder
	sb.WriteString("const out = {};\n")
	for _, p := range probes {
		fmt.Fprintf(&sb, "out[%q] = typeof %s;\n", p, p)
	}
	sb.WriteString(`out.ctor = (function(){ return this }).constructor("return typeof process")();
try { require("fs"); out.requireFs = "loaded"; } catch (e) { out.requireFs = "refused"; }
return out;`)
	res := mustRun(t, sb.String(), nil)
	var got map[string]string
	if err := json.Unmarshal([]byte(res.Output), &got); err != nil {
		t.Fatalf("%v: %s", err, res.Output)
	}
	for _, p := range probes {
		if got[p] != "undefined" {
			t.Errorf("%s is %s inside the sandbox, want undefined", p, got[p])
		}
	}
	if got["ctor"] != "undefined" || got["requireFs"] != "refused" {
		t.Errorf("escape probes: %v", got)
	}
	// There is no module loader: import does not even compile.
	for _, code := range []string{`const fs = await import("fs"); return 1`, `import fs from "fs"; return 1`} {
		if _, re := run(t, testLimits(), code, nil); re == nil || re.Kind != KindError {
			t.Errorf("%s: want a refusal, got %+v", code, re)
		}
	}
}

func TestRun_InfiniteLoopKilledByCPULimit(t *testing.T) {
	l := testLimits()
	l.CPUTime = time.Second
	start := time.Now()
	_, re := run(t, l, `while (true) {}`, nil)
	wantKind(t, re, KindCPU)
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %s to stop", d)
	}
}

func TestRun_WallClockTimeout(t *testing.T) {
	l := testLimits()
	l.Timeout = time.Second
	// A busy loop that also waits on tool calls: the wall clock ends it.
	up := &fakeUpstream{delay: 200 * time.Millisecond}
	start := time.Now()
	_, re := run(t, l, `for (;;) { await tools.github.slow({}); }`, up)
	wantKind(t, re, KindTimeout)
	if d := time.Since(start); d > l.Timeout+killGrace+2*time.Second {
		t.Fatalf("took %s to stop", d)
	}
	// A loop with no calls also stops at the wall clock when the CPU
	// budget is larger.
	l.CPUTime = 30 * time.Second
	_, re = run(t, l, `for (;;) {}`, nil)
	wantKind(t, re, KindTimeout)
}

func TestRun_MemoryLimits(t *testing.T) {
	for name, code := range map[string]string{
		// One native allocation: the interrupt cannot reach it, the
		// kernel limit does.
		"huge string": `return "x".repeat(2 ** 30).length`,
		// Growth in JavaScript: the heap watchdog interrupts it.
		"growing array": `const a = []; for (;;) { a.push(new Array(100000).fill(1)); }`,
		// A giant dense array.
		"giant array": `return new Array(2 ** 28).fill(0).length`,
	} {
		t.Run(name, func(t *testing.T) {
			l := testLimits()
			l.CPUTime = 20 * time.Second
			l.Timeout = 30 * time.Second
			start := time.Now()
			_, re := run(t, l, code, nil)
			if raceEnabled && re != nil && re.Kind == KindCPU {
				t.Skipf("stopped by the CPU limit first under -race: %s", re.Message)
			}
			wantKind(t, re, KindMemory)
			if d := time.Since(start); d > 20*time.Second {
				t.Fatalf("took %s to stop", d)
			}
			t.Logf("%s: %s after %s", re.Kind, re.Message, time.Since(start).Round(time.Millisecond))
		})
	}
}

func TestRun_OutputLimit(t *testing.T) {
	l := testLimits()
	l.MaxOutputBytes = 1024
	_, re := run(t, l, `return "y".repeat(5000)`, nil)
	wantKind(t, re, KindOutput)
	res, re := run(t, l, `return "y".repeat(1000)`, nil)
	if re != nil || len(res.Output) != 1000 {
		t.Fatalf("an output under the limit must pass: %v", re)
	}
	// console.log is capped separately and never fails the run.
	res, re = run(t, l, `for (let i = 0; i < 1000; i++) console.log("z".repeat(100)); return "ok"`, nil)
	if re != nil || res.Output != "ok" {
		t.Fatalf("console flood: %v", re)
	}
	total := 0
	for _, s := range res.Logs {
		total += len(s)
	}
	if total > maxLogBytes+64 {
		t.Fatalf("logs kept %d bytes, cap %d", total, maxLogBytes)
	}
}

func TestRun_CallLimit(t *testing.T) {
	l := testLimits()
	l.MaxCalls = 3
	up := &fakeUpstream{}
	_, re := run(t, l, `for (let i = 0; i < 10; i++) { await tools.github.list_issues({repo: "r" + i}); }`, up)
	wantKind(t, re, KindCalls)
	if len(up.calls) != 3 {
		t.Fatalf("%d calls reached the upstream, limit 3", len(up.calls))
	}
}

func TestRun_ProgramErrors(t *testing.T) {
	for code, fragment := range map[string]string{
		`return (`:                         "syntax error",
		`throw new Error("nope")`:          "nope",
		`await new Promise(() => {})`:      "never finished",
		`null.x`:                           "TypeError",
		`const f = () => f(); return f()`:  "",
		`return {toJSON() { return 1n }}`:  "",
		`const o = {}; o.o = o; return o;`: "JSON",
	} {
		_, re := run(t, testLimits(), code, nil)
		if re == nil {
			t.Errorf("%s: want an error", code)
			continue
		}
		if !strings.Contains(re.Message, fragment) {
			t.Errorf("%s: message %q lacks %q", code, re.Message, fragment)
		}
	}
	l := testLimits()
	l.MaxCodeBytes = 10
	_, re := run(t, l, `return "0123456789"`, nil)
	wantKind(t, re, KindError)
	_, re = run(t, testLimits(), "  ", nil)
	wantKind(t, re, KindError)
}

// The sandbox never inherits the proxy's environment.
func TestSession_CommandEnvironment(t *testing.T) {
	t.Setenv("LEANPROXY_TEST_SECRET", "s3cr3t")
	s := &session{opts: Options{}}
	cmd, err := s.command(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Env == nil || len(cmd.Env) != 0 {
		t.Fatalf("env %v: must be empty, not inherited", cmd.Env)
	}
	if len(cmd.Args) != 2 || cmd.Args[1] != WorkerCommand {
		t.Fatalf("args %v", cmd.Args)
	}
}

func TestToolValue(t *testing.T) {
	for _, tc := range []struct {
		raw, want, err string
	}{
		{`{"content":[{"type":"text","text":"[1,2]"}]}`, `[1,2]`, ""},
		{`{"content":[{"type":"text","text":"plain"}]}`, `"plain"`, ""},
		{`{"content":[{"type":"text","text":"{broken"}]}`, `"{broken"`, ""},
		{`{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`, `"a\nb"`, ""},
		{`{"content":[],"structuredContent":{"k":1}}`, `{"k":1}`, ""},
		{`{"content":[{"type":"image","data":"x"}]}`, `{"content":[{"type":"image","data":"x"}]}`, ""},
		{`{"isError":true,"content":[{"type":"text","text":"bad"}]}`, "", "bad"},
		{`[1]`, `[1]`, ""},
	} {
		got, err := toolValue(json.RawMessage(tc.raw))
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("%s: err %v, want %s", tc.raw, err, tc.err)
			}
			continue
		}
		if err != nil || string(got) != tc.want {
			t.Errorf("%s: got %s %v, want %s", tc.raw, got, err, tc.want)
		}
	}
}
