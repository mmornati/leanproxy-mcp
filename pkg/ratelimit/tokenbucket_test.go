package ratelimit

import (
	"testing"
	"time"
)

func TestNewTokenBucket(t *testing.T) {
	tb := NewTokenBucket(5000, time.Hour)
	if tb == nil {
		t.Fatal("expected non-nil bucket")
	}
	if tb.capacity != 5000 {
		t.Errorf("capacity = %f, want 5000", tb.capacity)
	}
	if tb.tokens != 5000 {
		t.Errorf("tokens = %f, want 5000", tb.tokens)
	}
}

func TestTokenBucket_Allow(t *testing.T) {
	tb := NewTokenBucket(3, time.Hour)
	if !tb.Allow() {
		t.Error("expected first request to be allowed")
	}
	if !tb.Allow() {
		t.Error("expected second request to be allowed")
	}
	if !tb.Allow() {
		t.Error("expected third request to be allowed")
	}
	if tb.Allow() {
		t.Error("expected fourth request to be denied")
	}
}

func TestTokenBucket_AllowN(t *testing.T) {
	tb := NewTokenBucket(10, time.Hour)
	if !tb.AllowN(10) {
		t.Error("expected AllowN(10) to succeed")
	}
	if tb.AllowN(1) {
		t.Error("expected AllowN(1) to fail after capacity exhausted")
	}
}

func TestTokenBucket_AllowN_LargerThanCapacity(t *testing.T) {
	tb := NewTokenBucket(3, time.Hour)
	if tb.AllowN(5) {
		t.Error("expected AllowN larger than capacity to fail")
	}
}

func TestTokenBucket_Remaining(t *testing.T) {
	tb := NewTokenBucket(10, time.Hour)
	if r := tb.Remaining(); r != 10 {
		t.Errorf("remaining = %d, want 10", r)
	}
	tb.AllowN(3)
	if r := tb.Remaining(); r != 7 {
		t.Errorf("remaining = %d, want 7", r)
	}
	tb.AllowN(7)
	if r := tb.Remaining(); r != 0 {
		t.Errorf("remaining = %d, want 0", r)
	}
}

func TestTokenBucket_Remaining_NeverNegative(t *testing.T) {
	tb := NewTokenBucket(5, time.Hour)
	tb.AllowN(10)
	if r := tb.Remaining(); r < 0 {
		t.Errorf("remaining should not be negative, got %d", r)
	}
}

func TestTokenBucket_Refill(t *testing.T) {
	tb := NewTokenBucket(100, 10*time.Millisecond)
	tb.AllowN(100)

	if r := tb.Remaining(); r != 0 {
		t.Errorf("remaining = %d, want 0", r)
	}

	time.Sleep(15 * time.Millisecond)

	r := tb.Remaining()
	if r <= 0 {
		t.Errorf("expected positive remaining after refill, got %d", r)
	}
}

func TestTokenBucket_Check(t *testing.T) {
	tb := NewTokenBucket(5, time.Hour)
	info := tb.Check()
	if !info.Allowed {
		t.Error("expected check to show allowed")
	}
	if info.Remaining != 5 {
		t.Errorf("remaining = %d, want 5", info.Remaining)
	}
}

func TestTokenBucket_AllowWithInfo(t *testing.T) {
	tb := NewTokenBucket(3, time.Hour)
	info := tb.AllowWithInfo()
	if !info.Allowed {
		t.Error("expected allowed")
	}
	if info.Remaining != 2 {
		t.Errorf("remaining = %d, want 2", info.Remaining)
	}

	tb.AllowN(2)
	info = tb.AllowWithInfo()
	if info.Allowed {
		t.Error("expected not allowed")
	}
	if info.Remaining != 0 {
		t.Errorf("remaining = %d, want 0", info.Remaining)
	}
}

func TestTokenBucket_ResetTime(t *testing.T) {
	tb := NewTokenBucket(1, time.Hour)
	tb.Allow()
	rt := tb.ResetTime()
	if rt.IsZero() {
		t.Error("expected non-zero reset time")
	}
	if rt.Before(time.Now()) {
		t.Error("expected reset time in the future")
	}
}

func TestTokenBucket_ResetTime_Full(t *testing.T) {
	tb := NewTokenBucket(100, time.Hour)
	rt := tb.ResetTime()
	if rt.After(time.Now().Add(time.Second)) {
		t.Error("expected reset time at or near now when bucket is full")
	}
}

func TestTokenBucket_ConcurrentSafety(t *testing.T) {
	tb := NewTokenBucket(1000, time.Hour)
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				tb.Allow()
				tb.Remaining()
				tb.Check()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestRateLimitError(t *testing.T) {
	err := &RateLimitError{Remaining: 0, ResetAt: time.Now().Add(time.Hour)}
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}