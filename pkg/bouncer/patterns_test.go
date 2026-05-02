package bouncer

import (
	"regexp"
	"strings"
	"testing"
)

func TestBuiltInPatterns(t *testing.T) {
	tests := []struct {
		name      string
		pattern   *regexp.Regexp
		input     string
		wantMatch bool
	}{
		{
			name:      "AWS Access Key",
			pattern:   BuiltInPatterns[0],
			input:     "AKIAIOSFODNN7EXAMPLE",
			wantMatch: true,
		},
		{
			name:      "AWS Access Key invalid",
			pattern:   BuiltInPatterns[0],
			input:     "AKIA1234",
			wantMatch: false,
		},
		{
			name:      "GitHub Personal Token",
			pattern:   BuiltInPatterns[1],
			input:     "ghp_123456789012345678901234567890123456",
			wantMatch: true,
		},
		{
			name:      "GitHub Fine-grained PAT",
			pattern:   BuiltInPatterns[2],
			input:     "github_pat_11AAAAAAAAAAAAAAA_BBBBBBBBBBBBBBBBBBB",
			wantMatch: true,
		},
		{
			name:      "Stripe Live Secret Key",
			pattern:   BuiltInPatterns[3],
			input:     "sk_live_" + strings.Repeat("A", 24),
			wantMatch: true,
		},
		{
			name:      "Stripe Live Publishable Key",
			pattern:   BuiltInPatterns[4],
			input:     "pk_live_" + strings.Repeat("A", 24),
			wantMatch: true,
		},
		{
			name:      "Generic API Key case insensitive",
			pattern:   BuiltInPatterns[5],
			input:     "api_key=abcdefghijklmnopqrstuvwxyz123456",
			wantMatch: true,
		},
		{
			name:      "Bearer Token",
			pattern:   BuiltInPatterns[6],
			input:     "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.pattern.MatchString(tt.input)
			if got != tt.wantMatch {
				t.Errorf("pattern match = %v, want %v for input %q", got, tt.wantMatch, tt.input)
			}
		})
	}
}

func TestPatternCount(t *testing.T) {
	if len(BuiltInPatterns) != 7 {
		t.Errorf("expected 7 built-in patterns, got %d", len(BuiltInPatterns))
	}
}

func TestAllPatternsCompile(t *testing.T) {
	for i, p := range BuiltInPatterns {
		if p == nil {
			t.Errorf("pattern at index %d is nil", i)
		}
	}
}