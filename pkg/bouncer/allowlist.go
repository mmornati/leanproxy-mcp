package bouncer

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
)

type SecretPattern struct {
	Name        string
	Pattern     *regexp.Regexp
	Example     string
	Description string
}

var BuiltInPatterns = []SecretPattern{
	{
		Name:        "aws-access-key",
		Pattern:     regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		Example:     "AKIAIOSFODNN7EXAMPLE",
		Description: "AWS Access Key ID (20 characters, starts with AKIA)",
	},
	{
		Name:        "github-classic-pat",
		Pattern:     regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`),
		Example:     "ghp_abcdefghijklmnopqrstuvwxyz1234567890abcd",
		Description: "GitHub Classic Personal Access Token (starts with ghp_)",
	},
	{
		Name:        "github-fine-grained-pat",
		Pattern:     regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
		Example:     "github_pat_11abcdefghIJ9xsQ_xxxxxxxxxxxxxxxxx",
		Description: "GitHub Fine-grained PAT (starts with github_pat_)",
	},
	{
		Name:        "stripe-secret-key",
		Pattern:     regexp.MustCompile(`sk_live_[A-Za-z0-9]{24}`),
		Example:     "[Stripe Live Secret Key - 24 chars after sk_live_]",
		Description: "Stripe Live Secret Key (starts with sk_live_)",
	},
	{
		Name:        "stripe-publishable-key",
		Pattern:     regexp.MustCompile(`pk_live_[A-Za-z0-9]{24}`),
		Example:     "[Stripe Publishable Key - 24 chars after pk_live_]",
		Description: "Stripe Live Publishable Key (starts with pk_live_)",
	},
	{
		Name:        "generic-api-key",
		Pattern:     regexp.MustCompile(`(?i)(api[_-]?key)[_-]?[=]?[A-Za-z0-9]{16,}`),
		Example:     "api_key=abcdefghijklmnopqrstuvwx",
		Description: "Generic API key pattern (case-insensitive)",
	},
	{
		Name:        "bearer-token",
		Pattern:     regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`),
		Example:     "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		Description: "JWT Bearer token (three base64url segments)",
	},
	{
		Name:        "env-var-value",
		Pattern:     regexp.MustCompile(`\$[A-Z_][A-Z0-9_]{0,30}=([^\s,}]+)`),
		Example:     "$API_KEY=secret123",
		Description: "Environment variable assignment",
	},
}

func ValidatePatterns() error {
	for _, p := range BuiltInPatterns {
		if p.Pattern == nil {
			return fmt.Errorf("allowlist: pattern %q has nil regexp", p.Name)
		}
		if p.Name == "" {
			return fmt.Errorf("allowlist: pattern has empty name")
		}
	}
	return nil
}

func GetPatternNames() []string {
	names := make([]string, len(BuiltInPatterns))
	for i, p := range BuiltInPatterns {
		names[i] = p.Name
	}
	return names
}

func GetPatternByName(name string) *SecretPattern {
	for i := range BuiltInPatterns {
		if BuiltInPatterns[i].Name == name {
			return &BuiltInPatterns[i]
		}
	}
	return nil
}

func GetBuiltInPatterns() []SecretPattern {
	return BuiltInPatterns
}

func CompileCustomPatterns(configs []PatternConfig) ([]*regexp.Regexp, error) {
	var patterns []*regexp.Regexp
	for _, c := range configs {
		if c.Name == "" || c.Pattern == "" {
			continue
		}
		re, err := regexp.Compile(c.Pattern)
		if err != nil {
			return nil, fmt.Errorf("allowlist: invalid pattern %q: %w", c.Name, err)
		}
		patterns = append(patterns, re)
	}
	return patterns, nil
}

type PatternConfig struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
}

func (pc PatternConfig) Validate() error {
	if pc.Name == "" {
		return fmt.Errorf("allowlist: pattern config has empty name")
	}
	if pc.Pattern == "" {
		return fmt.Errorf("allowlist: pattern config %q has empty pattern", pc.Name)
	}
	if _, err := regexp.Compile(pc.Pattern); err != nil {
		return fmt.Errorf("allowlist: pattern config %q has invalid regexp: %w", pc.Name, err)
	}
	return nil
}

func PatternsToRegexps(patterns []SecretPattern) []*regexp.Regexp {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for i := range patterns {
		result = append(result, patterns[i].Pattern)
	}
	return result
}

func LoadPatternsWithLogging(customConfigs []PatternConfig) ([]*regexp.Regexp, []string) {
	patternCount := len(BuiltInPatterns)
	slog.Info("loading allow-list patterns", "count", patternCount)

	var skipped []string
	for i, p := range BuiltInPatterns {
		slog.Debug("pattern validated", "name", p.Name, "index", i)
	}

	if len(customConfigs) > 0 {
		for _, c := range customConfigs {
			if err := c.Validate(); err != nil {
				slog.Warn("invalid custom pattern skipped", "name", c.Name, "error", err.Error())
				skipped = append(skipped, c.Name)
			}
		}
	}

	allPatterns := PatternsToRegexps(BuiltInPatterns)
	return allPatterns, skipped
}

func MatchSecret(input string) []string {
	var matched []string
	for _, pattern := range BuiltInPatterns {
		if pattern.Pattern.MatchString(input) {
			matched = append(matched, pattern.Name)
		}
	}
	return matched
}

func RedactSecrets(input string) string {
	result := input
	for _, pattern := range BuiltInPatterns {
		result = pattern.Pattern.ReplaceAllString(result, SecretRedacted)
	}
	return result
}

func RedactWithPatterns(input string, patterns []*regexp.Regexp) string {
	result := input
	for _, pattern := range patterns {
		result = pattern.ReplaceAllString(result, SecretRedacted)
	}
	return result
}

func FormatPatternList() string {
	var lines []string
	for _, p := range BuiltInPatterns {
		lines = append(lines, fmt.Sprintf("- %s: %s (%s)", p.Name, p.Description, p.Example))
	}
	return strings.Join(lines, "\n")
}