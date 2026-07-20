package ratelimit

import (
	"fmt"
	"sync"
	"time"
)

type TokenBucket struct {
	mu         sync.Mutex
	capacity   float64
	tokens     float64
	refillRate float64
	lastRefill time.Time
}

func NewTokenBucket(capacity int, refillInterval time.Duration) *TokenBucket {
	return &TokenBucket{
		capacity:   float64(capacity),
		tokens:     float64(capacity),
		refillRate: float64(capacity) / refillInterval.Seconds(),
		lastRefill: time.Now(),
	}
}

func (tb *TokenBucket) Allow() bool {
	return tb.AllowN(1)
}

func (tb *TokenBucket) AllowN(n int) bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	if tb.tokens >= float64(n) {
		tb.tokens -= float64(n)
		return true
	}
	return false
}

func (tb *TokenBucket) Remaining() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return int(tb.tokens)
}

func (tb *TokenBucket) ResetTime() time.Time {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()
	return tb.estimateResetTimeLocked()
}

type RateLimitInfo struct {
	Allowed   bool
	Remaining int
	ResetAt   time.Time
}

func (tb *TokenBucket) Check() RateLimitInfo {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	return RateLimitInfo{
		Allowed:   tb.tokens >= 1,
		Remaining: int(tb.tokens),
		ResetAt:   tb.estimateResetTimeLocked(),
	}
}

func (tb *TokenBucket) AllowWithInfo() RateLimitInfo {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	tb.refill()

	allowed := tb.tokens >= 1

	if allowed {
		tb.tokens--
	}

	info := RateLimitInfo{
		Allowed:   allowed,
		Remaining: int(tb.tokens),
		ResetAt:   tb.estimateResetTimeLocked(),
	}

	return info
}

type RateLimitError struct {
	Remaining int
	ResetAt   time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit exceeded: %d requests remaining, resets at %s", e.Remaining, e.ResetAt.Format(time.RFC3339))
}

func (tb *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(tb.lastRefill).Seconds()
	if elapsed > 0 {
		tb.tokens += elapsed * tb.refillRate
		if tb.tokens > tb.capacity {
			tb.tokens = tb.capacity
		}
	}
	tb.lastRefill = now
}

func (tb *TokenBucket) estimateResetTimeLocked() time.Time {
	if tb.tokens >= tb.capacity {
		return time.Now()
	}
	needed := tb.capacity - tb.tokens
	seconds := needed / tb.refillRate
	return tb.lastRefill.Add(time.Duration(seconds * float64(time.Second)))
}
