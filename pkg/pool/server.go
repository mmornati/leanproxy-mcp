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
	"syscall"
	"time"
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
	RequestCount  int64
	ErrorCount    int64
	AvgLatencyMs  float64
	LastRequestAt time.Time
	RestartCount  int
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
	state          ServerState
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
		state:          StateIdle,
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

func (s *StdioServerV2) spawn(ctx context.Context) error {
	s.mu.Lock()

	if s.state == StateRunning || s.state == StateBusy || s.state == StateStarting {
		s.mu.Unlock()
		return fmt.Errorf("pool: cannot spawn server in state %s", s.state)
	}

	s.state = StateStarting

	cmd := exec.CommandContext(ctx, s.config.Command, s.config.Args...)
	if s.config.Env != nil {
		cmd.Env = append(os.Environ(), s.config.Env...)
	}
	if s.config.CWD != "" {
		cmd.Dir = s.config.CWD
	}

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

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
	pgid, _ := syscall.Getpgid(cmd.Process.Pid)
	s.pgid = pgid
	s.state = StateIdle
	s.restartCount = 0
	s.backoff = time.Second
	s.stats.RestartCount++
	s.stats.CurrentBackoff = s.backoff

	s.logger.Info("server spawned", "name", s.name, "pid", cmd.Process.Pid, "pgid", s.pgid)

	s.mu.Unlock()

	go s.waitForExit(ctx)
	go s.readResponses()

	return nil
}

func (s *StdioServerV2) waitForExit(ctx context.Context) {
	err := s.process.Wait()

	s.mu.Lock()
	if s.state == StateStopping {
		s.mu.Unlock()
		return
	}

	s.state = StateError
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
		s.state = StateError
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
	if s.state == StateStopping {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if err := s.spawn(ctx); err != nil {
		s.logger.Error("restart failed", "name", s.name, "error", err)
	}
}

func (s *StdioServerV2) readResponses() {
	scanner := bufio.NewScanner(s.stdout)
	scanner.Buffer(make([]byte, 1024), 50*1024*1024)

	for {
		select {
		case <-s.stopCh:
			return
		default:
			if scanner.Scan() {
				line := scanner.Bytes()
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
	if s.state == StateStopping || s.state == StateStopped {
		s.mu.Unlock()
		return nil
	}
	s.state = StateStopping
	s.mu.Unlock()

	s.stopChOnce.Do(func() {
		close(s.stopCh)
	})

	if s.process != nil && s.process.Process != nil {
		pgid := s.pgid
		signalErr := syscall.Kill(-pgid, syscall.SIGTERM)
		if signalErr != nil {
			s.logger.Warn("failed to send SIGTERM", "name", s.name, "error", signalErr)
			syscall.Kill(-pgid, syscall.SIGKILL)
		}

		s.process.Wait()
	}

	s.mu.Lock()
	s.state = StateStopped
	s.mu.Unlock()

	s.logger.Info("server stop completed", "name", s.name)
	return nil
}

func (s *StdioServerV2) isHealthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == StateIdle || s.state == StateRunning || s.state == StateBusy
}

func (s *StdioServerV2) canAcceptRequest() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLoad < s.maxConcurrent
}

func (s *StdioServerV2) isIdle() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentLoad == 0 && (s.state == StateIdle || s.state == StateRunning)
}

func (s *StdioServerV2) getStats() ServerStats {
	s.mu.Lock()
	stats := s.stats
	s.mu.Unlock()
	return stats
}

func (s *StdioServerV2) getState() ServerState {
	s.mu.Lock()
	state := s.state
	s.mu.Unlock()
	return state
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

	s.mu.Lock()
	s.state = StateBusy
	s.mu.Unlock()

	result, sendErr := s.sendRequest(ctx, req)
	if sendErr != nil {
		resp.Error = &JSONRPCError{Code: ErrCodeServerError, Message: sendErr.Error()}
		s.stats.ErrorCount++
	} else {
		resp.Result = result
	}

	latency := time.Since(startTime).Seconds() * 1000
	s.mu.Lock()
	s.stats.RequestCount++
	s.stats.AvgLatencyMs = (s.stats.AvgLatencyMs*float64(s.stats.RequestCount-1) + latency) / float64(s.stats.RequestCount)
	if s.state != StateStopping {
		s.state = StateIdle
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

	s.mu.Lock()
	if s.stdin == nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("pool: stdin not available")
	}
	stdin := s.stdin
	s.mu.Unlock()

	if _, err := fmt.Fprintln(stdin, string(encoded)); err != nil {
		return nil, fmt.Errorf("pool: write stdin: %w", err)
	}

	timeout := s.requestTimeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}

	select {
	case resp := <-s.responseCh:
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

func (s *StdioServerV2) checkIdleTimeout(ctx context.Context) {
	s.mu.Lock()
	idleDuration := time.Since(s.lastRequestAt)
	shouldStop := s.currentLoad == 0 && idleDuration > s.idleTimeout && s.state == StateIdle
	s.mu.Unlock()

	if shouldStop {
		s.logger.Info("idle timeout reached, stopping server", "name", s.name)
		s.stop()
	}
}