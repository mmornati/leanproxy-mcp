package utils

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGenerateMarkdownReport_BasicStructure(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-123",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 10,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:    1000,
			OptimizedTokens:   600,
			SavedTokens:       400,
			SavingsPercentage: 40.0,
			ByServer:          map[string]ServerTokenSavings{},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "# LeanProxy Session Report") {
		t.Error("Report should contain header")
	}
	if !strings.Contains(report, "## Summary") {
		t.Error("Report should contain Summary section")
	}
	if !strings.Contains(report, "## Token Savings") {
		t.Error("Report should contain Token Savings section")
	}
	if !strings.Contains(report, "## Security Events") {
		t.Error("Report should contain Security Events section")
	}
}

func TestGenerateMarkdownReport_TokenSavingsMetrics(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-456",
		SessionStart:  time.Now().Add(-2 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 25,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:    5000,
			OptimizedTokens:   3000,
			SavedTokens:       2000,
			SavingsPercentage: 40.0,
			ByServer: map[string]ServerTokenSavings{
				"test-server": {
					ServerName:      "test-server",
					OriginalTokens:  5000,
					OptimizedTokens: 3000,
					SavedTokens:     2000,
				},
			},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "Total Tokens Saved") {
		t.Error("Report should contain Total Tokens Saved")
	}
	if !strings.Contains(report, "2000 (40.00%)") {
		t.Error("Report should contain savings with percentage")
	}
	if !strings.Contains(report, "test-server") {
		t.Error("Report should contain server name")
	}
}

func TestGenerateMarkdownReport_SecurityMetrics(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:    "test-session-789",
		SessionStart: time.Now().Add(-30 * time.Minute),
		SessionEnd:   time.Now(),
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:  100,
			OptimizedTokens: 80,
			SavedTokens:     20,
			ByServer:        map[string]ServerTokenSavings{},
		},
		SecurityEvents: []SecurityEvent{
			{
				Timestamp:      time.Now().Add(-10 * time.Minute),
				EventType:      "api_key",
				PatternMatched: "openai-api-key",
				ServerName:     "server1",
				Redacted:       true,
			},
			{
				Timestamp:      time.Now().Add(-5 * time.Minute),
				EventType:      "secret",
				PatternMatched: "aws-secret-key",
				ServerName:     "server2",
				Redacted:       true,
			},
		},
		ServerMetrics: map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "Security Risks Intercepted") {
		t.Error("Report should contain Security Risks Intercepted")
	}
	if !strings.Contains(report, "API Keys Redacted") {
		t.Error("Report should contain API Keys Redacted count")
	}
	if !strings.Contains(report, "Secrets Redacted") {
		t.Error("Report should contain Secrets Redacted count")
	}
}

func TestGenerateMarkdownReport_EmptySession(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := rg.NewEmptySessionMetrics()

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "# LeanProxy Session Report") {
		t.Error("Report should contain header even for empty session")
	}
	if !strings.Contains(report, "no-session") {
		t.Error("Report should indicate no session data")
	}
}

func TestGenerateMarkdownReport_NoSecurityEvents(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-no-sec",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 5,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:  1000,
			OptimizedTokens: 700,
			SavedTokens:     300,
			ByServer:        map[string]ServerTokenSavings{},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "No security events") {
		t.Error("Report should indicate no security events")
	}
}

func TestGenerateMarkdownReport_NoTokenSavings(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-no-savings",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 0,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:  0,
			OptimizedTokens: 0,
			SavedTokens:     0,
			ByServer:        map[string]ServerTokenSavings{},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "No servers processed") {
		t.Error("Report should indicate no servers processed")
	}
}

func TestGenerateMarkdownReport_ServerBreakdownFormatting(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-breakdown",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 3,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:  3000,
			OptimizedTokens: 2000,
			SavedTokens:     1000,
			ByServer: map[string]ServerTokenSavings{
				"server-a": {
					ServerName:      "server-a",
					OriginalTokens:  1000,
					OptimizedTokens:  600,
					SavedTokens:      400,
				},
				"server-b": {
					ServerName:      "server-b",
					OriginalTokens:  2000,
					OptimizedTokens: 1400,
					SavedTokens:      600,
				},
			},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "server-a") {
		t.Error("Report should contain server-a")
	}
	if !strings.Contains(report, "server-b") {
		t.Error("Report should contain server-b")
	}
	if !strings.Contains(report, "1000") || !strings.Contains(report, "600") || !strings.Contains(report, "400") {
		t.Error("Report should contain token counts")
	}
}

func TestGenerateJSONReport_BasicStructure(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-json",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 15,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:    2000,
			OptimizedTokens:   1200,
			SavedTokens:       800,
			SavingsPercentage: 40.0,
			ByServer:          map[string]ServerTokenSavings{},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateJSONReport(sessionData)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(report), &result)
	if err != nil {
		t.Errorf("JSON report should be valid JSON: %v", err)
	}

	if _, ok := result["session_id"]; !ok {
		t.Error("JSON report should contain session_id")
	}
	if _, ok := result["summary"]; !ok {
		t.Error("JSON report should contain summary")
	}
	if _, ok := result["token_savings"]; !ok {
		t.Error("JSON report should contain token_savings")
	}
}

func TestGenerateJSONReport_AllMetricsPreserved(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-full",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 20,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:    5000,
			OptimizedTokens:   3000,
			SavedTokens:       2000,
			SavingsPercentage: 40.0,
			ByServer: map[string]ServerTokenSavings{
				"test-server": {
					ServerName:      "test-server",
					OriginalTokens:  5000,
					OptimizedTokens: 3000,
					SavedTokens:     2000,
				},
			},
		},
		SecurityEvents: []SecurityEvent{
			{
				Timestamp:      time.Now(),
				EventType:      "api_key",
				PatternMatched: "api-key-pattern",
				ServerName:     "test-server",
				Redacted:       true,
			},
		},
		ServerMetrics: map[string]ServerMetrics{},
	}

	report := rg.GenerateJSONReport(sessionData)

	var result map[string]interface{}
	err := json.Unmarshal([]byte(report), &result)
	if err != nil {
		t.Errorf("JSON report should be valid JSON: %v", err)
	}

	summary := result["summary"].(map[string]interface{})
	if int(summary["total_requests"].(float64)) != 20 {
		t.Error("JSON report should preserve total requests")
	}
	if int(summary["total_tokens_saved"].(float64)) != 2000 {
		t.Error("JSON report should preserve token savings")
	}
}

func TestGenerateMarkdownReport_SpecialCharactersInServerNames(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-special",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 1,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:  100,
			OptimizedTokens: 80,
			SavedTokens:     20,
			ByServer: map[string]ServerTokenSavings{
				"server|with|pipes": {
					ServerName:      "server|with|pipes",
					OriginalTokens:  100,
					OptimizedTokens: 80,
					SavedTokens:     20,
				},
			},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if strings.Contains(report, "server|with|pipes") {
		t.Error("Special characters should be escaped in Markdown tables")
	}
}

func TestGenerateMarkdownReport_OptimizationBreakdown(t *testing.T) {
	rg := NewReportGenerator()
	sessionData := SessionMetrics{
		SessionID:     "test-session-breakdown",
		SessionStart:  time.Now().Add(-1 * time.Hour),
		SessionEnd:    time.Now(),
		TotalRequests: 4,
		TokenSavings: TokenSavingsSummary{
			OriginalTokens:  4000,
			OptimizedTokens: 2500,
			SavedTokens:     1500,
			ByServer: map[string]ServerTokenSavings{
				"discovery_signatures": {
					ServerName:      "discovery_signatures",
					OriginalTokens:  1000,
					OptimizedTokens: 600,
					SavedTokens:     400,
				},
				"jit_schema_injection": {
					ServerName:      "jit_schema_injection",
					OriginalTokens:  1000,
					OptimizedTokens: 650,
					SavedTokens:     350,
				},
				"boilerplate_pruning": {
					ServerName:      "boilerplate_pruning",
					OriginalTokens:  1000,
					OptimizedTokens: 700,
					SavedTokens:     300,
				},
				"manifest_compaction": {
					ServerName:      "manifest_compaction",
					OriginalTokens:  1000,
					OptimizedTokens: 550,
					SavedTokens:     450,
				},
			},
		},
		SecurityEvents: []SecurityEvent{},
		ServerMetrics:  map[string]ServerMetrics{},
	}

	report := rg.GenerateMarkdownReport(sessionData)

	if !strings.Contains(report, "Discovery Signatures") {
		t.Error("Report should contain Discovery Signatures breakdown")
	}
	if !strings.Contains(report, "JIT Schema Injection") {
		t.Error("Report should contain JIT Schema Injection breakdown")
	}
	if !strings.Contains(report, "Boilerplate Pruning") {
		t.Error("Report should contain Boilerplate Pruning breakdown")
	}
	if !strings.Contains(report, "Manifest Compaction") {
		t.Error("Report should contain Manifest Compaction breakdown")
	}
}
