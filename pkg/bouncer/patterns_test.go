package bouncer

import (
	"strings"
	"testing"
)

func patternByName(name string) *SecretPattern {
	for i := range BuiltInPatterns {
		if BuiltInPatterns[i].Name == name {
			return &BuiltInPatterns[i]
		}
	}
	return nil
}

func TestBuiltInPatterns(t *testing.T) {
	tests := []struct {
		name      string
		pattern   *SecretPattern
		input     string
		wantMatch bool
	}{
		{
			name:      "AWS Access Key",
			pattern:   patternByName("aws-access-key"),
			input:     "AKIAIOSFODNN7EXAMPLE",
			wantMatch: true,
		},
		{
			name:      "AWS Access Key invalid",
			pattern:   patternByName("aws-access-key"),
			input:     "AKIA1234",
			wantMatch: false,
		},
		{
			name:      "GitHub Personal Token",
			pattern:   patternByName("github-classic-pat"),
			input:     "ghp_123456789012345678901234567890123456",
			wantMatch: true,
		},
		{
			name:      "GitHub Fine-grained PAT",
			pattern:   patternByName("github-fine-grained-pat"),
			input:     "github_pat_11AAAAAAAAAAAAAAA_BBBBBBBBBBBBBBBBBBB",
			wantMatch: true,
		},
		{
			name:      "Stripe Live Secret Key",
			pattern:   patternByName("stripe-secret-key"),
			input:     "sk_live_" + strings.Repeat("A", 24),
			wantMatch: true,
		},
		{
			name:      "Stripe Live Publishable Key",
			pattern:   patternByName("stripe-publishable-key"),
			input:     "pk_live_" + strings.Repeat("A", 24),
			wantMatch: true,
		},
		{
			name:      "OpenAI API Key",
			pattern:   patternByName("openai-api-key"),
			input:     "sk-" + strings.Repeat("a", 48),
			wantMatch: true,
		},
		{
			name:      "Anthropic API Key",
			pattern:   patternByName("anthropic-api-key"),
			input:     "sk-ant-api03-" + strings.Repeat("a", 40),
			wantMatch: true,
		},
		{
			name:      "GitLab PAT",
			pattern:   patternByName("gitlab-pat"),
			input:     "glpat-" + strings.Repeat("a", 20),
			wantMatch: true,
		},
		{
			name:      "Slack Token (bot)",
			pattern:   patternByName("slack-token"),
			input:     "xoxb-1234567890-1234567890123-" + strings.Repeat("a", 20),
			wantMatch: true,
		},
		{
			name:      "GCP OAuth Token",
			pattern:   patternByName("gcp-oauth-token"),
			input:     "ya29." + strings.Repeat("a", 20),
			wantMatch: true,
		},
		{
			name:      "GCP Service Account marker",
			pattern:   patternByName("gcp-service-account"),
			input:     `{"type": "service_account", "project_id": "x"}`,
			wantMatch: true,
		},
		{
			name:      "PEM Private Key",
			pattern:   patternByName("pem-private-key"),
			input:     "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQ\n-----END RSA PRIVATE KEY-----",
			wantMatch: true,
		},
		{
			name:      "PEM Certificate",
			pattern:   patternByName("pem-certificate"),
			input:     "-----BEGIN CERTIFICATE-----\nMIIDdzCCAl+g\n-----END CERTIFICATE-----",
			wantMatch: true,
		},
		{
			name:      "Generic API Key case insensitive",
			pattern:   patternByName("generic-api-key"),
			input:     "api_key=abcdefghijklmnopqrstuvwxyz123456",
			wantMatch: true,
		},
		{
			name:      "Bearer Token",
			pattern:   patternByName("bearer-token"),
			input:     "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
			wantMatch: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.pattern == nil {
				t.Fatalf("pattern not found")
			}
			got := tt.pattern.Pattern.MatchString(tt.input)
			if got != tt.wantMatch {
				t.Errorf("pattern match = %v, want %v for input %q", got, tt.wantMatch, tt.input)
			}
		})
	}
}

func TestPatternCount(t *testing.T) {
	if len(BuiltInPatterns) != 16 {
		t.Errorf("expected 16 built-in patterns, got %d", len(BuiltInPatterns))
	}
}

func TestAllPatternsCompile(t *testing.T) {
	for i, p := range BuiltInPatterns {
		if p.Pattern == nil {
			t.Errorf("pattern at index %d has nil Pattern", i)
		}
	}
}
