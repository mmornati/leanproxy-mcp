package utils

import (
	"log/slog"
	"math"
)

const (
	charsPerToken = 4
)

type SavingsResult struct {
	OriginalTokens  int
	OptimizedTokens int
	SavedTokens     int
	SavingsPercent  float64
	Breakdown       map[string]int
}

type TokenEstimator struct{}

func NewTokenEstimator() *TokenEstimator {
	return &TokenEstimator{}
}

func (t *TokenEstimator) EstimateTokens(content string) int {
	if content == "" {
		return 0
	}
	return int(math.Ceil(float64(len(content)) / charsPerToken))
}

func (t *TokenEstimator) CalculateSavings(original, optimized string) (SavingsResult, error) {
	if original == "" && optimized == "" {
		return SavingsResult{
			OriginalTokens:  0,
			OptimizedTokens: 0,
			SavedTokens:     0,
			SavingsPercent:  0,
			Breakdown:       make(map[string]int),
		}, nil
	}

	originalTokens := t.EstimateTokens(original)
	optimizedTokens := t.EstimateTokens(optimized)

	if optimizedTokens > originalTokens {
		slog.Warn("optimized token count exceeds original",
			"original", originalTokens,
			"optimized", optimizedTokens)
		optimizedTokens = originalTokens
	}

	savedTokens := originalTokens - optimizedTokens
	var savingsPercent float64
	if originalTokens > 0 {
		savingsPercent = float64(savedTokens) / float64(originalTokens) * 100
	}

	return SavingsResult{
		OriginalTokens:  originalTokens,
		OptimizedTokens: optimizedTokens,
		SavedTokens:     savedTokens,
		SavingsPercent:  savingsPercent,
		Breakdown:       make(map[string]int),
	}, nil
}
