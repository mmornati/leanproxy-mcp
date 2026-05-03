package utils

import (
	"math"
	"testing"
)

func TestEstimateTokens(t *testing.T) {
	te := NewTokenEstimator()

	tests := []struct {
		name     string
		content  string
		expected int
	}{
		{"empty string", "", 0},
		{"4 characters", "test", 1},
		{"16 characters", "abcdefghijklmnop", 4},
		{"17 characters (ceil)", "abcdefghijklmnopq", 5},
		{"exact division", "abcdefghijklmnop", 4},
		{"single character", "a", 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := te.EstimateTokens(tt.content)
			if result != tt.expected {
				t.Errorf("EstimateTokens(%q) = %d, want %d", tt.content, result, tt.expected)
			}
		})
	}
}

func TestCalculateSavings(t *testing.T) {
	te := NewTokenEstimator()

	tests := []struct {
		name             string
		original         string
		optimized        string
		wantOriginal     int
		wantOptimized    int
		wantSaved        int
		wantSavingsPct   float64
	}{
		{
			name:           "both empty",
			original:       "",
			optimized:      "",
			wantOriginal:   0,
			wantOptimized:  0,
			wantSaved:      0,
			wantSavingsPct: 0,
		},
		{
			name:           "50% savings",
			original:       "abcdefghijklmnop",
			optimized:      "ab",
			wantOriginal:   4,
			wantOptimized:  1,
			wantSaved:      3,
			wantSavingsPct: 75.0,
		},
		{
			name:           "100% savings",
			original:       "abcdefghijklmnop",
			optimized:      "",
			wantOriginal:   4,
			wantOptimized:  0,
			wantSaved:      4,
			wantSavingsPct: 100.0,
		},
		{
			name:           "0% savings",
			original:       "ab",
			optimized:      "abcdefghijklmnop",
			wantOriginal:   1,
			wantOptimized:  1,
			wantSaved:      0,
			wantSavingsPct: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := te.CalculateSavings(tt.original, tt.optimized)
			if err != nil {
				t.Fatalf("CalculateSavings() error = %v", err)
			}

			if result.OriginalTokens != tt.wantOriginal {
				t.Errorf("OriginalTokens = %d, want %d", result.OriginalTokens, tt.wantOriginal)
			}
			if result.OptimizedTokens != tt.wantOptimized {
				t.Errorf("OptimizedTokens = %d, want %d", result.OptimizedTokens, tt.wantOptimized)
			}
			if result.SavedTokens != tt.wantSaved {
				t.Errorf("SavedTokens = %d, want %d", result.SavedTokens, tt.wantSaved)
			}
			if math.Abs(result.SavingsPercent-tt.wantSavingsPct) > 0.001 {
				t.Errorf("SavingsPercent = %.2f, want %.2f", result.SavingsPercent, tt.wantSavingsPct)
			}
		})
	}
}

func TestEstimateTokensAccuracy(t *testing.T) {
	te := NewTokenEstimator()

	knownStrings := []struct {
		content string
		tokens  int
	}{
		{"hello", 2},
		{"hello world", 3},
		{"The quick brown fox jumps over the lazy dog", 11},
	}

	for _, ks := range knownStrings {
		result := te.EstimateTokens(ks.content)
		if result != ks.tokens {
			t.Errorf("EstimateTokens(%q) = %d, want %d (approx)", ks.content, result, ks.tokens)
		}
	}
}
