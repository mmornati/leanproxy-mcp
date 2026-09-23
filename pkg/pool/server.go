package pool

import (
	"bufio"
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
	"syscall"
	"time"

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
	Env     []string
	CWD     string
	// MaxInFlight caps the number of requests multiplexed concurrently
	// over the server's stdio pipe. Callers beyond the cap wait (respecting
	// their context) instead of being rejected. 0 means
	// DefaultMaxInFlight.
	MaxInFlight     int
	IdleTimeout     time.Duration
	RequestTimeout  time.Duration
	MaxResponseSize int
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
	// slots is the per-server in-flight semaphore (capacity maxInFlight).
	slots chan struct{}
	// inFlight counts requests holding a slot. Guarded by mu.
	inFlight       int
	healthTicker   *time.Ticker
	genStopCh      chan struct{}
	genStopOnce    *sync.Once
	restartMu      sync.Mutex
	logger         *slog.Logger
	wg             sync.WaitGroup
	mcpInitialized atomic.Bool
	stderrLines    *stderrRing
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
	if maxResponseSize == 0 {
		maxResponseSize = 1024 * 1024 // 1MB default
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

func (s *StdioServerV2) IsMCPInitialized() bool {
	return s.mcpInitialized.Load()
}

func (s *StdioServerV2) SetMCPInitialized() {
	s.mcpInitialized.Store(true)
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
	// Build environment: inherit current env, apply user config, then ensure
	// PYTHONUNBUFFERED=1 so Python-based MCP servers don't buffer stdout.
	env := os.Environ()
	if s.config.Env != nil {
		env = append(env, s.config.Env...)
	}
	env = append(env, "PYTHONUNBUFFERED=1")
	cmd.Env = env
	if s.config.CWD != "" {
		cmd.Dir = s.config.CWD
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		return fmt.Errorf("pool: stdin pipe: %w", err)
	}

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		return fmt.Errorf("pool: stdout pipe: %w", err)
	}

	stderrR, err := cmd.StderrPipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		return fmt.Errorf("pool: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		s.logger.Error("failed to start server process",
			"name", s.name,
			"command", s.config.Command,
			"args", s.config.Args,
			"error", err)
		return fmt.Errorf("pool: start %s: %w", s.name, err)
	}

	s.process = cmd
	s.pgid = cmd.Process.Pid
	atomic.StoreInt32(&s.state, stateIdle)
	s.backoff = s.initialBackoff
	s.lastSpawnAt = time.Now()
	s.lastRequestAt = time.Now()
	s.mcpInitialized.Store(false)
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
	s.conn = conn
	s.generation.Add(1)

	s.logger.Info("server spawned", "name", s.name, "pid", cmd.Process.Pid, "pgid", s.pgid, "command", s.config.Command, "args", s.config.Args)

	s.mu.Unlock()

	go s.readStderr(stderrR, genStopCh)
	s.wg.Add(1)
	go s.waitForExit(genCtx, cmd, conn, genStopCh, genStopOnce)
	s.wg.Add(1)
	go s.readResponses(conn, stdoutR, genStopCh)
	s.wg.Add(1)
	go s.runIdleLoop(genCtx, genStopCh)

	// Post-spawn verification: confirm process is alive.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		// Tear down the generation we just started so a failed spawn never
		// leaks a process and its goroutines outside the pool's view.
		// waitForExit observes the kill and drives the normal crash-recovery
		// path (bounded by the restart budget).
		genStopOnce.Do(func() { close(genStopCh) })
		_ = cmd.Process.Kill()
		atomic.StoreInt32(&s.state, stateError)
		return fmt.Errorf("pool: server %s process not alive after spawn: %w (recent stderr: %s)", s.name, err, s.stderrLines.String())
	}

	return nil
}

func (s *StdioServerV2) waitForExit(ctx context.Context, cmd *exec.Cmd, conn *stdioConn, stopCh chan struct{}, stopOnce *sync.Once) {
	err := cmd.Wait()

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
	// stopGracePeriod is how long stopLocked waits for a SIGTERMed process
	// generation to wind down before escalating to SIGKILL.
	stopGracePeriod = 5 * time.Second
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
		s.mu.Unlock()
		s.logger.Error("max restarts exceeded, leaving server in error state until next use", "name", s.name, "restarts", s.restartCount)
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
	s.mu.Unlock()

	s.logger.Info("scheduled restart", "name", s.name, "backoff", backoff, "attempt", s.restartCount)

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
// to its waiter. When the reader ends for any reason (EOF, read error,
// oversized line, generation stopped) every pending waiter is failed
// immediately via conn.fail.
func (s *StdioServerV2) readResponses(conn *stdioConn, stdout io.Reader, stopCh chan struct{}) {
	defer s.wg.Done()
	// Evaluated at exit so the error carries the stderr lines captured up
	// to the moment the reader died.
	defer func() { conn.fail(s.exitedError()) }()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024), s.maxResponseSize)

	for scanner.Scan() {
		select {
		case <-stopCh:
			return
		default:
		}
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		s.logger.Debug("read from server stdout", "name", s.name, "line", string(line))
		conn.handleLine(line)
	}

	if err := scanner.Err(); err != nil {
		if errstd.Is(err, bufio.ErrTooLong) {
			s.logger.Error("response exceeds max buffer size", "name", s.name, "maxSize", s.maxResponseSize)
		} else {
			s.logger.Debug("stdout reader ended", "name", s.name, "error", err)
		}
	}
}

func (s *StdioServerV2) readStderr(stderr io.Reader, stopCh chan struct{}) {
	scanner := bufio.NewScanner(stderr)

	for {
		select {
		case <-stopCh:
			return
		default:
			if scanner.Scan() {
				if scanner.Err() != nil {
					s.logger.Error("stderr scanner error", "name", s.name, "error", scanner.Err())
					return
				}

				line := scanner.Bytes()
				if len(line) > 0 {
					s.stderrLines.add(string(line))
					s.logger.Info("server stderr", "name", s.name, "output", string(line))
				}
			} else {
				return
			}
		}
	}
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
	conn := s.conn
	s.mu.Unlock()

	// Fail in-flight requests right away instead of letting them hang until
	// their timeout while this generation is torn down.
	if conn != nil {
		conn.fail(fmt.Errorf("pool: server %s is stopping", s.name))
	}

	if stopCh != nil && stopOnce != nil {
		stopOnce.Do(func() { close(stopCh) })
	}

	if proc != nil && proc.Process != nil {
		if err := proc.Process.Signal(syscall.SIGTERM); err != nil {
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
	select {
	case <-wgDone:
	case <-time.After(stopGracePeriod):
		if proc != nil && proc.Process != nil {
			s.logger.Warn("server ignored SIGTERM, escalating to SIGKILL", "name", s.name)
			_ = proc.Process.Kill()
		}
		<-wgDone
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
// slot.
//
// The effective timeout is min(server request timeout, req.Timeout). When
// the caller's context ends (or the timeout elapses) before the response
// arrives, the pending entry is removed, the MCP cancellation notification is sent to
// the server and a late response is discarded by the reader. A request whose
// context is already done before it is written is never written.
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

	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return nil, fmt.Errorf("pool: stdin not available")
	}

	// Give the request a unique internal wire ID so the response can be
	// matched to this exact in-flight request. Callers across generations
	// and retries routinely reuse the same JSON-RPC ID (handlers default to
	// id 1), so the caller's ID is never put on the wire; it is restored on
	// the response by processRequest.
	wireID := s.nextRequestID.Add(1)
	wireReq := req
	wireReq.ID = wireID
	encoded, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("pool: marshal request: %w", err)
	}

	replyCh, err := conn.register(wireID)
	if err != nil {
		return nil, err
	}

	// Never write a request nobody is waiting for any more.
	if callCtx.Err() != nil {
		conn.unregister(wireID)
		return nil, s.waitError(ctx, timeout)
	}

	s.logger.Debug("sending request to server", "name", s.name, "method", req.Method, "id", wireID)
	if err := conn.writeLine(encoded); err != nil {
		conn.unregister(wireID)
		return nil, fmt.Errorf("pool: write stdin: %w", err)
	}

	select {
	case reply := <-replyCh:
		return reply.result, reply.err
	case <-callCtx.Done():
		if !conn.unregister(wireID) {
			// The response (or a connection failure) was delivered at the
			// same moment the context ended; the channel already holds it.
			reply := <-replyCh
			return reply.result, reply.err
		}
		reason := "timeout"
		if errstd.Is(callCtx.Err(), context.Canceled) {
			reason = "canceled"
		}
		// Asynchronous so a server that stopped reading stdin can never
		// block a caller past its deadline.
		go conn.notifyCancelled(wireID, reason)
		return nil, s.waitError(ctx, timeout)
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
	notification := map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
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

	if err := conn.writeLine(encoded); err != nil {
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
