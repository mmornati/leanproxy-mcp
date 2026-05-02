package bouncer

import (
	"regexp"
	"strings"
	"testing"
)

func TestValidatePatterns(t *testing.T) {
	if err := ValidatePatterns(); err != nil {
		t.Fatalf("ValidatePatterns failed: %v", err)
	}
}

func TestAWSKeyPattern(t *testing.T) {
	valid := []string{
		"AKIAIOSFODNN7EXAMPLE",
		"AKIAJ7XGSJBSWYZXCDER",
		"AKIA0123456789ABCDEF",
	}
	invalid := []string{
		"akiaIOSFODNN7EXAMPLE",
		"AKIA1234567890",
		"AKIAAAA1234567890AB",
		"akia2IOSFODNN7EXAMPLE",
	}

	awsPattern := BuiltInPatterns[0].Pattern
	for _, v := range valid {
		if !awsPattern.MatchString(v) {
			t.Errorf("AWS pattern should match valid key: %q", v)
		}
	}
	for _, inv := range invalid {
		if awsPattern.MatchString(inv) {
			t.Errorf("AWS pattern should NOT match: %q", inv)
		}
	}
}

func TestGitHubClassicPATPattern(t *testing.T) {
	ghPattern := BuiltInPatterns[1].Pattern

	valid := []string{
		"ghp_abcdefghijklmnopqrstuvwxyz123456abcdef", // 40 chars (4 prefix + 36)
		"ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ123456ABCDEF", // 40 chars
		"ghp_123456789012345678901234567890123456",   // 40 chars
	}
	invalid := []string{
		"ghx_abcdefghijklmnopqrstuvwxyz1234567890abcd",  // wrong prefix
		"GHP_abcdefghijklmnopqrstuvwxyz1234567890abcd",  // uppercase prefix
		"ghp abcdefghijklmnopqrstuvwxyz1234567890abcd",  // space in prefix
	}

	for _, v := range valid {
		if !ghPattern.MatchString(v) {
			t.Errorf("GitHub classic PAT pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if ghPattern.MatchString(inv) {
			t.Errorf("GitHub classic PAT pattern should NOT match: %q", inv)
		}
	}
}

func TestGitHubFineGrainedPATPattern(t *testing.T) {
	valid := []string{
		"github_pat_11abcdefghIJ9xsQ_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
		"github_pat_11AAAAAAAAAAAAAAA_BBBBBBBBBBBBBBBBBBB",
		"github_pat_12abcABCabc123_xyzXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
	}
	invalid := []string{
		"github_pat_1abcdefghijklmnop", // too short
		"github_pat_11",                // too short
		"Github_pat_11abcdefghIJKLM_xxxxx",
		"github_pat_11abcdefghIJKLM_", // underscore at end ok, but pattern requires more
	}

	ghFGPattern := BuiltInPatterns[2].Pattern
	for _, v := range valid {
		if !ghFGPattern.MatchString(v) {
			t.Errorf("GitHub fine-grained PAT pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if ghFGPattern.MatchString(inv) {
			t.Errorf("GitHub fine-grained PAT pattern should NOT match: %q", inv)
		}
	}
}

func TestStripeKeyPattern(t *testing.T) {
	valid := []string{
		"sk_live_" + strings.Repeat("x", 24),
		"sk_live_" + strings.Repeat("x", 24),
		"sk_live_" + strings.Repeat("x", 24),
	}
	invalid := []string{
		"test_key_not_stripe_format_32charsXXX",
		"sk_live_short",
		"sk_live_" + strings.Repeat("x", 23),
	}

	stripePattern := BuiltInPatterns[3].Pattern
	for _, v := range valid {
		if !stripePattern.MatchString(v) {
			t.Errorf("Stripe secret key pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if stripePattern.MatchString(inv) {
			t.Errorf("Stripe secret key pattern should NOT match: %q", inv)
		}
	}
}

func TestStripePublishableKeyPattern(t *testing.T) {
	valid := []string{
		"pk_live_AbCdEfGhIjKlMnOpQrStUvWx",
		"pk_live_" + strings.Repeat("x", 24),
		"pk_live_aBcDeFgHiJkLmNoPqRsTuVwXyZaBcDeF",
	}
	invalidPK := []string{
		"pk_test_xxxxxxxxxxxxxxxxxxxxxxxx",
		"pk_live_short",
		"pk_live_" + strings.Repeat("x", 23),
		"pk_live_xxxxxxxxxxxxxxxxxxxxxxx",
	}

	pkPattern := BuiltInPatterns[4].Pattern
	for _, v := range valid {
		if !pkPattern.MatchString(v) {
			t.Errorf("Stripe publishable key pattern should match: %q", v)
		}
	}
	for _, inv := range invalidPK {
		if pkPattern.MatchString(inv) {
			t.Errorf("Stripe publishable key pattern should NOT match: %q", inv)
		}
	}
}

func TestGenericAPIKeyPattern(t *testing.T) {
	valid := []string{
		"api_key=abcdefghijklmnopqrstuvwx",
		"API_KEY=abcdefghijklmnopqrstuvwx",
		"Api-Key-abcdefghijklmnopqrstuvwx",
		"api-key=abcdefghijklmnop",
		"apiKey=abcdefghijklmnopqrstuvwx123456",
		"API-KEY=abcdefghijklmnopqrstuvwx",
		"api_key_12345678901234567890",
		"APIKEY=abcdefghijklmnopqrstuvwx", // 16 chars after KEY
	}
	invalid := []string{
		"api_key=short",                    // only 5 chars
		"api_key=abc",                      // only 3 chars
		"APIKEY=abcdefgh",                  // only 8 chars
		"APIKEYabcdefgh",                   // only 8 chars
		"apikey",
		"key=abcdefghijklmnop",
		"secret_token",
	}

	apiPattern := BuiltInPatterns[5].Pattern
	for _, v := range valid {
		if !apiPattern.MatchString(v) {
			t.Errorf("Generic API key pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if apiPattern.MatchString(inv) {
			t.Errorf("Generic API key pattern should NOT match: %q", inv)
		}
	}
}

func TestBearerTokenPattern(t *testing.T) {
	valid := []string{
		"Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		"bearer abc.def.ghi",
		"Bearer a-b.c_d.e_f",
	}
	invalid := []string{
		"Bearer invalid",
		"bearer abc.def",
		"Bearer abc",
		"Bearer eyJhbGciOiJIUzI1NiJ9",
		"Bearer123",
		"bearer1.2.3",
	}

	bearerPattern := BuiltInPatterns[6].Pattern
	for _, v := range valid {
		if !bearerPattern.MatchString(v) {
			t.Errorf("Bearer token pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if bearerPattern.MatchString(inv) {
			t.Errorf("Bearer token pattern should NOT match: %q", inv)
		}
	}
}

func TestEnvVarPattern(t *testing.T) {
	valid := []string{
		"$API_KEY=secret123",
		"$AWS_SECRET_ACCESS_KEY=secret",
		"$MY_VAR=value",
		"$VAR1=another",
	}
	invalid := []string{
		"api_key=secret",
		"API_KEY=secret",
		"$api_key=secret",
		"$123VAR=value",
	}

	envPattern := BuiltInPatterns[7].Pattern
	for _, v := range valid {
		if !envPattern.MatchString(v) {
			t.Errorf("Env var pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if envPattern.MatchString(inv) {
			t.Errorf("Env var pattern should NOT match: %q", inv)
		}
	}
}

func TestNoFalsePositives(t *testing.T) {
	benign := []string{
		"This is not an API key",
		"ghx_token",
		"sk_test_xxx",
		"Bearer",
		"$api_key=value",
		"random_text_here",
		"password123",
		"my_secret_key",
		"token_abc123",
	}

	for _, b := range benign {
		for i, p := range BuiltInPatterns {
			if p.Pattern.MatchString(b) {
				t.Errorf("Pattern %d (%s) should NOT match benign input: %q", i, p.Name, b)
			}
		}
	}
}

func TestPatternStructFields(t *testing.T) {
	for i, p := range BuiltInPatterns {
		if p.Name == "" {
			t.Errorf("Pattern at index %d has empty name", i)
		}
		if p.Pattern == nil {
			t.Errorf("Pattern %s has nil Pattern", p.Name)
		}
		if p.Example == "" {
			t.Errorf("Pattern %s has empty Example", p.Name)
		}
		if p.Description == "" {
			t.Errorf("Pattern %s has empty Description", p.Name)
		}
	}
}

func TestGetPatternNames(t *testing.T) {
	names := GetPatternNames()
	if len(names) != len(BuiltInPatterns) {
		t.Fatalf("expected %d pattern names, got %d", len(BuiltInPatterns), len(names))
	}
	for _, name := range names {
		if name == "" {
			t.Error("pattern name should not be empty")
		}
	}
}

func TestGetPatternByName(t *testing.T) {
	pattern := GetPatternByName("aws-access-key")
	if pattern == nil {
		t.Fatal("expected to find aws-access-key pattern")
	}
	if pattern.Name != "aws-access-key" {
		t.Errorf("expected name aws-access-key, got %s", pattern.Name)
	}

	notFound := GetPatternByName("nonexistent")
	if notFound != nil {
		t.Error("expected nil for nonexistent pattern")
	}
}

func TestCompileCustomPatterns(t *testing.T) {
	configs := []PatternConfig{
		{Name: "custom1", Pattern: `test_\w+`},
		{Name: "custom2", Pattern: `secret_\d+`},
	}

	patterns, err := CompileCustomPatterns(configs)
	if err != nil {
		t.Fatalf("CompileCustomPatterns failed: %v", err)
	}
	if len(patterns) != 2 {
		t.Errorf("expected 2 patterns, got %d", len(patterns))
	}

	invalidConfigs := []PatternConfig{
		{Name: "invalid", Pattern: "[invalid"},
	}
	_, err = CompileCustomPatterns(invalidConfigs)
	if err == nil {
		t.Error("expected error for invalid pattern")
	}
}

func TestPatternConfigValidate(t *testing.T) {
	valid := PatternConfig{Name: "test", Pattern: `\w+`}
	if err := valid.Validate(); err != nil {
		t.Errorf("expected valid config, got: %v", err)
	}

	emptyName := PatternConfig{Name: "", Pattern: `\w+`}
	if err := emptyName.Validate(); err == nil {
		t.Error("expected error for empty name")
	}

	emptyPattern := PatternConfig{Name: "test", Pattern: ""}
	if err := emptyPattern.Validate(); err == nil {
		t.Error("expected error for empty pattern")
	}

	invalidRegex := PatternConfig{Name: "test", Pattern: "[invalid"}
	if err := invalidRegex.Validate(); err == nil {
		t.Error("expected error for invalid regex")
	}
}

func TestPatternsToRegexps(t *testing.T) {
	regexps := PatternsToRegexps(BuiltInPatterns)
	if len(regexps) != len(BuiltInPatterns) {
		t.Errorf("expected %d regexps, got %d", len(BuiltInPatterns), len(regexps))
	}

	for i, re := range regexps {
		if re == nil {
			t.Errorf("regexp at index %d is nil", i)
		}
	}
}

func TestMatchSecret(t *testing.T) {
	matches := MatchSecret("AKIAIOSFODNN7EXAMPLE")
	if len(matches) == 0 {
		t.Error("expected to match AWS key")
	}
	if matches[0] != "aws-access-key" {
		t.Errorf("expected aws-access-key match, got %s", matches[0])
	}

	noMatches := MatchSecret("random text")
	if len(noMatches) > 0 {
		t.Error("expected no matches for benign text")
	}
}

func TestRedactSecrets(t *testing.T) {
	input := "AKIAIOSFODNN7EXAMPLE is my key"
	result := RedactSecrets(input)
	if result == input {
		t.Error("expected secret to be redacted")
	}
	if result != "[SECRET_REDACTED] is my key" {
		t.Errorf("unexpected redaction result: %q", result)
	}
}

func TestRedactWithPatterns(t *testing.T) {
	input := "AKIAIOSFODNN7EXAMPLE is my key"
	patterns := []*regexp.Regexp{regexp.MustCompile(`AKIA[0-9A-Z]{16}`)}
	result := RedactWithPatterns(input, patterns)
	if result == input {
		t.Error("expected secret to be redacted")
	}
}

func TestFormatPatternList(t *testing.T) {
	list := FormatPatternList()
	if list == "" {
		t.Error("expected non-empty pattern list")
	}
	if !strings.Contains(list, "aws-access-key") {
		t.Error("expected aws-access-key in pattern list")
	}
}

func TestLoadPatternsWithLogging(t *testing.T) {
	customConfigs := []PatternConfig{
		{Name: "custom1", Pattern: `custom_\w+`},
	}
	patterns, skipped := LoadPatternsWithLogging(customConfigs)
	if len(patterns) == 0 {
		t.Error("expected patterns to be loaded")
	}
	if len(skipped) > 0 {
		t.Errorf("expected no skipped patterns, got %d", len(skipped))
	}
}

func TestLoadPatternsWithLoggingInvalid(t *testing.T) {
	customConfigs := []PatternConfig{
		{Name: "invalid", Pattern: "[invalid"},
	}
	_, skipped := LoadPatternsWithLogging(customConfigs)
	if len(skipped) != 1 {
		t.Errorf("expected 1 skipped pattern, got %d", len(skipped))
	}
}

func TestAllowlistIntegration(t *testing.T) {
	for _, p := range BuiltInPatterns {
		if p.Pattern == nil {
			t.Fatalf("Pattern %s has nil regexp", p.Name)
		}
	}
}