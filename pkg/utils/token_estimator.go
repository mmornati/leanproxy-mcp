package utils

import (
	"encoding/json"
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

type MCPServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type MCPServerAnalysis struct {
	Name             string `json:"name"`
	EstimatedTokens  int    `json:"estimated_tokens"`
	ToolCount        int    `json:"tool_count"`
	EstimatedPerTool int    `json:"estimated_per_tool"`
}

type ComparisonResult struct {
	NativeMCPTokens    int                      `json:"native_mcp_tokens"`
	LeanProxyTokens    int                      `json:"leanproxy_tokens"`
	SavedTokens        int                      `json:"saved_tokens"`
	SavingsPercent     float64                  `json:"savings_percent"`
	ServerBreakdown    []MCPServerAnalysis      `json:"server_breakdown"`
	MonthlySavings     map[string]MonthlySaving `json:"monthly_savings"`
}

type MonthlySaving struct {
	Model       string  `json:"model"`
	Sessions    int     `json:"sessions"`
	SavingsUSD  float64 `json:"savings_usd"`
	InputRate   float64 `json:"input_rate"`
	OutputRate  float64 `json:"output_rate"`
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

func (t *TokenEstimator) EstimateLeanProxySchemaTokens() int {
	gatewayTools := []map[string]interface{}{
		{
			"name":        "invoke_tool",
			"description": "Invoke a tool on a specific MCP server",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{
					"server_name": map[string]interface{}{"type": "string"},
					"tool_name":   map[string]interface{}{"type": "string"},
					"arguments":   map[string]interface{}{"type": "object"},
				},
				"required": []string{"server_name", "tool_name"},
			},
		},
		{
			"name":        "search_tools",
			"description": "Search for tools across all configured MCP servers",
			"inputSchema": map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"query": map[string]interface{}{"type": "string"}},
				"required":   []string{"query"},
			},
		},
	}

	jsonBytes, _ := json.Marshal(gatewayTools)
	return t.EstimateTokens(string(jsonBytes))
}

func (t *TokenEstimator) EstimateNativeMCPOverhead(serverName string, toolCount int) int {
	avgSchemaPerTool := 75
	return toolCount * avgSchemaPerTool
}

func (t *TokenEstimator) CompareMCPConfigurations(servers map[string]MCPServerConfig) ComparisonResult {
	var totalNativeTokens int
	var serverBreakdown []MCPServerAnalysis

	avgToolsPerServer := 35
	for name := range servers {
		serverTokens := t.EstimateNativeMCPOverhead(name, avgToolsPerServer)
		totalNativeTokens += serverTokens
		serverBreakdown = append(serverBreakdown, MCPServerAnalysis{
			Name:             name,
			EstimatedTokens:  serverTokens,
			ToolCount:        avgToolsPerServer,
			EstimatedPerTool: serverTokens / avgToolsPerServer,
		})
	}

	leanProxyTokens := t.EstimateLeanProxySchemaTokens()
	savedTokens := totalNativeTokens - leanProxyTokens

	var savingsPercent float64
	if totalNativeTokens > 0 {
		savingsPercent = float64(savedTokens) / float64(totalNativeTokens) * 100
	}

	result := ComparisonResult{
		NativeMCPTokens: totalNativeTokens,
		LeanProxyTokens: leanProxyTokens,
		SavedTokens:     savedTokens,
		SavingsPercent:  savingsPercent,
		ServerBreakdown: serverBreakdown,
		MonthlySavings:  t.calculateMonthlySavings(savedTokens, 100),
	}

	return result
}

func (t *TokenEstimator) calculateMonthlySavings(savedTokensPerSession int, sessionsPerMonth int) map[string]MonthlySaving {
	totalSavedTokens := savedTokensPerSession * sessionsPerMonth

	inputTokens := int(float64(totalSavedTokens) * 0.8)
	outputTokens := int(float64(totalSavedTokens) * 0.2)

	providers := map[string]struct {
		inputRate  float64
		outputRate float64
		model      string
	}{
		"OpenAI GPT-4o":     {inputRate: 2.50, outputRate: 10.00, model: "gpt-4o"},
		"OpenAI GPT-5.4":    {inputRate: 2.50, outputRate: 15.00, model: "gpt-5.4"},
		"Anthropic Sonnet":  {inputRate: 3.00, outputRate: 15.00, model: "claude-sonnet-4-6"},
		"Anthropic Opus":    {inputRate: 5.00, outputRate: 25.00, model: "claude-opus-4-7"},
		"Anthropic Haiku":   {inputRate: 1.00, outputRate: 5.00, model: "claude-haiku-4-5"},
	}

	monthlySavings := make(map[string]MonthlySaving)
	for name, pricing := range providers {
		inputCost := (float64(inputTokens) / 1_000_000) * pricing.inputRate
		outputCost := (float64(outputTokens) / 1_000_000) * pricing.outputRate
		monthlySavings[name] = MonthlySaving{
			Model:       pricing.model,
			Sessions:    sessionsPerMonth,
			SavingsUSD:  inputCost + outputCost,
			InputRate:   pricing.inputRate,
			OutputRate:  pricing.outputRate,
		}
	}

	return monthlySavings
}
