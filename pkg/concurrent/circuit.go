package concurrent

import (
	"sync"
	"sync/atomic"
	"time"
)

type CircuitState int

const (
	StateClosed CircuitState = iota
	StateOpen
	StateHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

type CircuitBreaker struct {
	mu              sync.RWMutex
	failures        int32
	threshold       int
	cooldown        time.Duration
	halfOpenSuccess int32
	halfOpenMax     int
	state           CircuitState
	lastFailure     time.Time
	successes       int32
	totalSuccesses   int32
	totalFailures    int32
}

type CircuitBreakerConfig struct {
	Threshold        int
	Cooldown         time.Duration
	HalfOpenMaxSuccess int
}

func NewCircuitBreaker(threshold int, cooldown time.Duration, halfOpenCooldown time.Duration) *CircuitBreaker {
	maxSuccess := 3
	if halfOpenCooldown > 0 {
		maxSuccess = 3
	}

	return &CircuitBreaker{
		threshold:       threshold,
		cooldown:        cooldown,
		halfOpenMax:     maxSuccess,
		state:           StateClosed,
		lastFailure:     time.Time{},
		successes:       0,
		totalSuccesses:  0,
		totalFailures:   0,
	}
}

func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	state := cb.state
	lastFailure := cb.lastFailure
	cb.mu.RUnlock()

	if state == StateOpen {
		if time.Since(lastFailure) >= cb.cooldown {
			cb.mu.Lock()
			if cb.state == StateOpen {
				cb.state = StateHalfOpen
				cb.successes = 0
			}
			cb.mu.Unlock()
			return StateHalfOpen
		}
		return StateOpen
	}

	return state
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	atomic.AddInt32(&cb.totalSuccesses, 1)

	switch cb.state {
	case StateHalfOpen:
		cb.successes++
		if cb.successes >= int32(cb.halfOpenMax) {
			cb.state = StateClosed
			cb.failures = 0
			cb.successes = 0
		}
	case StateClosed:
		cb.failures = 0
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	atomic.AddInt32(&cb.totalFailures, 1)
	cb.failures++
	cb.lastFailure = time.Now()

	switch cb.state {
	case StateClosed:
		if cb.failures >= int32(cb.threshold) {
			cb.state = StateOpen
		}
	case StateHalfOpen:
		cb.state = StateOpen
		cb.successes = 0
	}
}

func (cb *CircuitBreaker) Allow() bool {
	state := cb.State()
	return state == StateClosed || state == StateHalfOpen
}

func (cb *CircuitBreaker) GetMetrics() CircuitBreakerMetrics {
	cb.mu.RLock()
	state := cb.state
	failures := cb.failures
	cb.mu.RUnlock()

	return CircuitBreakerMetrics{
		State:          state.String(),
		Failures:       failures,
		Threshold:      cb.threshold,
		TotalSuccesses: atomic.LoadInt32(&cb.totalSuccesses),
		TotalFailures:  atomic.LoadInt32(&cb.totalFailures),
		LastFailure:   cb.lastFailure,
	}
}

type CircuitBreakerMetrics struct {
	State          string
	Failures       int32
	Threshold      int
	TotalSuccesses int32
	TotalFailures  int32
	LastFailure    time.Time
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.lastFailure = time.Time{}
}

type CircuitBreakerGroup struct {
	breakers map[string]*CircuitBreaker
	mu       sync.RWMutex
	defaultCB *CircuitBreaker
}

func NewCircuitBreakerGroup() *CircuitBreakerGroup {
	return &CircuitBreakerGroup{
		breakers: make(map[string]*CircuitBreaker),
		defaultCB: NewCircuitBreaker(5, 50*time.Second, 10*time.Second),
	}
}

func (g *CircuitBreakerGroup) Get(name string) *CircuitBreaker {
	g.mu.RLock()
	cb, exists := g.breakers[name]
	g.mu.RUnlock()

	if exists {
		return cb
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if cb, exists = g.breakers[name]; exists {
		return cb
	}

	cb = NewCircuitBreaker(5, 50*time.Second, 10*time.Second)
	g.breakers[name] = cb
	return cb
}

func (g *CircuitBreakerGroup) Register(name string, cb *CircuitBreaker) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.breakers[name] = cb
}

func (g *CircuitBreakerGroup) ResetAll() {
	g.mu.Lock()
	defer g.mu.Unlock()

	for _, cb := range g.breakers {
		cb.Reset()
	}
}