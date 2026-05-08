package concurrent

import (
	"time"
)

type Clock interface {
	Now() time.Time
	Since(time.Time) time.Duration
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now()
}

func (RealClock) Since(t time.Time) time.Duration {
	return time.Since(t)
}

var defaultClock Clock = RealClock{}
