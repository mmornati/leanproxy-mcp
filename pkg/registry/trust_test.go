package registry

import (
	"testing"
	"time"
)

func TestCalculateTrustScore_UsesExistingScore(t *testing.T) {
	entry := RegistryFeedEntry{
		Name:       "test-server",
		TrustScore: 85,
	}
	if got := CalculateTrustScore(entry); got != 85 {
		t.Errorf("expected 85, got %d", got)
	}
}

func TestCalculateTrustScore_RecentRelease(t *testing.T) {
	entry := RegistryFeedEntry{
		Name:        "recent",
		LastRelease: time.Now().Format(time.RFC3339),
		OpenIssues:  0,
		Downloads:   100000,
		Description: "A great server",
		Categories:  []string{"tools"},
		Command:     "run",
	}
	score := CalculateTrustScore(entry)
	if score < 85 {
		t.Errorf("recent release + clean issues + high downloads + completeness should be high, got %d", score)
	}
}

func TestCalculateTrustScore_StaleRelease(t *testing.T) {
	entry := RegistryFeedEntry{
		Name:        "stale",
		LastRelease: "2020-01-01",
		OpenIssues:  200,
	}
	score := CalculateTrustScore(entry)
	if score > 20 {
		t.Errorf("stale release with many issues should be low, got %d", score)
	}
}

func TestCalculateTrustScore_MinimalData(t *testing.T) {
	entry := RegistryFeedEntry{
		Name: "minimal",
	}
	score := CalculateTrustScore(entry)
	if score < 0 || score > 100 {
		t.Errorf("score out of range: %d", score)
	}
}

func TestCalculateTrustScore_ScoreCappedAt100(t *testing.T) {
	entry := RegistryFeedEntry{
		Name:        "perfect",
		LastRelease: time.Now().Format(time.RFC3339),
		OpenIssues:  0,
		Downloads:   500000,
		Description: "Perfect server",
		Categories:  []string{"tools", "dev"},
		Command:     "run",
		URL:         "https://example.com",
	}
	score := CalculateTrustScore(entry)
	if score != 100 {
		t.Errorf("expected 100 (capped), got %d", score)
	}
}

func TestCalculateTrustScore_MediumTrust(t *testing.T) {
	entry := RegistryFeedEntry{
		Name:        "medium",
		LastRelease: time.Now().Add(-60 * 24 * time.Hour).Format(time.RFC3339),
		OpenIssues:  10,
		Downloads:   5000,
		Description: "Okay server",
		Categories:  []string{"tools"},
	}
	score := CalculateTrustScore(entry)
	if score < 40 || score >= 70 {
		t.Errorf("expected medium trust (40-69), got %d", score)
	}
}

func TestReleaseRecencyScore_ParseError(t *testing.T) {
	if got := releaseRecencyScore("not-a-date"); got != 5 {
		t.Errorf("expected 5 for unparseable date, got %d", got)
	}
}

func TestReleaseRecencyScore_FutureDate(t *testing.T) {
	future := time.Now().Add(365 * 24 * time.Hour).Format(time.RFC3339)
	if got := releaseRecencyScore(future); got != 30 {
		t.Errorf("expected 30 for future date, got %d", got)
	}
}

func TestIssueScore(t *testing.T) {
	tests := []struct {
		issues int
		want   int
	}{
		{0, 30},
		{3, 25},
		{10, 15},
		{50, 5},
		{200, 0},
	}
	for _, tt := range tests {
		if got := issueScore(tt.issues); got != tt.want {
			t.Errorf("issueScore(%d) = %d, want %d", tt.issues, got, tt.want)
		}
	}
}

func TestDownloadScore(t *testing.T) {
	tests := []struct {
		downloads int
		want      int
	}{
		{500000, 25},
		{50000, 20},
		{5000, 15},
		{500, 10},
		{50, 5},
		{0, 0},
	}
	for _, tt := range tests {
		if got := downloadScore(tt.downloads); got != tt.want {
			t.Errorf("downloadScore(%d) = %d, want %d", tt.downloads, got, tt.want)
		}
	}
}

func TestTrustLevel(t *testing.T) {
	tests := []struct {
		score int
		want  string
	}{
		{0, "low"},
		{20, "low"},
		{39, "low"},
		{40, "medium"},
		{55, "medium"},
		{69, "medium"},
		{70, "high"},
		{85, "high"},
		{100, "high"},
	}
	for _, tt := range tests {
		if got := TrustLevel(tt.score); got != tt.want {
			t.Errorf("TrustLevel(%d) = %s, want %s", tt.score, got, tt.want)
		}
	}
}

func TestFormatString(t *testing.T) {
	if got := FormatString(""); got != "-" {
		t.Errorf("empty string: got %q", got)
	}
	if got := FormatString("hello"); got != "hello" {
		t.Errorf("non-empty string: got %q", got)
	}
}

func TestFormatInt(t *testing.T) {
	if got := FormatInt(0); got != "-" {
		t.Errorf("zero int: got %q", got)
	}
	if got := FormatInt(42); got != "42" {
		t.Errorf("42: got %q", got)
	}
	if got := FormatInt(-1); got != "-" {
		t.Errorf("negative: got %q", got)
	}
}

func TestFormatInt64(t *testing.T) {
	if got := FormatInt64(0); got != "-" {
		t.Errorf("zero int64: got %q", got)
	}
	if got := FormatInt64(999); got != "999" {
		t.Errorf("999: got %q", got)
	}
}

func TestFormatTrustLabel(t *testing.T) {
	label := FormatTrustLabel(85)
	if label != "85 (high)" {
		t.Errorf("got %q", label)
	}
}

func TestIsLowTrust(t *testing.T) {
	if !IsLowTrust(0) {
		t.Error("0 should be low trust")
	}
	if !IsLowTrust(39) {
		t.Error("39 should be low trust")
	}
	if IsLowTrust(40) {
		t.Error("40 should not be low trust")
	}
	if IsLowTrust(100) {
		t.Error("100 should not be low trust")
	}
}

func TestFormatWarning(t *testing.T) {
	msg := FormatWarning("bad-server", 25)
	if msg == "" {
		t.Fatal("expected non-empty warning")
	}
	if !contains(msg, "--i-understand-the-risks") {
		t.Errorf("warning should mention --i-understand-the-risks: %s", msg)
	}
	if !contains(msg, "bad-server") {
		t.Errorf("warning should mention server name: %s", msg)
	}
	if !contains(msg, "25") {
		t.Errorf("warning should mention score: %s", msg)
	}
}

func TestTryParseDate_RFC3339(t *testing.T) {
	ts := time.Now().Add(-24 * time.Hour).Format(time.RFC3339)
	if got := tryParseDate(ts); got == nil {
		t.Error("expected parsed RFC3339 date")
	}
}

func TestTryParseDate_ISO8601(t *testing.T) {
	if got := tryParseDate("2025-06-01"); got == nil {
		t.Error("expected parsed ISO8601 date")
	}
}

func TestTryParseDate_Invalid(t *testing.T) {
	if got := tryParseDate("not-a-date"); got != nil {
		t.Error("expected nil for invalid date")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && containsStr(s, substr)
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
