package pool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/errors"
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
	Name           string
	Command        string
	Args           []string
	Env            []string
	CWD            string
	MaxConcurrent  int
	IdleTimeout    time.Duration
	RequestTimeout time.Duration
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
}

type StdioServerV2 struct {
	name           string
	config         StdioServerConfig
	process        *exec.Cmd
	pgid           int
	stdin          io.WriteCloser
	stdout         io.Reader
	mu             sync.Mutex
	requestCh      chan Request
	responseCh     chan Response
	state          int32
	stats          ServerStats
	restartCount   int
	maxRestarts    int
	backoff        time.Duration
	lastRequestAt  time.Time
	idleTimeout    time.Duration
	requestTimeout time.Duration
	maxConcurrent  int
	currentLoad    int
	healthTicker   *time.Ticker
	stopCh         chan struct{}
	stopped        bool
	logger         *slog.Logger
	stopChOnce     sync.Once
	wg             sync.WaitGroup
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
	if idleTimeout == 0 {
		idleTimeout = 5 * time.Minute
	}

	requestTimeout := config.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = 30 * time.Second
	}

	return &StdioServerV2{
		name:           name,
		config:         config,
		requestCh:      make(chan Request, maxConcurrent),
		responseCh:     make(chan Response, maxConcurrent),
		state:          stateIdle,
		stats:          ServerStats{},
		maxRestarts:    5,
		backoff:        time.Second,
		idleTimeout:    idleTimeout,
		requestTimeout: requestTimeout,
		maxConcurrent:  maxConcurrent,
		healthTicker:   time.NewTicker(30 * time.Second),
		stopCh:         make(chan struct{}),
		logger:         logger,
	}
}

func (s *StdioServerV2) getState() ServerState {
	return toServerState(atomic.LoadInt32(&s.state))
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

func (s *StdioServerV2) spawn(ctx context.Context) error {
	s.mu.Lock()

	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateRunning || currentState == stateBusy || currentState == stateStarting {
		s.mu.Unlock()
		return fmt.Errorf("pool: cannot spawn server in state %s", toServerState(currentState))
	}

	atomic.StoreInt32(&s.state, stateStarting)

	cmd := exec.CommandContext(ctx, s.config.Command, s.config.Args...)
	if s.config.Env != nil {
		cmd.Env = append(os.Environ(), s.config.Env...)
	}
	if s.config.CWD != "" {
		cmd.Dir = s.config.CWD
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("pool: stdin pipe: %w", err)
	}
	s.stdin = stdin

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		s.mu.Unlock()
		return fmt.Errorf("pool: stdout pipe: %w", err)
	}
	s.stdout = stdoutR

	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("pool: start %s: %w", s.name, err)
	}

	s.process = cmd
	s.pgid = cmd.Process.Pid
	atomic.StoreInt32(&s.state, stateIdle)
	s.restartCount = 0
	s.backoff = time.Second
	s.stats.RestartCount++
	s.stats.CurrentBackoff = s.backoff

	s.logger.Info("server spawned", "name", s.name, "pid", cmd.Process.Pid, "pgid", s.pgid)

	s.mu.Unlock()

	s.wg.Add(1)
	go s.waitForExit(ctx)
	s.wg.Add(1)
	go s.readResponses()

	return nil
}

func (s *StdioServerV2) waitForExit(ctx context.Context) {
	defer s.wg.Done()
	err := s.process.Wait()

	s.mu.Lock()
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping {
		atomic.StoreInt32(&s.state, stateStopped)
		s.mu.Unlock()
		return
	}

	atomic.StoreInt32(&s.state, stateError)
	s.mu.Unlock()

	s.logger.Warn("server process exited", "name", s.name, "error", err)

	s.scheduleRestart(ctx)
}

func (s *StdioServerV2) scheduleRestart(ctx context.Context) {
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

	select {
	case <-time.After(backoff):
	case <-ctx.Done():
		return
	}

	s.mu.Lock()
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if err := s.spawn(ctx); err != nil {
		s.logger.Error("restart failed", "name", s.name, "error", err)
	}
}

func (s *StdioServerV2) readResponses() {
	defer s.wg.Done()
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024), 50*1024*1024)

	for {
		select {
		case <-s.stopCh:
			return
		default:
			if scanner.Scan() {
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

func (s *StdioServerV2) stop() error {
	s.mu.Lock()
	currentState := atomic.LoadInt32(&s.state)
	if currentState == stateStopping || currentState == stateStopped {
		s.mu.Unlock()
		return nil
	}
	atomic.StoreInt32(&s.state, stateStopping)
	s.mu.Unlock()

	s.stopChOnce.Do(func() {
		close(s.stopCh)
	})

	if s.process != nil && s.process.Process != nil {
		s.process.Process.Signal(syscall.SIGTERM)
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

func (s *StdioServerV2) runRequestLoop(ctx context.Context, pool *StdioPool) {
	for {
		select {
		case req := <-s.requestCh:
			s.processRequest(ctx, req)

		case <-s.healthTicker.C:
			s.checkIdleTimeout(ctx)

		case <-ctx.Done():
			return
		case <-s.stopCh:
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
		resp.Error = &errors.JSONRPCError{Code: errors.ErrCodeServerError, Message: sendErr.Error()}
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
		return nil, fmt.Errorf("pool: request timeout after %v", timeout)
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
