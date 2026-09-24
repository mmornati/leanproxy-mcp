//go:build codemode

package codemode

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// The sandbox process: one goja runtime running one program, in a child
// process of the proxy (see sandbox.go for why a process). Its only way
// out is its stdout, read by the proxy; a tool call is a request on it.
//
// What the program sees: the ECMAScript built-ins (goja implements ES2015+
// with async/await), a `tools` object and a bounded `console.log`. There
// is no require, no module loader, no filesystem, no network, no timers,
// no process or environment access: goja only has what the embedder adds,
// and this file adds nothing else.

const (
	// maxLogBytes caps what console.log keeps.
	maxLogBytes = 4 << 10
	// maxArgBytes caps the JSON arguments of one tool call.
	maxArgBytes = 256 << 10
	// maxCallStack bounds JavaScript recursion.
	maxCallStack = 4096
	// memoryPoll is how often the memory watchdog samples the heap.
	memoryPoll = 5 * time.Millisecond
	// codeFile is the program's name in stack traces.
	codeFile = "execute_code.js"
)

// limitError is an uncatchable interrupt: the VM stops, the proxy gets
// its kind.
type limitError struct {
	kind string
	msg  string
}

func (e *limitError) Error() string { return e.msg }

// ServeWorker runs the sandbox side of the protocol on in / out and
// returns the process exit code. It is the entry point of the hidden
// `codemode-worker` command.
func ServeWorker(in io.Reader, out io.Writer) int {
	w := &worker{
		in:      bufio.NewReaderSize(in, 64<<10),
		out:     bufio.NewWriterSize(out, 64<<10),
		pending: make(map[int64]pendingCall),
	}
	if err := w.serve(); err != nil {
		fmt.Fprintln(os.Stderr, "codemode worker:", err)
		return 1
	}
	return 0
}

type pendingCall struct {
	resolve, reject func(interface{}) error
}

type worker struct {
	in  *bufio.Reader
	out *bufio.Writer

	limits  Limits
	vm      *goja.Runtime
	servers map[string]bool
	calls   int
	nextID  int64
	pending map[int64]pendingCall

	logs     []string
	logBytes int

	jsonParse, jsonStringify goja.Callable
	errorCtor                goja.Value
	callLimitHit             bool
}

func (w *worker) serve() error {
	var run message
	if err := w.read(&run); err != nil {
		return err
	}
	if run.Type != msgRun || run.Limits == nil {
		return fmt.Errorf("expected a run message, got %q", run.Type)
	}
	w.limits = *run.Limits

	// Limits first: nothing of the program runs before they hold.
	if err := applyOSLimits(w.limits); err != nil {
		return fmt.Errorf("apply limits: %w", err)
	}
	debug.SetMemoryLimit(int64(w.limits.memoryBytes())) // #nosec G115 -- at most 1 TiB
	// One program needs one thread for the VM and one for the watchdogs;
	// fewer threads also means fewer thread stacks under RLIMIT_AS.
	runtime.GOMAXPROCS(2)

	w.servers = make(map[string]bool, len(run.Servers))
	for _, s := range run.Servers {
		w.servers[s] = true
	}
	if err := w.setUpVM(); err != nil {
		return err
	}

	stop := w.watch()
	defer stop()
	output, logs, kind, err := w.execute(run.Code)
	if err != nil && kind == KindError && w.callLimitHit {
		kind = KindCalls
	}
	done := message{Type: msgDone, Output: output, Logs: logs}
	if err != nil {
		done.Error, done.Kind = err.Error(), kind
		done.Output = ""
	}
	return w.write(done)
}

func (w *worker) read(m *message) error {
	line, err := w.in.ReadBytes('\n')
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	return json.Unmarshal(line, m)
}

func (w *worker) write(m message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if _, err := w.out.Write(append(data, '\n')); err != nil {
		return err
	}
	return w.out.Flush()
}

// setUpVM builds the runtime: the built-ins, `tools` and `console` only.
func (w *worker) setUpVM() error {
	vm := goja.New()
	vm.SetMaxCallStackSize(maxCallStack)
	w.vm = vm

	// The program may overwrite JSON.parse and JSON.stringify; the worker
	// keeps the originals.
	jsonObj := vm.Get("JSON").ToObject(vm)
	var ok bool
	if w.jsonParse, ok = goja.AssertFunction(jsonObj.Get("parse")); !ok {
		return errors.New("JSON.parse is not a function")
	}
	if w.jsonStringify, ok = goja.AssertFunction(jsonObj.Get("stringify")); !ok {
		return errors.New("JSON.stringify is not a function")
	}
	w.errorCtor = vm.Get("Error")

	if err := vm.Set("tools", vm.NewDynamicObject(&toolsObject{w: w})); err != nil {
		return err
	}
	console := vm.NewObject()
	if err := console.Set("log", w.consoleLog); err != nil {
		return err
	}
	return vm.Set("console", console)
}

// watch starts the limit watchdogs: the wall clock, the heap and, on
// Unix, the SIGXCPU the kernel sends at the CPU time soft limit. Each
// interrupts the VM with an uncatchable error; the proxy's own hard
// limits (kill on timeout, RLIMIT_CPU hard limit, RLIMIT_AS) back them
// up for the time spent in native code, which Interrupt cannot stop.
func (w *worker) watch() (stop func()) {
	quit := make(chan struct{})
	timer := time.AfterFunc(w.limits.Timeout, func() {
		w.vm.Interrupt(&limitError{KindTimeout, fmt.Sprintf("time limit exceeded (%s)", w.limits.Timeout)})
	})
	xcpu := make(chan os.Signal, 1)
	notifyCPULimit(xcpu)
	go func() {
		limit := w.limits.memoryBytes()
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		tick := time.NewTicker(memoryPoll)
		defer tick.Stop()
		for {
			select {
			case <-quit:
				return
			case <-xcpu:
				w.vm.Interrupt(&limitError{KindCPU, fmt.Sprintf("CPU time limit exceeded (%s)", w.limits.CPUTime)})
			case <-tick.C:
				metrics.Read(sample)
				if sample[0].Value.Kind() == metrics.KindUint64 && sample[0].Value.Uint64() > limit {
					w.vm.Interrupt(&limitError{KindMemory, fmt.Sprintf("memory limit exceeded (%d MiB)", w.limits.MaxMemoryMB)})
				}
			}
		}
	}()
	return func() {
		timer.Stop()
		signal.Stop(xcpu)
		close(quit)
	}
}

// execute runs the program and its tool calls to completion.
func (w *worker) execute(code string) (output string, logs []string, kind string, err error) {
	// The program is the body of an async function: it can `await` tool
	// calls and `return` its answer.
	prog, err := goja.Compile(codeFile, "(async function () {\n"+code+"\n})()", true)
	if err != nil {
		return "", nil, KindError, fmt.Errorf("syntax error: %w", err)
	}
	v, err := w.vm.RunProgram(prog)
	if err != nil {
		return "", w.logs, errorKind(err), jsError(err)
	}
	p, ok := v.Export().(*goja.Promise)
	if !ok {
		return "", w.logs, KindError, errors.New("internal error: the program did not return a promise")
	}
	for p.State() == goja.PromiseStatePending {
		if len(w.pending) == 0 {
			return "", w.logs, KindError, errors.New("the program never finished: it awaits a promise that nothing settles")
		}
		if err := w.settleNext(); err != nil {
			return "", w.logs, errorKind(err), jsError(err)
		}
	}
	if p.State() == goja.PromiseStateRejected {
		return "", w.logs, KindError, fmt.Errorf("uncaught %s", describe(w.vm, p.Result()))
	}
	output, err = w.render(p.Result())
	if err != nil {
		return "", w.logs, errorKind(err), err
	}
	if len(output) > w.limits.MaxOutputBytes {
		return "", w.logs, KindOutput, fmt.Errorf("output limit exceeded: the result is %d bytes, the limit is %d", len(output), w.limits.MaxOutputBytes)
	}
	return output, w.logs, "", nil
}

// settleNext waits for the next tool result from the proxy and settles
// its promise; the promise jobs it unblocks run before it returns.
func (w *worker) settleNext() error {
	var m message
	if err := w.read(&m); err != nil {
		return err
	}
	pc, ok := w.pending[m.ID]
	if m.Type != msgResult || !ok {
		return fmt.Errorf("unexpected message %q id %d", m.Type, m.ID)
	}
	delete(w.pending, m.ID)
	if m.Error != "" {
		return pc.reject(w.newError(m.Error))
	}
	val, err := w.jsonParse(goja.Undefined(), w.vm.ToValue(string(m.Value)))
	if err != nil {
		return pc.reject(w.newError("undecodable tool result: " + err.Error()))
	}
	return pc.resolve(val)
}

// render turns the program's return value into the text the model gets: a
// string as is, anything else as JSON.
func (w *worker) render(v goja.Value) (string, error) {
	if v == nil || goja.IsUndefined(v) {
		return "", nil
	}
	if s, ok := v.Export().(string); ok {
		return s, nil
	}
	out, err := w.jsonStringify(goja.Undefined(), v)
	if err != nil {
		return "", fmt.Errorf("the return value is not JSON-serializable: %w", jsError(err))
	}
	if goja.IsUndefined(out) {
		return "", nil
	}
	return out.String(), nil
}

// call is one tool stub invocation: tools.<server>.<tool>(args).
func (w *worker) call(server, tool string, args goja.Value) goja.Value {
	if w.calls >= w.limits.MaxCalls {
		w.callLimitHit = true
		panic(w.newError(fmt.Sprintf("tool call limit exceeded (%d calls)", w.limits.MaxCalls)))
	}
	raw := "{}"
	if args != nil && !goja.IsUndefined(args) {
		if obj, ok := args.(*goja.Object); !ok || obj.ClassName() == "Array" || obj.ClassName() == "Function" {
			panic(w.vm.NewTypeError("tools.%s.%s: the arguments must be an object", server, tool))
		}
		s, err := w.jsonStringify(goja.Undefined(), args)
		if err != nil {
			panic(err)
		}
		raw = s.String()
	}
	if len(raw) > maxArgBytes {
		panic(w.vm.NewTypeError("tools.%s.%s: the arguments are %d bytes, over the %d-byte limit", server, tool, len(raw), maxArgBytes))
	}
	w.calls++
	w.nextID++
	id := w.nextID
	p, resolve, reject := w.vm.NewPromise()
	w.pending[id] = pendingCall{resolve: resolve, reject: reject}
	if err := w.write(message{Type: msgCall, ID: id, Server: server, Tool: tool, Arguments: json.RawMessage(raw)}); err != nil {
		panic(w.newError(err.Error()))
	}
	return w.vm.ToValue(p)
}

// newError is a plain JavaScript Error (built with the original Error
// constructor, whatever the program did to the global).
func (w *worker) newError(msg string) *goja.Object {
	obj, err := w.vm.New(w.errorCtor, w.vm.ToValue(msg))
	if err != nil {
		return w.vm.NewTypeError(msg)
	}
	return obj
}

func (w *worker) consoleLog(call goja.FunctionCall) goja.Value {
	parts := make([]string, 0, len(call.Arguments))
	for _, a := range call.Arguments {
		if s, ok := a.Export().(string); ok {
			parts = append(parts, s)
			continue
		}
		if out, err := w.jsonStringify(goja.Undefined(), a); err == nil && !goja.IsUndefined(out) {
			parts = append(parts, out.String())
		} else {
			parts = append(parts, a.String())
		}
	}
	line := strings.Join(parts, " ")
	if room := maxLogBytes - w.logBytes; room <= 0 {
		return goja.Undefined()
	} else if len(line) > room {
		line = line[:room] + "…[console output truncated]"
	}
	w.logBytes += len(line)
	w.logs = append(w.logs, line)
	return goja.Undefined()
}

// toolsObject is the global `tools`: tools.<server> is a serverObject for
// every configured server. It lists the servers (Object.keys(tools)) but
// never the tools: which tools exist, and which are allowed, is for the
// proxy's policy to decide on each call, not for the sandbox to enumerate.
type toolsObject struct{ w *worker }

func (t *toolsObject) Get(key string) goja.Value {
	if !t.w.servers[key] {
		return goja.Undefined()
	}
	return t.w.vm.NewDynamicObject(&serverObject{w: t.w, server: key})
}
func (t *toolsObject) Set(string, goja.Value) bool { return false }
func (t *toolsObject) Has(key string) bool         { return t.w.servers[key] }
func (t *toolsObject) Delete(string) bool          { return false }
func (t *toolsObject) Keys() []string {
	keys := make([]string, 0, len(t.w.servers))
	for s := range t.w.servers {
		keys = append(keys, s)
	}
	return keys
}

// serverObject is tools.<server>: any property is a stub calling that
// tool through the proxy.
type serverObject struct {
	w      *worker
	server string
}

func (s *serverObject) Get(key string) goja.Value {
	if key == "" || key == "then" { // not a thenable: `await tools.x` must not call a tool
		return goja.Undefined()
	}
	return s.w.vm.ToValue(func(call goja.FunctionCall) goja.Value {
		return s.w.call(s.server, key, call.Argument(0))
	})
}
func (s *serverObject) Set(string, goja.Value) bool { return false }
func (s *serverObject) Has(key string) bool         { return key != "" && key != "then" }
func (s *serverObject) Delete(string) bool          { return false }
func (s *serverObject) Keys() []string              { return nil }

// errorKind maps a run error to its failure kind.
func errorKind(err error) string {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		if le, ok := ie.Value().(*limitError); ok {
			return le.kind
		}
		return KindTimeout
	}
	var so *goja.StackOverflowError
	if errors.As(err, &so) {
		return KindMemory
	}
	return KindError
}

// jsError is err's message, without goja's Go-side decorations.
func jsError(err error) error {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) {
		if le, ok := ie.Value().(*limitError); ok {
			return le
		}
		return fmt.Errorf("interrupted: %v", ie.Value())
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		return errors.New(ex.Error())
	}
	return err
}

// describe renders a rejection reason.
func describe(vm *goja.Runtime, v goja.Value) string {
	if v == nil {
		return "rejection"
	}
	if obj, ok := v.(*goja.Object); ok {
		if stack := obj.Get("stack"); stack != nil && !goja.IsUndefined(stack) {
			return stack.String()
		}
	}
	return v.String()
}

// memoryBytes is the memory limit in bytes (the default for a
// non-positive value).
func (l Limits) memoryBytes() uint64 {
	mb := l.MaxMemoryMB
	if mb <= 0 || mb > 1<<20 {
		mb = DefaultMaxMemoryMB
	}
	return uint64(mb) << 20 // #nosec G115 -- 0 < mb <= 1<<20
}
