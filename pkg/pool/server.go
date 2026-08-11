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
	Name            string
	Command         string
	Args            []string
	Env             []string
	CWD             string
	MaxConcurrent   int
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
	name            string
	config          StdioServerConfig
	process         *exec.Cmd
	pgid            int
	stdin           io.WriteCloser
	stdout          io.Reader
	mu              sync.Mutex
	requestCh       chan Request
	responseCh      chan Response
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
	maxConcurrent   int
	maxResponseSize int
	currentLoad     int
	healthTicker    *time.Ticker
	genStopCh       chan struct{}
	genStopOnce     *sync.Once
	restartMu       sync.Mutex
	logger          *slog.Logger
	wg              sync.WaitGroup
	mcpInitialized  atomic.Bool
	stderrLines     *stderrRing
}

func newServerV2(name string, config StdioServerConfig, logger *slog.Logger) *StdioServerV2 {
	if logger == nil {
		logger = slog.Default()
	}

	maxConcurrent := config.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = 5
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
		requestCh:       make(chan Request, maxConcurrent),
		responseCh:      make(chan Response, maxConcurrent),
		state:           stateIdle,
		stats:           ServerStats{},
		maxRestarts:     5,
		backoff:         time.Second,
		initialBackoff:  time.Second,
		stableWindow:    2 * time.Minute,
		idleTimeout:     idleTimeout,
		requestTimeout:  requestTimeout,
		maxConcurrent:   maxConcurrent,
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
	s.maxRestarts = settings.MaxRestartAttempts
	s.initialBackoff = settings.RestartBackoff
	s.backoff = settings.RestartBackoff
	s.stableWindow = settings.StableWindow
	s.stats.CurrentBackoff = s.backoff
}

func (s *StdioServerV2) setState(newState int32) {
	atomic.StoreInt32(&s.state, newState)
}

func (s *StdioServerV2) compareAndSwapState(oldState, newState int32) bool {
	return atomic.CompareAndSwapInt32(&s.state, oldState, newState)
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

	cmd := exec.CommandContext(genCtx, s.config.Command, s.config.Args...)
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
	s.stdin = stdin

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		atomic.StoreInt32(&s.state, stateError)
		s.mu.Unlock()
		return fmt.Errorf("pool: stdout pipe: %w", err)
	}
	s.stdout = stdoutR

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

	s.logger.Info("server spawned", "name", s.name, "pid", cmd.Process.Pid, "pgid", s.pgid, "command", s.config.Command, "args", s.config.Args)

	s.mu.Unlock()

	go s.readStderr(stderrR, genStopCh)
	s.wg.Add(1)
	go s.waitForExit(genCtx, genStopCh, genStopOnce)
	s.wg.Add(1)
	go s.readResponses(genStopCh)
	s.wg.Add(1)
	go s.runRequestLoop(genCtx, genStopCh)

	// Post-spawn verification: confirm process is alive.
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		return fmt.Errorf("pool: server %s process not alive after spawn: %w (recent stderr: %s)", s.name, err, s.stderrLines.String())
	}

	return nil
}

func (s *StdioServerV2) waitForExit(ctx context.Context, stopCh chan struct{}, stopOnce *sync.Once) {
	err := s.process.Wait()

	// Signal the rest of this generation that the process is gone so readers
	// and the request loop exit and never serve a dead process.
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

func (s *StdioServerV2) scheduleRestart(ctx context.Context) {
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping || currentState == stateStopped {
		return
	}

	s.mu.Lock()
	s.restartCount++
	if s.restartCount > s.maxRestarts {
		s.mu.Unlock()
		s.logger.Error("max restarts exceeded", "name", s.name, "restarts", s.restartCount)
		atomic.StoreInt32(&s.state, stateError)
		return
	}

	backoff := s.backoff
	s.backoff *= 2
	if s.backoff > time.Minute {
		s.backoff = time.Minute
	}
	s.stats.CurrentBackoff = s.backoff
	s.mu.Unlock()

	s.logger.Info("scheduled restart", "name", s.name, "backoff", backoff, "attempt", s.restartCount)

	// Add jitter so a fleet of crashed servers does not restart in lockstep.
	wait := backoff + time.Duration(rand.Int63n(int64(backoff/4))+1)

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
// one generation at a time.
func (s *StdioServerV2) restart(ctx context.Context) error {
	s.restartMu.Lock()
	defer s.restartMu.Unlock()

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

func (s *StdioServerV2) readResponses(stopCh chan struct{}) {
	defer s.wg.Done()
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024), s.maxResponseSize)

	for {
		select {
		case <-stopCh:
			return
		default:
			if scanner.Scan() {
				if scanner.Err() != nil {
					if errstd.Is(scanner.Err(), bufio.ErrBufferFull) {
						s.logger.Error("response exceeds max buffer size", "name", s.name, "maxSize", s.maxResponseSize)
					} else {
						s.logger.Error("scanner error", "name", s.name, "error", scanner.Err())
					}
					return
				}

				line := scanner.Bytes()
				s.logger.Debug("read from server stdout", "name", s.name, "line", string(line))

				var msg map[string]json.RawMessage
				if err := json.Unmarshal(line, &msg); err != nil {
					s.logger.Warn("failed to parse response", "name", s.name, "error", err)
					continue
				}

				if _, hasResult := msg["result"]; !hasResult {
					if _, hasError := msg["error"]; !hasError {
						s.logger.Debug("received notification, ignoring", "name", s.name, "line", string(line))
						continue
					}
				}

				var resp Response
				if err := json.Unmarshal(line, &resp); err != nil {
					s.logger.Warn("failed to parse response", "name", s.name, "error", err)
					continue
				}
				select {
				case s.responseCh <- resp:
				default:
					s.logger.Warn("response channel full, dropping response", "name", s.name)
				}
			} else {
				return
			}
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
	s.mu.Unlock()

	if stopCh != nil && stopOnce != nil {
		stopOnce.Do(func() { close(stopCh) })
	}

	if proc != nil && proc.Process != nil {
		proc.Process.Signal(syscall.SIGTERM)
	}

	s.wg.Wait()

	return nil
}

func (s *StdioServerV2) isHealthy() bool {
	currentState := atomic.LoadInt32(&s.state)
	return currentState == stateIdle || currentState == stateRunning || currentState == stateBusy
}

func (s *StdioServerV2) canAcceptRequest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLoad < s.maxConcurrent
}

func (s *StdioServerV2) isIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	currentState := atomic.LoadInt32(&s.state)
	return s.currentLoad == 0 && (currentState == stateIdle || currentState == stateRunning)
}

func (s *StdioServerV2) getStats() ServerStats {
	s.mu.Lock()
	stats := s.stats
	s.mu.Unlock()
	return stats
}

func (s *StdioServerV2) enqueueRequest(req Request) bool {
	s.mu.Lock()
	if s.currentLoad >= s.maxConcurrent {
		s.mu.Unlock()
		return false
	}
	s.currentLoad++
	s.mu.Unlock()

	select {
	case s.requestCh <- req:
		return true
	default:
		s.mu.Lock()
		s.currentLoad--
		s.mu.Unlock()
		return false
	}
}

func (s *StdioServerV2) runRequestLoop(ctx context.Context, stopCh chan struct{}) {
	defer s.wg.Done()
	for {
		select {
		case req := <-s.requestCh:
			s.processRequest(ctx, req)

		case <-s.healthTicker.C:
			s.checkIdleTimeout(ctx)

		case <-ctx.Done():
			return
		case <-stopCh:
			return
		}
	}
}

func (s *StdioServerV2) processRequest(ctx context.Context, req Request) {
	startTime := time.Now()

	s.mu.Lock()
	s.lastRequestAt = startTime
	s.mu.Unlock()

	resp := &Response{ID: req.ID}

	atomic.StoreInt32(&s.state, stateBusy)

	result, sendErr := s.sendRequest(ctx, req)
	if sendErr != nil {
		resp.Error = &errs.JSONRPCError{Code: errs.ErrCodeServerError, Message: sendErr.Error()}
		s.mu.Lock()
		s.stats.ErrorCount++
		s.mu.Unlock()
	} else {
		resp.Result = result
	}

	latency := time.Since(startTime).Seconds() * 1000
	s.mu.Lock()
	s.stats.RequestCount++
	s.stats.AvgLatencyMs = (s.stats.AvgLatencyMs*float64(s.stats.RequestCount-1) + latency) / float64(s.stats.RequestCount)
	currentState := atomic.LoadInt32(&s.state)
	if currentState != stateStopping {
		atomic.StoreInt32(&s.state, stateIdle)
	}
	s.mu.Unlock()

	if req.ResultCh != nil {
		select {
		case req.ResultCh <- resp:
		default:
		}
	}

	if req.ErrorCh != nil && sendErr != nil {
		select {
		case req.ErrorCh <- sendErr:
		default:
		}
	}
}

func (s *StdioServerV2) sendRequest(ctx context.Context, req Request) (json.RawMessage, error) {
	encoded, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("pool: marshal request: %w", err)
	}

	s.logger.Debug("sending request to server", "name", s.name, "method", req.Method, "id", req.ID, "encoded", string(encoded))

	s.mu.Lock()
	if s.stdin == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("pool: stdin not available")
	}
	stdin := s.stdin
	s.mu.Unlock()

	s.logger.Debug("writing to stdin", "name", s.name, "data", string(encoded))
	if _, err := fmt.Fprintln(stdin, string(encoded)); err != nil {
		return nil, fmt.Errorf("pool: write stdin: %w", err)
	}

	timeout := s.requestTimeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}

	select {
	case resp := <-s.responseCh:
		s.logger.Debug("received raw response from server", "name", s.name, "response", fmt.Sprintf("%+v", resp))
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("pool: request timeout after %v (recent stderr: %s)", timeout, s.stderrLines.String())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
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
	if s.stdin == nil {
		s.mu.Unlock()
		return fmt.Errorf("pool: stdin not available")
	}
	stdin := s.stdin
	s.mu.Unlock()

	if _, err := fmt.Fprintln(stdin, string(encoded)); err != nil {
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
	shouldStop := s.currentLoad == 0 && idleDuration > s.idleTimeout && currentState == stateIdle
	s.mu.Unlock()

	if shouldStop {
		s.logger.Info("idle timeout reached, stopping server", "name", s.name)
		s.stop()
	}
}
