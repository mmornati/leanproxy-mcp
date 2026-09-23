package pool

import (
	"bytes"
	"context"
	"encoding/json"
	errstd "errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	errs "github.com/mmornati/leanproxy-mcp/pkg/errors"
)

const (
	stateIdle int32 = iota
	stateRunning
	stateBusy
	stateStopping
	stateStopped
	stateStarting
	stateError
)

type StdioServerConfig struct {
	Name    string
	Command string
	Args    []string
	// Env holds explicit "KEY=VALUE" entries added on top of the minimal
	// base environment (#311). A value may reference "${VAR}", expanded
	// from the parent (proxy) environment at spawn time.
	Env []string
	CWD string
	// EnvPassthrough lists parent environment variable names copied to the
	// child as-is, in addition to the minimal base environment and Env
	// (#311).
	EnvPassthrough []string
	// InheritEnv restores full inheritance of the proxy's environment
	// (pre-#311 behavior), logging one warning per spawn.
	InheritEnv bool
	// MaxInFlight caps the number of requests multiplexed concurrently
	// over the server's stdio pipe. Callers beyond the cap wait (respecting
	// their context) instead of being rejected. 0 means
	// DefaultMaxInFlight.
	MaxInFlight    int
	IdleTimeout    time.Duration
	RequestTimeout time.Duration
	// MaxResponseSize caps one JSON-RPC message read from the server's
	// stdout (max_response_bytes). A longer message fails only the request
	// it answers. 0 means DefaultMaxResponseBytes.
	MaxResponseSize int
	// StopGracePeriod is how long stop waits after SIGTERM before sending
	// SIGKILL to the server's process group. 0 means
	// defaultStopGracePeriod.
	StopGracePeriod time.Duration
	// ClientCapabilities is what the pool declares to the server in its
	// initialize handshake (see UpstreamClientCapabilities).
	ClientCapabilities mcp.ClientCapabilities
}

type ServerHandle struct {
	Name  string
	State ServerState
	Stats ServerStats
}

type ServerStats struct {
	RequestCount   int64
	ErrorCount     int64
	AvgLatencyMs   float64
	LastRequestAt  time.Time
	RestartCount   int
	CurrentBackoff time.Duration
	LastError      string
	LastErrorAt    time.Time
}

// stderrRing captures the most recent stderr lines for diagnostics.
type stderrRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func newStderrRing(max int) *stderrRing {
	return &stderrRing{max: max}
}

func (r *stderrRing) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) >= r.max {
		r.lines = r.lines[1:]
	}
	r.lines = append(r.lines, line)
}

func (r *stderrRing) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.lines) == 0 {
		return "(no stderr output)"
	}
	return strings.Join(r.lines, "\n")
}

type StdioServerV2 struct {
	name    string
	config  StdioServerConfig
	process *exec.Cmd
	pgid    int
	// conn is the current process generation's multiplexed JSON-RPC
	// connection (pending map + serialized stdin writer). Guarded by mu.
	conn            *stdioConn
	mu              sync.Mutex
	state           int32
	stats           ServerStats
	restartCount    int
	maxRestarts     int
	backoff         time.Duration
	initialBackoff  time.Duration
	stableWindow    time.Duration
	lastRequestAt   time.Time
	lastSpawnAt     time.Time
	idleTimeout     time.Duration
	requestTimeout  time.Duration
	maxInFlight     int
	maxResponseSize int
	stopGrace       time.Duration
	// redact scrubs secrets from stderr lines before they are kept or
	// logged (bouncer.RedactSecrets by default).
	redact func(string) string
	// slots is the per-server in-flight semaphore (capacity maxInFlight).
	slots chan struct{}
	// inFlight counts requests holding a slot. Guarded by mu.
	inFlight     int
	healthTicker *time.Ticker
	genStopCh    chan struct{}
	genStopOnce  *sync.Once
	restartMu    sync.Mutex
	logger       *slog.Logger
	wg           sync.WaitGroup
	stderrLines  *stderrRing
	// initResult is the InitializeResult of the most recent successful MCP
	// handshake (kept across generations until the next one succeeds).
	initResult atomic.Pointer[InitializeResult]
	// events receives this server's lifecycle events (set by the pool).
	events *eventHub
	// messages receives the requests and notifications the server
	// initiates (set by the pool; issue #308).
	messages *messageHub
	// autoRestartDisabled is the reconnect.enabled=false master switch: the
	// crash path (scheduleRestart) leaves the server in the error state
	// instead of respawning it. Explicit restarts (request/manual) still work.
	autoRestartDisabled bool
	// autoRestartExhausted is set when the crash-restart budget is spent; the
	// server then stays in the error state until a deliberate restart resets
	// the budget.
	autoRestartExhausted atomic.Bool
	// closed is set by the pool before stopping the server for good; restart
	// aborts on it so a late respawn can never orphan a process.
	closed atomic.Bool
	// generation increments on every spawn; restart uses it to skip redundant
	// stop+spawn cycles when a concurrent caller already recovered the server.
	generation atomic.Uint64
	// nextRequestID generates the internal wire IDs used toward the child
	// process so responses can be matched to the exact in-flight request.
	nextRequestID atomic.Int64
}

func newServerV2(name string, config StdioServerConfig, logger *slog.Logger) *StdioServerV2 {
	if logger == nil {
		logger = slog.Default()
	}

	maxInFlight := config.MaxInFlight
	if maxInFlight <= 0 {
		maxInFlight = DefaultMaxInFlight
	}

	idleTimeout := config.IdleTimeout
	// idleTimeout == 0 means disabled (no idle timeout); set idle_timeout: "0" in config
	// idleTimeout < 0 falls back to 30m default (should not happen in practice)
	if idleTimeout < 0 {
		idleTimeout = 30 * time.Minute
	}

	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = 30 * time.Second
	}

	maxResponseSize := config.MaxResponseSize
	if maxResponseSize <= 0 {
		maxResponseSize = DefaultMaxResponseBytes
	}

	stopGrace := config.StopGracePeriod
	if stopGrace <= 0 {
		stopGrace = defaultStopGracePeriod
	}

	return &StdioServerV2{
		name:            name,
		config:          config,
		state:           stateIdle,
		stats:           ServerStats{},
		maxRestarts:     5,
		backoff:         time.Second,
		initialBackoff:  time.Second,
		stableWindow:    2 * time.Minute,
		idleTimeout:     idleTimeout,
		requestTimeout:  requestTimeout,
		maxInFlight:     maxInFlight,
		slots:           make(chan struct{}, maxInFlight),
		maxResponseSize: maxResponseSize,
		stopGrace:       stopGrace,
		redact:          bouncer.RedactSecrets,
		healthTicker:    time.NewTicker(30 * time.Second),
		logger:          logger,
		stderrLines:     newStderrRing(50),
	}
}

func (s *StdioServerV2) getState() ServerState {
	return toServerState(atomic.LoadInt32(&s.state))
}

// applyReconnect applies reconnect settings to this server. It only overrides
// values that were explicitly provided (non-zero).
func (s *StdioServerV2) applyReconnect(settings ReconnectSettings) {
	settings = settings.validate()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.autoRestartDisabled = settings.Disabled
	s.maxRestarts = settings.MaxRestartAttempts
	s.initialBackoff = settings.RestartBackoff
	s.backoff = settings.RestartBackoff
	s.stableWindow = settings.StableWindow
	s.stats.CurrentBackoff = s.backoff
}

func toServerState(state int32) ServerState {
	switch state {
	case stateIdle:
		return StateIdle
	case stateRunning:
		return StateRunning
	case stateBusy:
		return StateBusy
	case stateStopping:
		return StateStopping
	case stateStopped:
		return StateStopped
	case stateStarting:
		return StateStarting
	case stateError:
		return StateError
	default:
		return StateUnknown
	}
}

// currentConn returns the current process generation's connection and
// generation number.
func (s *StdioServerV2) currentConn() (*stdioConn, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn, s.generation.Load()
}

// IsMCPInitialized reports whether the current process generation completed
// its MCP initialize handshake.
func (s *StdioServerV2) IsMCPInitialized() bool {
	conn, _ := s.currentConn()
	return conn != nil && conn.handshake.initialized()
}

// SetMCPInitialized marks the current process generation as initialized
// without performing the handshake. The pool performs the handshake itself;
// this only exists for callers (tests) that set the session up out of band.
func (s *StdioServerV2) SetMCPInitialized() {
	if conn, _ := s.currentConn(); conn != nil {
		conn.handshake.markInitialized()
	}
}

// InitializeResult returns the stored result of the most recent successful
// MCP handshake, or nil.
func (s *StdioServerV2) InitializeResult() *InitializeResult {
	return s.initResult.Load()
}

// handleServerNotification is the stdout reader's hook for server
// notifications of generation gen: list changes become server events,
// everything else goes to the message handler (progress, resource
// updates, ...).
func (s *StdioServerV2) handleServerNotification(method string, params json.RawMessage, gen uint64) {
	if kind, ok := remoteNotificationEvent(method); ok {
		s.logger.Info("server list changed", "name", s.name, "generation", gen, "event", kind.String())
		s.events.emit(ServerEvent{Server: s.name, Kind: kind, Generation: gen})
		return
	}
	s.messages.notify(context.Background(), s.name, method, params)
}

// spawn starts a new process generation. It serializes against concurrent
// restarts via restartMu so that only one process generation is ever spawned
// at a time.
func (s *StdioServerV2) spawn(ctx context.Context) error {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	return s.spawnLocked(ctx)
}

func (s *StdioServerV2) spawnLocked(ctx context.Context) error {
	s.mu.Lock()

	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateRunning || currentState == stateBusy || currentState == stateStarting {
		s.mu.Unlock()
		return fmt.Errorf("pool: cannot spawn server in state %s", toServerState(currentState))
	}

	atomic.StoreInt32(&s.state, stateStarting)

	// Use a context that cannot be canceled by a short-lived request scope so
	// the spawned process is never killed because the caller that triggered a
	// restart timed out.
	genCtx := context.WithoutCancel(ctx)

	cmd := exec.CommandContext(genCtx, s.config.Command, s.config.Args...) // #nosec G204 -- the command is the operator's own configured MCP server
	// Build a least-privilege environment (#311): a minimal allowlist from
	// the proxy's own environment, plus the server's own env_passthrough
	// names and explicit env (with ${VAR} expansion), plus
	// PYTHONUNBUFFERED=1. inherit_env: true opts a server back into the
	// old full-inheritance behavior.
	env, err := buildChildEnv(os.Environ(), s.config, s.logger)
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		return fmt.Errorf("pool: %w", err)
	}
	cmd.Env = env
	if s.config.CWD != "" {
		cmd.Dir = s.config.CWD
	}

	configureProcAttr(cmd)

	// stdin is an os.Pipe we own (rather than cmd.StdinPipe) so the write
	// end is a real *os.File: on Unix its writes honor deadlines, which is
	// how a caller is never blocked past its context by a child that
	// stopped reading stdin (see stdioConn.writeLine).
	stdinR, stdin, err := os.Pipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		return fmt.Errorf("pool: stdin pipe: %w", err)
	}
	cmd.Stdin = stdinR
	closeStdin := func() {
		_ = stdinR.Close()
		_ = stdin.Close()
	}

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		closeStdin()
		return fmt.Errorf("pool: stdout pipe: %w", err)
	}

	stderrR, err := cmd.StderrPipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		closeStdin()
		return fmt.Errorf("pool: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		closeStdin()
		s.logger.Error("failed to start server process",
			"name", s.name,
			"command", s.config.Command,
			"args", s.config.Args,
			"error", err)
		return fmt.Errorf("pool: start %s: %w", s.name, err)
	}

	// The child holds its own copy of the read end; ours must be closed so
	// writes fail (instead of blocking) once the child is gone.
	_ = stdinR.Close()

	s.process = cmd
	pgid := processGroupID(cmd.Process)
	s.pgid = pgid
	atomic.StoreInt32(&s.state, stateIdle)
	s.backoff = s.initialBackoff
	s.lastSpawnAt = time.Now()
	s.lastRequestAt = time.Now()
	s.stats.RestartCount++
	s.stats.CurrentBackoff = s.backoff

	// Each spawn gets a fresh lifecycle generation: a dedicated stop channel
	// (guarded by its own once) so that closing the previous generation can
	// never leak into a newly spawned process.
	genStopCh := make(chan struct{})
	genStopOnce := &sync.Once{}
	s.genStopCh = genStopCh
	s.genStopOnce = genStopOnce

	// A fresh connection (and pending map) per generation: late answers to
	// requests of a previous generation can never reach a new waiter.
	conn := newStdioConn(s.name, stdin, s.logger)
	gen := s.generation.Add(1)
	conn.onNotification = func(method string, params json.RawMessage) {
		s.handleServerNotification(method, params, gen)
	}
	conn.onRequest = func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, *errs.JSONRPCError) {
		return s.messages.request(ctx, s.name, method, params)
	}
	s.conn = conn

	// Args are redacted (#304): a server is routinely started with a
	// secret on its command line (e.g. --api-key=...), and this line is
	// logged at Info, unconditionally.
	s.logger.Info("server spawned", "name", s.name, "pid", cmd.Process.Pid, "pgid", s.pgid, "command", s.config.Command, "args", redactArgs(s.config.Args))

	s.mu.Unlock()

	// stderr is drained for as long as the pipe is open, independent of
	// the generation's stop channel: a child whose stderr is not drained
	// blocks on its next write.
	go drainStderr(stderrR, s.captureStderrLine)
	s.wg.Add(1)
	go s.waitForExit(genCtx, cmd, conn, stdin, genStopCh, genStopOnce)
	s.wg.Add(1)
	go s.readResponses(conn, stdoutR, cmd, pgid, genStopCh)
	s.wg.Add(1)
	go s.runIdleLoop(genCtx, genStopCh)

	// Post-spawn verification: confirm process is alive.
	if !processAlive(cmd.Process) {
		// Tear down the generation we just started so a failed spawn never
		// leaks a process and its goroutines outside the pool's view.
		// waitForExit observes the kill and drives the normal crash-recovery
		// path (bounded by the restart budget).
		genStopOnce.Do(func() { close(genStopCh) })
		killProcessTree(cmd.Process, pgid)
		atomic.StoreInt32(&s.state, stateError)
		return fmt.Errorf("pool: server %s process not alive after spawn (recent stderr: %s)", s.name, s.stderrLines.String())
	}

	// A new generation needs a new handshake (done lazily by the first
	// request) and may expose a different tool list.
	s.events.emit(ServerEvent{Server: s.name, Kind: EventSessionStarted, Generation: gen})
	return nil
}

func (s *StdioServerV2) waitForExit(ctx context.Context, cmd *exec.Cmd, conn *stdioConn, stdin io.Closer, stopCh chan struct{}, stopOnce *sync.Once) {
	err := cmd.Wait()
	// We own the stdin write end (see spawnLocked). Closing it also
	// releases any writer still blocked on the dead child's full pipe.
	_ = stdin.Close()

	// Fail every request still waiting on this generation right away (the
	// stdout reader normally does this first on EOF; this is the backstop).
	conn.fail(s.exitedError())

	// Signal the rest of this generation that the process is gone so readers
	// and the idle loop exit and never serve a dead process.
	stopOnce.Do(func() { close(stopCh) })

	s.mu.Lock()
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping {
		atomic.StoreInt32(&s.state, stateStopped)
		s.mu.Unlock()
		s.wg.Done()
		return
	}

	// If the previous generation lived for a stable period, the restart
	// budget resets so a single healthy run grants fresh restart attempts
	// instead of inheriting a stale budget from an earlier crash loop.
	if !s.lastSpawnAt.IsZero() && time.Since(s.lastSpawnAt) > s.stableWindow {
		s.restartCount = 0
	}

	atomic.StoreInt32(&s.state, stateError)

	errorMsg := "unknown"
	if err != nil {
		errorMsg = err.Error()
		s.stats.LastError = errorMsg
		s.stats.LastErrorAt = time.Now()
		s.stats.ErrorCount++
	}
	restartCount := s.restartCount
	pid := 0
	if s.process != nil && s.process.Process != nil {
		pid = s.process.Process.Pid
	}

	s.mu.Unlock()

	s.logger.Error("server process crashed",
		"name", s.name,
		"error", errorMsg,
		"pid", pid,
		"state", currentState,
		"restart_count", restartCount)

	// Run the restart loop in its own goroutine so that a concurrent
	// request-triggered restart (which holds restartMu across stop+spawn) can
	// never deadlock against the crash path waiting on this goroutine.
	go s.scheduleRestart(ctx)
	s.wg.Done()
}

const (
	// DefaultMaxInFlight is the default per-server cap on requests
	// multiplexed concurrently over one stdio pipe (max_in_flight).
	DefaultMaxInFlight = 32
	// maxRestartBackoff caps the exponential crash-restart backoff.
	maxRestartBackoff = time.Minute
	// minRestartBackoff floors the configured backoff so the jitter math can
	// never panic and a crash loop cannot spin faster than this.
	minRestartBackoff = 10 * time.Millisecond
	// defaultStopGracePeriod is how long stopLocked waits for a SIGTERMed
	// process group to wind down before escalating to SIGKILL.
	defaultStopGracePeriod = 5 * time.Second
	// readerDeathGrace is how long a dead stdout reader waits for the
	// process to exit on its own before killing it.
	readerDeathGrace = time.Second
)

func (s *StdioServerV2) scheduleRestart(ctx context.Context) {
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping || currentState == stateStopped {
		return
	}

	s.mu.Lock()
	if s.autoRestartDisabled {
		s.mu.Unlock()
		s.logger.Warn("auto-reconnect disabled, leaving crashed server in error state", "name", s.name)
		atomic.StoreInt32(&s.state, stateError)
		return
	}
	s.restartCount++
	if s.restartCount > s.maxRestarts {
		s.autoRestartExhausted.Store(true)
		restarts := s.restartCount
		s.mu.Unlock()
		s.logger.Error("max restarts exceeded, leaving server in error state until next use", "name", s.name, "restarts", restarts)
		atomic.StoreInt32(&s.state, stateError)
		return
	}

	backoff := s.backoff
	// Defensive clamp: config validation bounds the initial backoff, but the
	// doubling below must also be overflow-safe for pathological values.
	if backoff < minRestartBackoff {
		backoff = minRestartBackoff
	}
	if backoff > maxRestartBackoff {
		backoff = maxRestartBackoff
	}
	if s.backoff > maxRestartBackoff/2 {
		s.backoff = maxRestartBackoff
	} else {
		s.backoff *= 2
	}
	s.stats.CurrentBackoff = s.backoff
	attempt := s.restartCount
	s.mu.Unlock()

	s.logger.Info("scheduled restart", "name", s.name, "backoff", backoff, "attempt", attempt)

	// Add jitter so a fleet of crashed servers does not restart in lockstep.
	quarter := int64(backoff / 4)
	if quarter < 1 {
		quarter = 1
	}
	wait := backoff + time.Duration(rand.Int63n(quarter)+1) // #nosec G404 -- jitter only de-synchronizes restart timers; not security-sensitive

	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return
	}

	// Serialize the respawn against request-triggered restarts (server.restart
	// holds restartMu across stop+spawn). Only respawn while the server is
	// still in the error state set by waitForExit; if another path already
	// recovered the server, there is nothing left to do. Holding restartMu
	// around the check makes the decision atomic, which also removes the
	// deadlock window where stop() waited for this goroutine while we waited
	// for the mutex it held.
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	if atomic.LoadInt32(&s.state) != stateError {
		return
	}

	if err := s.spawnLocked(ctx); err != nil {
		s.logger.Error("restart failed", "name", s.name, "error", err)
		// Re-arm the retry so a transient spawn failure (e.g. port/temp dir
		// contention) does not strand the server in a dead state.
		s.mu.Lock()
		currentState = atomic.LoadInt32(&s.state)
		if currentState != stateStopping && currentState != stateStopped {
			s.mu.Unlock()
			go s.scheduleRestart(ctx)
			return
		}
		s.mu.Unlock()
	}
}

// restart tears down the current process generation and spawns a fresh one.
// It is serialized via restartMu so concurrent callers never create more than
// one generation at a time. A deliberate restart (request- or health-triggered)
// grants a fresh crash-restart budget.
func (s *StdioServerV2) restart(ctx context.Context) error {
	gen0 := s.generation.Load()

	s.restartMu.Lock()
	defer s.restartMu.Unlock()

	// The pool closed this server while we were waiting on the mutex; spawning
	// now would orphan a process that no Close sweep will ever see.
	if s.closed.Load() {
		return fmt.Errorf("pool: server %s is closed", s.name)
	}

	// Another caller already restarted the server while we were waiting and
	// it is healthy again: skip the redundant stop+spawn cycle.
	if s.generation.Load() != gen0 && s.isHealthy() {
		return nil
	}

	// A deliberate restart resets the crash-restart budget: active use (or an
	// operator action) grants the server a fresh set of attempts instead of
	// carrying stale credit from an earlier crash loop.
	s.mu.Lock()
	s.restartCount = 0
	s.backoff = s.initialBackoff
	s.stats.CurrentBackoff = s.backoff
	s.mu.Unlock()
	s.autoRestartExhausted.Store(false)

	if err := s.stopLocked(); err != nil {
		return err
	}

	time.Sleep(200 * time.Millisecond)

	// Force the state machine through stopping→stopped (waitForExit normally
	// performs this transition, but it may not have run yet).
	if atomic.LoadInt32(&s.state) == stateStopping {
		atomic.StoreInt32(&s.state, stateStopped)
	}

	if err := s.spawnLocked(ctx); err != nil {
		return err
	}

	return nil
}

// exitedError is the error delivered to every request pending on a process
// generation that went away.
func (s *StdioServerV2) exitedError() error {
	return fmt.Errorf("pool: server %s %w (recent stderr: %s)", s.name, errServerExited, s.stderrLines.String())
}

// readResponses is the single stdout reader of a process generation. It
// hands every line to conn.handleLine, which decodes it once and delivers it
// to its waiter. A line over max_response_bytes is discarded up to its
// newline and fails only the request it answers. When the reader ends for
// any reason other than the generation being stopped, every pending waiter
// is failed immediately via conn.fail and the process is never left alive
// with nobody reading its stdout (see handleReaderDeath).
func (s *StdioServerV2) readResponses(conn *stdioConn, stdout io.Reader, cmd *exec.Cmd, pgid int, stopCh chan struct{}) {
	defer s.wg.Done()

	lr := newLineReader(stdout, s.maxResponseSize)
	for {
		line, over, err := lr.next()
		if err != nil {
			// Evaluated now so the error carries the stderr lines captured
			// up to the moment the reader died.
			conn.fail(s.exitedError())
			s.logger.Debug("stdout reader ended", "name", s.name, "error", err)
			s.handleReaderDeath(cmd, pgid, stopCh)
			return
		}
		select {
		case <-stopCh:
			conn.fail(s.exitedError())
			return
		default:
		}
		if over != nil {
			s.handleOversized(conn, over)
			continue
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		s.logger.Debug("read from server stdout", "name", s.name, "bytes", len(line))
		conn.handleLine(line)
	}
}

// handleOversized fails the request a discarded oversized line answered:
// the one whose "id" could be recovered from the line, or else the oldest
// pending request.
func (s *StdioServerV2) handleOversized(conn *stdioConn, over *oversizedLine) {
	id, haveID := oversizedResponseID(over)
	err := fmt.Errorf("response from %s exceeded max_response_bytes (%d)", s.name, s.maxResponseSize)
	failed, ok := conn.failRequest(id, haveID, err)
	s.logger.Error("discarded response larger than max_response_bytes",
		"name", s.name,
		"size", over.size,
		"max_response_bytes", s.maxResponseSize,
		"id_found", haveID,
		"failed_request", ok,
		"wire_id", failed)
}

// handleReaderDeath runs when the stdout reader ends (EOF or read error)
// while the generation was not being stopped. Normally the process is
// exiting and waitForExit takes over. If it is still alive after
// readerDeathGrace — it closed stdout, or the read failed — nobody can read
// its answers any more: mark the server errored and kill its process group
// so waitForExit drives the usual crash-restart path (scheduleRestart).
func (s *StdioServerV2) handleReaderDeath(cmd *exec.Cmd, pgid int, stopCh chan struct{}) {
	select {
	case <-stopCh:
		return
	case <-time.After(readerDeathGrace):
	}
	select {
	case <-stopCh:
		return
	default:
	}
	s.logger.Error("server stdout closed while the process is still running; killing it so it can be restarted",
		"name", s.name, "pid", cmd.Process.Pid)
	s.markErrored()
	killProcessTree(cmd.Process, pgid)
}

// markErrored moves a live server to the error state (a stopping server is
// left alone).
func (s *StdioServerV2) markErrored() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, from := range []int32{stateIdle, stateBusy, stateRunning} {
		if atomic.CompareAndSwapInt32(&s.state, from, stateError) {
			return
		}
	}
}

// captureStderrLine records one (already truncated) stderr line: redacted,
// appended to the diagnostics ring, and logged at Debug only, because
// stderr often carries tokens.
func (s *StdioServerV2) captureStderrLine(line []byte, truncated int) {
	if len(bytes.TrimSpace(line)) == 0 && truncated == 0 {
		return
	}
	text := formatStderrLine(line, truncated)
	if s.redact != nil {
		text = s.redact(text)
	}
	s.stderrLines.add(text)
	s.logger.Debug("server stderr", "name", s.name, "output", text)
}

// redactArgs returns a copy of args with secrets scrubbed via the built-in
// redactor, so a server started with a secret on its command line (e.g.
// --api-key=...) never puts it, unredacted, in the "server spawned" log
// line (#304).
func redactArgs(args []string) []string {
	if len(args) == 0 {
		return args
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = bouncer.RedactSecrets(a)
	}
	return out
}

func (s *StdioServerV2) stop() error {
	// Serialize against spawnLocked (which registers goroutines on s.wg). If a
	// concurrent respawn were to call wg.Add while this call waits on s.wg, the
	// WaitGroup would be used concurrently — a data race.
	s.restartMu.Lock()
	defer s.restartMu.Unlock()
	return s.stopLocked()
}

// stopLocked is stop() and must only be called while holding restartMu.
func (s *StdioServerV2) stopLocked() error {
	s.mu.Lock()
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping || currentState == stateStopped {
		s.mu.Unlock()
		return nil
	}
	atomic.StoreInt32(&s.state, stateStopping)

	stopCh := s.genStopCh
	stopOnce := s.genStopOnce
	proc := s.process
	pgid := s.pgid
	conn := s.conn
	grace := s.stopGrace
	s.mu.Unlock()

	// Fail in-flight requests right away instead of letting them hang until
	// their timeout while this generation is torn down.
	if conn != nil {
		conn.fail(fmt.Errorf("pool: server %s is stopping", s.name))
	}

	if stopCh != nil && stopOnce != nil {
		stopOnce.Do(func() { close(stopCh) })
	}

	var osProc *os.Process
	if proc != nil {
		osProc = proc.Process
	}
	deadline := time.Now().Add(grace)
	if osProc != nil {
		// SIGTERM the whole process group so wrapper-spawned grandchildren
		// (npx, uvx, docker run, sh -c ...) go down with the server.
		if err := terminateProcessTree(osProc, pgid); err != nil {
			s.logger.Debug("SIGTERM failed", "name", s.name, "error", err)
		}
	}

	// Wait for the generation's goroutines to wind down, but escalate to
	// SIGKILL if the child ignores SIGTERM — otherwise a wedged process (the
	// exact case the health checker exists to recover from) would hang this
	// wait forever with restartMu held, blocking all future restarts.
	wgDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(wgDone)
	}()
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-wgDone:
	case <-timer.C:
		if osProc != nil {
			s.logger.Warn("server ignored SIGTERM, escalating to SIGKILL", "name", s.name)
			killProcessTree(osProc, pgid)
		}
		<-wgDone
	}

	// The direct child is gone; give the rest of its process group what is
	// left of the grace period, then SIGKILL any straggler.
	for processGroupAlive(pgid) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processGroupAlive(pgid) {
		s.logger.Warn("server's child processes ignored SIGTERM, escalating to SIGKILL", "name", s.name)
		killProcessTree(nil, pgid)
	}

	return nil
}

func (s *StdioServerV2) isHealthy() bool {
	currentState := atomic.LoadInt32(&s.state)
	return currentState == stateIdle || currentState == stateRunning || currentState == stateBusy
}

// isIdle reports whether the server is alive with no request in flight.
func (s *StdioServerV2) isIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	currentState := atomic.LoadInt32(&s.state)
	return s.inFlight == 0 && (currentState == stateIdle || currentState == stateRunning)
}

// inFlightCount returns the number of requests currently holding an
// in-flight slot on this server.
func (s *StdioServerV2) inFlightCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight
}

// failedSinceSpawn reports whether a request has failed in the current
// process generation. The health checker uses it to detect servers that are
// alive but wedged before completing the MCP initialize handshake.
func (s *StdioServerV2) failedSinceSpawn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.stats.LastErrorAt.IsZero() && s.stats.LastErrorAt.After(s.lastSpawnAt)
}

func (s *StdioServerV2) getStats() ServerStats {
	s.mu.Lock()
	stats := s.stats
	s.mu.Unlock()
	return stats
}

// acquireSlot takes one of the server's max_in_flight slots, waiting (and
// respecting ctx) when the server is at its cap. The returned release func
// must be called exactly once. While at least one request holds a slot the
// server reports the busy state.
func (s *StdioServerV2) acquireSlot(ctx context.Context) (func(), error) {
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	s.mu.Lock()
	s.inFlight++
	// Only a live server transitions to busy: an unconditional store would
	// overwrite the error state set by waitForExit when the child dies.
	if !atomic.CompareAndSwapInt32(&s.state, stateIdle, stateBusy) {
		atomic.CompareAndSwapInt32(&s.state, stateRunning, stateBusy)
	}
	s.mu.Unlock()

	return func() {
		s.mu.Lock()
		s.inFlight--
		if s.inFlight == 0 {
			// If the state is no longer busy, something else (crash, stop)
			// changed it while requests were in flight; leave that alone.
			atomic.CompareAndSwapInt32(&s.state, stateBusy, stateIdle)
		}
		s.mu.Unlock()
		<-s.slots
	}, nil
}

// runIdleLoop periodically checks the idle timeout for one process
// generation. Requests no longer flow through a loop: every caller writes
// its own request and waits on its own pending entry (see sendRequest).
func (s *StdioServerV2) runIdleLoop(ctx context.Context, stopCh chan struct{}) {
	defer s.wg.Done()
	for {
		select {
		case <-s.healthTicker.C:
			s.checkIdleTimeout(ctx)
		case <-ctx.Done():
			return
		case <-stopCh:
			return
		}
	}
}

// processRequest executes req on the server and returns the response
// envelope (carrying the caller's own ID) together with the error that made
// the request fail, if any. An upstream JSON-RPC error is returned both as
// resp.Error (code, message and data preserved) and as err.
func (s *StdioServerV2) processRequest(ctx context.Context, req Request) (*Response, error) {
	startTime := time.Now()

	s.mu.Lock()
	s.lastRequestAt = startTime
	s.mu.Unlock()

	resp := &Response{ID: req.ID}

	result, sendErr := s.sendRequest(ctx, req)
	if sendErr != nil {
		// A structured upstream JSON-RPC error (the subprocess's own
		// {"error":{"code":...,"message":...,"data":...}}) keeps its
		// original code, message and data instead of being collapsed into
		// a generic ErrCodeServerError with a "jsonrpc: error N: msg"
		// string for a message. Only a real transport/proxy failure
		// (timeout, broken pipe, ...) gets the generic wrapping.
		var upstreamErr *errs.JSONRPCError
		if errstd.As(sendErr, &upstreamErr) {
			resp.Error = &errs.JSONRPCError{Code: upstreamErr.Code, Message: upstreamErr.Message, Data: upstreamErr.Data}
		} else {
			resp.Error = &errs.JSONRPCError{Code: errs.ErrCodeServerError, Message: sendErr.Error()}
		}
	} else {
		resp.Result = result
	}

	now := time.Now()
	latency := now.Sub(startTime).Seconds() * 1000
	s.mu.Lock()
	s.lastRequestAt = now
	if sendErr != nil {
		s.stats.ErrorCount++
		s.stats.LastError = sendErr.Error()
		s.stats.LastErrorAt = now
	}
	s.stats.RequestCount++
	s.stats.AvgLatencyMs = (s.stats.AvgLatencyMs*float64(s.stats.RequestCount-1) + latency) / float64(s.stats.RequestCount)
	s.mu.Unlock()

	return resp, sendErr
}

// sendRequest multiplexes one request over the current process generation's
// pipe and waits for its own response. Any number of callers may be in
// sendRequest concurrently, up to max_in_flight; further callers wait for a
// slot. The generation's MCP handshake runs first (once, shared).
//
// The effective timeout is min(server request timeout, req.Timeout); see
// roundTrip for what happens when it elapses.
func (s *StdioServerV2) sendRequest(ctx context.Context, req Request) (json.RawMessage, error) {
	timeout := s.requestTimeout
	if req.Timeout > 0 && req.Timeout < timeout {
		timeout = req.Timeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	release, err := s.acquireSlot(callCtx)
	if err != nil {
		return nil, s.waitError(ctx, timeout)
	}
	defer release()

	conn, gen := s.currentConn()
	if conn == nil {
		return nil, fmt.Errorf("pool: stdin not available")
	}

	// The pool owns the MCP handshake: every process generation performs
	// initialize + notifications/initialized exactly once, before any other
	// request of that generation is written. Concurrent callers share it.
	if err := s.ensureHandshake(callCtx, conn, gen); err != nil {
		if callCtx.Err() != nil {
			return nil, s.waitError(ctx, timeout)
		}
		return nil, err
	}
	if req.Method == methodInitialize {
		// An explicit initialize never reaches the server a second time:
		// the caller gets this generation's stored result.
		conn.handshake.mu.Lock()
		res := conn.handshake.result
		conn.handshake.mu.Unlock()
		return initializeResultJSON(res), nil
	}

	result, err := s.roundTrip(callCtx, conn, req.Method, req.Params)
	if errstd.Is(err, errWaitEnded) {
		return nil, s.waitError(ctx, timeout)
	}
	return result, err
}

// errWaitEnded marks a roundTrip that ended because its context did, before
// any reply arrived.
var errWaitEnded = errstd.New("wait ended")

// roundTrip writes one request on conn under a fresh wire ID and waits for
// its reply. When ctx ends first, the pending entry is removed, the MCP
// cancellation notification is sent to the server and a late response is
// discarded by the reader. A request whose context is already done before
// it is written is never written.
func (s *StdioServerV2) roundTrip(ctx context.Context, conn *stdioConn, method string, params json.RawMessage) (json.RawMessage, error) {
	// Give the request a unique internal wire ID so the response can be
	// matched to this exact in-flight request. Callers across generations
	// and retries routinely reuse the same JSON-RPC ID (handlers default to
	// id 1), so the caller's ID is never put on the wire; it is restored on
	// the response by processRequest.
	wireID := s.nextRequestID.Add(1)
	encoded, err := json.Marshal(Request{Method: method, Params: params, ID: wireID})
	if err != nil {
		return nil, fmt.Errorf("pool: marshal request: %w", err)
	}

	replyCh, err := conn.register(wireID)
	if err != nil {
		return nil, err
	}

	// Never write a request nobody is waiting for any more.
	if ctx.Err() != nil {
		conn.unregister(wireID)
		return nil, fmt.Errorf("%w: %w", errWaitEnded, ctx.Err())
	}

	s.logger.Debug("sending request to server", "name", s.name, "method", method, "id", wireID)
	if sent, err := conn.writeLine(ctx, encoded); err != nil {
		if !conn.unregister(wireID) {
			// A connection failure was delivered meanwhile.
			reply := <-replyCh
			return reply.result, reply.err
		}
		if sent {
			// The request will still reach the server (the interrupted
			// write is finished in the background): tell it we gave up.
			go conn.notifyCancelled(context.WithoutCancel(ctx), wireID, "timeout")
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%w: %w", errWaitEnded, ctx.Err())
		}
		return nil, fmt.Errorf("pool: write stdin: %w", err)
	}

	select {
	case reply := <-replyCh:
		return reply.result, reply.err
	case <-ctx.Done():
		if !conn.unregister(wireID) {
			// The response (or a connection failure) was delivered at the
			// same moment the context ended; the channel already holds it.
			reply := <-replyCh
			return reply.result, reply.err
		}
		reason := "timeout"
		if errstd.Is(ctx.Err(), context.Canceled) {
			reason = "canceled"
		}
		// Asynchronous so a server that stopped reading stdin can never
		// block a caller past its deadline.
		go conn.notifyCancelled(context.WithoutCancel(ctx), wireID, reason)
		return nil, fmt.Errorf("%w: %w", errWaitEnded, ctx.Err())
	}
}

// waitError is the error returned when a request's wait ends before a
// response: the caller's own context error when the caller gave up, or a
// request-timeout error (with recent stderr for diagnostics) when the
// per-request timeout elapsed.
func (s *StdioServerV2) waitError(ctx context.Context, timeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("pool: request timeout after %v (recent stderr: %s)", timeout, s.stderrLines.String())
}

func (s *StdioServerV2) sendNotification(ctx context.Context, method string, params map[string]interface{}) error {
	if method == methodInitializedNotification {
		// Part of the handshake the pool performs itself (once per
		// generation); a caller's copy would be a duplicate.
		return nil
	}
	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		notification["params"] = params
	}
	encoded, err := json.Marshal(notification)
	if err != nil {
		return fmt.Errorf("pool: marshal notification: %w", err)
	}

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("pool: stdin not available")
	}
	if !conn.handshake.initialized() {
		// Nothing but the handshake may reach a server before its
		// initialize completed; the server gets fresh state from it.
		s.logger.Debug("dropping notification sent before the MCP handshake", "name", s.name, "method", method)
		return nil
	}

	writeCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()
	if _, err := conn.writeLine(writeCtx, encoded); err != nil {
		return fmt.Errorf("pool: write stdin: %w", err)
	}

	return nil
}

func (s *StdioServerV2) checkIdleTimeout(ctx context.Context) {
	if s.idleTimeout <= 0 {
		return
	}

	s.mu.Lock()
	idleDuration := time.Since(s.lastRequestAt)
	currentState := atomic.LoadInt32(&s.state)
	shouldStop := s.inFlight == 0 && idleDuration > s.idleTimeout && currentState == stateIdle
	s.mu.Unlock()

	if shouldStop {
		s.logger.Info("idle timeout reached, stopping server", "name", s.name)
		// Must run asynchronously: checkIdleTimeout executes on the
		// runIdleLoop goroutine, which is registered in s.wg. stopLocked
		// waits on s.wg, so a synchronous call would wait on itself and
		// deadlock the server (and every later restart) permanently.
		go func() {
			if err := s.stop(); err != nil {
				s.logger.Warn("idle stop failed", "name", s.name, "error", err)
			}
		}()
	}
}
