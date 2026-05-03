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

func TestEstimateLeanProxySchemaTokens(t *testing.T) {
	te := NewTokenEstimator()

	tokens := te.EstimateLeanProxySchemaTokens()

	if tokens <= 0 {
		t.Errorf("EstimateLeanProxySchemaTokens() = %d, want > 0", tokens)
	}

	if tokens > 300 {
		t.Errorf("EstimateLeanProxySchemaTokens() = %d, should be < 300 (actual measured: ~160)", tokens)
	}
}

func TestEstimateNativeMCPOverhead(t *testing.T) {
	te := NewTokenEstimator()

	tests := []struct {
		name       string
		toolCount  int
		minTokens  int
		maxTokens  int
	}{
		{"small server", 10, 500, 1000},
		{"medium server", 35, 1500, 3500},
		{"large server", 50, 2500, 6000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := te.EstimateNativeMCPOverhead("test-server", tt.toolCount)
			if tokens < tt.minTokens || tokens > tt.maxTokens {
				t.Errorf("EstimateNativeMCPOverhead(%d tools) = %d, want between %d and %d",
					tt.toolCount, tokens, tt.minTokens, tt.maxTokens)
			}
		})
	}
}

func TestCompareMCPConfigurations(t *testing.T) {
	te := NewTokenEstimator()

	servers := map[string]MCPServerConfig{
		"github":     {Command: "npx", Args: []string{"@github/mcp-server"}},
		"slack":      {Command: "npx", Args: []string{"@slack/mcp-server"}},
		"filesystem": {Command: "npx", Args: []string{"@modelcontextprotocol/server-filesystem"}},
	}

	result := te.CompareMCPConfigurations(servers)

	if result.NativeMCPTokens <= 0 {
		t.Errorf("NativeMCPTokens = %d, want > 0", result.NativeMCPTokens)
	}

	if result.LeanProxyTokens <= 0 {
		t.Errorf("LeanProxyTokens = %d, want > 0", result.LeanProxyTokens)
	}

	if result.SavedTokens <= 0 {
		t.Errorf("SavedTokens = %d, want > 0", result.SavedTokens)
	}

	if result.SavingsPercent <= 0 {
		t.Errorf("SavingsPercent = %.2f, want > 0", result.SavingsPercent)
	}

	if len(result.ServerBreakdown) != len(servers) {
		t.Errorf("ServerBreakdown length = %d, want %d", len(result.ServerBreakdown), len(servers))
	}

	if len(result.MonthlySavings) == 0 {
		t.Error("MonthlySavings should not be empty")
	}
}

func TestCalculateMonthlySavings(t *testing.T) {
	te := NewTokenEstimator()

	savedTokensPerSession := 60000
	sessionsPerMonth := 100

	monthlySavings := te.calculateMonthlySavings(savedTokensPerSession, sessionsPerMonth)

	if len(monthlySavings) == 0 {
		t.Error("Expected monthly savings for multiple providers")
	}

	for provider, saving := range monthlySavings {
		if saving.Sessions != sessionsPerMonth {
			t.Errorf("%s: Sessions = %d, want %d", provider, saving.Sessions, sessionsPerMonth)
		}
		if saving.SavingsUSD <= 0 {
			t.Errorf("%s: SavingsUSD = %.2f, want > 0", provider, saving.SavingsUSD)
		}
		if saving.InputRate <= 0 || saving.OutputRate <= 0 {
			t.Errorf("%s: Invalid rates - input: %.2f, output: %.2f", provider, saving.InputRate, saving.OutputRate)
		}
	}
}
