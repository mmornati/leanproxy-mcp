package concurrent

import (
	"sync"
	"time"
)

type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func NewFakeClock(t time.Time) *FakeClock {
	return &FakeClock{now: t}
}

func (fc *FakeClock) Now() time.Time {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now
}

func (fc *FakeClock) Since(t time.Time) time.Duration {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	return fc.now.Sub(t)
}

func (fc *FakeClock) Add(d time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.now = fc.now.Add(d)
}

func (fc *FakeClock) Set(t time.Time) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.now = t
}
