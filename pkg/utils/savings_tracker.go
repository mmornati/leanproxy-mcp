package utils

import (
	"fmt"
	"log/slog"
	"sync"
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

type ServerSavings struct {
	ServerName      string
	OriginalTokens  int64
	OptimizedTokens int64
	SavedTokens     int64
}

type CumulativeSavings struct {
	TotalOriginal     int64
	TotalOptimized    int64
	TotalSaved        int64
	SessionDuration   time.Duration
	RequestsProcessed int
}

type SavingsTracker struct {
	sessionStart   time.Time
	totalOriginal  int64
	totalOptimized int64
	serverSavings  map[string]*ServerSavings
	mu             sync.Mutex
	clock          Clock
}

func NewSavingsTracker() *SavingsTracker {
	return newSavingsTracker(defaultClock)
}

func newSavingsTracker(clock Clock) *SavingsTracker {
	return &SavingsTracker{
		sessionStart:  clock.Now(),
		serverSavings: make(map[string]*ServerSavings),
		clock:         clock,
	}
}

func (s *SavingsTracker) RecordRequest(serverName string, original, optimized string) error {
	te := NewTokenEstimator()
	result, err := te.CalculateSavings(original, optimized)
	if err != nil {
		return fmt.Errorf("token savings: context: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.totalOriginal += int64(result.OriginalTokens)
	s.totalOptimized += int64(result.OptimizedTokens)

	if _, exists := s.serverSavings[serverName]; !exists {
		s.serverSavings[serverName] = &ServerSavings{
			ServerName: serverName,
		}
	}

	s.serverSavings[serverName].OriginalTokens += int64(result.OriginalTokens)
	s.serverSavings[serverName].OptimizedTokens += int64(result.OptimizedTokens)
	s.serverSavings[serverName].SavedTokens += int64(result.SavedTokens)

	slog.Info("token_savings",
		"server", serverName,
		"original", result.OriginalTokens,
		"optimized", result.OptimizedTokens,
		"saved", result.SavedTokens,
		"pct", result.SavingsPercent,
	)

	return nil
}

func (s *SavingsTracker) GetCumulativeSavings() CumulativeSavings {
	s.mu.Lock()
	defer s.mu.Unlock()

	return CumulativeSavings{
		TotalOriginal:     s.totalOriginal,
		TotalOptimized:    s.totalOptimized,
		TotalSaved:        s.totalOriginal - s.totalOptimized,
		SessionDuration:   s.clock.Since(s.sessionStart),
		RequestsProcessed: len(s.serverSavings),
	}
}

func (s *SavingsTracker) GetServerBreakdown() map[string]ServerSavings {
	s.mu.Lock()
	defer s.mu.Unlock()

	result := make(map[string]ServerSavings)
	for name, ss := range s.serverSavings {
		result[name] = *ss
	}
	return result
}

func (s *SavingsTracker) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionStart = s.clock.Now()
	s.totalOriginal = 0
	s.totalOptimized = 0
	s.serverSavings = make(map[string]*ServerSavings)
}
