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
	ghPattern := GetPatternByName("github-classic-pat").Pattern

	valid := []string{
		"ghp_" + strings.Repeat("X", 36),
		"ghp_" + strings.Repeat("X", 40),
	}
	invalid := []string{
		"ghx_" + strings.Repeat("X", 36),       // wrong prefix
		"GHP_" + strings.Repeat("X", 36),       // uppercase prefix
		"ghp " + strings.Repeat("X", 36),       // space in prefix
		"ghp_" + strings.Repeat("X", 35),       // too short
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
		"github_pat_11XXXXXXXXXXXXXXXX_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		"github_pat_11XXXXXXXXXXXXXXXX_XXXXXXXXXXXXXXXXXXXX",
		"github_pat_12XXXXXXXXXXXXX_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
	}
	invalid := []string{
		"github_pat_1XXXXXXXXXX", // too short
		"github_pat_11",                // too short
		"Github_pat_11XXXXXXXXXXXXXXXX_XXXXX",
		"github_pat_11XXXXXXXXXXXXXXXX_", // underscore at end ok, but pattern requires more
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
		"api_key=short",   // only 5 chars
		"api_key=abc",     // only 3 chars
		"APIKEY=abcdefgh", // only 8 chars
		"APIKEYabcdefgh",  // only 8 chars
		"apikey",
		"key=abcdefghijklmnop",
		"secret_token",
	}

	apiPattern := GetPatternByName("generic-api-key").Pattern
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

	bearerPattern := GetPatternByName("bearer-token").Pattern
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

	envPattern := GetPatternByName("env-var-value").Pattern
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
		// Sentinels for new patterns added in #279. Each must NOT match any
		// built-in; they exercise the boundaries (length prefix, casing,
		// charset) the new patterns key on.
		"-----BEGIN CUSTOM-----\nxxx\n-----END CUSTOM-----",
		"ya29.shortvalue",
		"xoxc-111-222-" + strings.Repeat("a", 30), // xoxc not in [abprs]
		"glpat-" + strings.Repeat("a", 10),
		"sk-" + strings.Repeat("a", 39),  // OpenAI too short
		"sk-ant-" + strings.Repeat("a", 30), // Anthropic too short
		`{"api_key": ""}`,
		`{"foo": "bar"}`,
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

func TestPEMPrivateKeyPattern(t *testing.T) {
	pem := GetPatternByName("pem-private-key").Pattern
	valid := []string{
		"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQ\n-----END PRIVATE KEY-----",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEAxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n-----END RSA PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----\nMHcCAQEExxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n-----END EC PRIVATE KEY-----",
		"-----BEGIN DSA PRIVATE KEY-----\nMIIBuwIBAAKCAYEA\n-----END DSA PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\n-----END OPENSSH PRIVATE KEY-----",
		"-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIBHDBOBgkqhkiG9w0BBQ0wQTApBgNVHSQE\n-----END ENCRYPTED PRIVATE KEY-----",
	}
	invalid := []string{
		"-----BEGIN PUBLIC KEY-----\nxxx\n-----END PUBLIC KEY-----",
		"-----BEGIN CERTIFICATE-----\nMII\n-----END CERTIFICATE-----",
		"random text with no PEM",
		"-----BEGIN RSA PRIVATE KEY-----\nno end marker",
	}

	for _, v := range valid {
		if !pem.MatchString(v) {
			t.Errorf("PEM private key pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if pem.MatchString(inv) {
			t.Errorf("PEM private key pattern should NOT match: %q", inv)
		}
	}
}

func TestPEMCertificatePattern(t *testing.T) {
	cert := GetPatternByName("pem-certificate").Pattern
	valid := []string{
		"-----BEGIN CERTIFICATE-----\nMIIDdzCCAl+gAwIBAgIJ\n-----END CERTIFICATE-----",
		"prefix stuff\n-----BEGIN CERTIFICATE-----\nMIIDXTCCAkWgAwIBAgIJA\n-----END CERTIFICATE-----\nsuffix",
	}
	invalid := []string{
		"-----BEGIN PRIVATE KEY-----\nxxx\n-----END PRIVATE KEY-----",
		"just text",
		"-----BEGIN CERTIFICATE-----\nno end",
	}

	for _, v := range valid {
		if !cert.MatchString(v) {
			t.Errorf("PEM cert pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if cert.MatchString(inv) {
			t.Errorf("PEM cert pattern should NOT match: %q", inv)
		}
	}
}

func TestGCPServiceAccountMarkerPattern(t *testing.T) {
	p := GetPatternByName("gcp-service-account").Pattern
	valid := []string{
		`{"type": "service_account", "project_id": "my-proj"}`,
		`{"type":"service_account"}`,
		`prefix {"type": "service_account"} suffix`,
	}
	invalid := []string{
		`{"type": "authorized_user"}`,
		`{"type":"user"}`,
		`{type: "service_account"}`,
		`{"type": "service-account"}`,
	}

	for _, v := range valid {
		if !p.MatchString(v) {
			t.Errorf("GCP service-account marker should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if p.MatchString(inv) {
			t.Errorf("GCP service-account marker should NOT match: %q", inv)
		}
	}
}

func TestGCPOAuthTokenPattern(t *testing.T) {
	p := GetPatternByName("gcp-oauth-token").Pattern
	valid := []string{
		"ya29.XXXXXXXXXXXXXXXXXXXX" + strings.Repeat("a", 20),
		"ya29." + strings.Repeat("X", 40),
		"ya29." + strings.Repeat("0", 60),
		"ya29." + strings.Repeat("-", 25), // GCP tokens may contain hyphens
		"ya29." + strings.Repeat("a", 500), // any longer token fully redacted
	}
	invalid := []string{
		"ya29.short",
		"ya29." + strings.Repeat("a", 19), // one short
		"ya29." + strings.Repeat("!", 30), // ! not in charset
		"ya2.abcdefghijklmnopqrstuv",
		"prefix ya29.",
	}

	for _, v := range valid {
		if !p.MatchString(v) {
			t.Errorf("GCP OAuth token pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if p.MatchString(inv) {
			t.Errorf("GCP OAuth token pattern should NOT match: %q", inv)
		}
	}
}

func TestSlackTokenPattern(t *testing.T) {
	p := GetPatternByName("slack-token").Pattern
	valid := []string{
		"xoxb-XXXXXXXXXXXXXXXX-XXXXXXXXXXXXXXXX-" + strings.Repeat("X", 24),
		"xoxp-XXXX-XXXX-" + strings.Repeat("X", 20),
		"xoxa-X-X-" + strings.Repeat("X", 30),
		"xoxr-X-X-" + strings.Repeat("X", 22),
		"xoxs-X-X-" + strings.Repeat("X", 30),
	}
	invalid := []string{
		"xoxb-short",
		"xoxc-XXXXXXXXXXXXXXXX-XXXXXXXXXXXXXXXX-" + strings.Repeat("X", 24), // xoxc not in [abprs]
		"xox-" + strings.Repeat("a", 30),
		"xoxb_XXXXXXXXXXXXXXXX_XXXXXXXXXXXXXXXX_" + strings.Repeat("X", 24), // _ not allowed mid-token
	}

	for _, v := range valid {
		if !p.MatchString(v) {
			t.Errorf("Slack token pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if p.MatchString(inv) {
			t.Errorf("Slack token pattern should NOT match: %q", inv)
		}
	}
}

func TestGitLabPATPattern(t *testing.T) {
	p := GetPatternByName("gitlab-pat").Pattern
	valid := []string{
		"glpat-" + strings.Repeat("X", 20),
		"glpat-ABCDEFGHIJKLMNOPQRST",
		"glpat-" + strings.Repeat("0", 50),
	}
	invalid := []string{
		"glpat-short",
		"glpat-" + strings.Repeat("X", 19), // one short
		"glpat-" + strings.Repeat("!", 25), // ! not in charset
		"GLPAT-abc",
	}

	for _, v := range valid {
		if !p.MatchString(v) {
			t.Errorf("GitLab PAT pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if p.MatchString(inv) {
			t.Errorf("GitLab PAT pattern should NOT match: %q", inv)
		}
	}
}

func TestOpenAIAPIKeyPattern(t *testing.T) {
	p := GetPatternByName("openai-api-key").Pattern
	valid := []string{
		"sk-" + strings.Repeat("a", 40),
		"sk-" + strings.Repeat("a", 48),
		"sk-" + strings.Repeat("Z", 100),
		"sk-AbCdEf1234567890" + strings.Repeat("X", 32),
		"sk-" + strings.Repeat("a", 500), // any longer key fully redacted
	}
	invalid := []string{
		"sk-" + strings.Repeat("a", 39),
		"sk_live_" + strings.Repeat("a", 24),
		"sk_" + strings.Repeat("a", 48), // underscore, not -
		// sk-ant- tokens also match the OpenAI fallback; that's fine - both
		// patterns redact the same span and the Anthropic pattern provides a
		// more accurate name in MatchSecret.
	}

	for _, v := range valid {
		if !p.MatchString(v) {
			t.Errorf("OpenAI API key pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if p.MatchString(inv) {
			t.Errorf("OpenAI API key pattern should NOT match: %q", inv)
		}
	}
}

func TestAnthropicAPIKeyPattern(t *testing.T) {
	p := GetPatternByName("anthropic-api-key").Pattern
	valid := []string{
		"sk-ant-api03-" + strings.Repeat("a", 32),
		"sk-ant-" + strings.Repeat("a", 40),
		"sk-ant-AbCdEf123-_X" + strings.Repeat("a", 25),
		"sk-ant-" + strings.Repeat("k", 200),
	}
	invalid := []string{
		"sk-ant-" + strings.Repeat("a", 31), // one short
		"sk-anti-" + strings.Repeat("a", 40), // "anti-" not "ant-"
		"skANTI-" + strings.Repeat("a", 40), // case sensitive
		"random_text_sk-ant-",
	}

	for _, v := range valid {
		if !p.MatchString(v) {
			t.Errorf("Anthropic API key pattern should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if p.MatchString(inv) {
			t.Errorf("Anthropic API key pattern should NOT match: %q", inv)
		}
	}
}

func TestWiderGitHubClassicPAT(t *testing.T) {
	gh := GetPatternByName("github-classic-pat").Pattern

	valid := []string{
		"ghp_" + strings.Repeat("a", 36),
		"ghp_" + strings.Repeat("a", 100),
		"ghp_" + strings.Repeat("a", 255),
		"ghp_" + strings.Repeat("a", 500), // any longer PAT, fully redacted
	}
	invalid := []string{
		"ghp_" + strings.Repeat("a", 35),
		"ghp_" + strings.Repeat("!", 36),
		"gho_" + strings.Repeat("a", 36), // different prefix
	}

	for _, v := range valid {
		if !gh.MatchString(v) {
			t.Errorf("Wider GitHub classic PAT should match: %q", v)
		}
	}
	for _, inv := range invalid {
		if gh.MatchString(inv) {
			t.Errorf("Wider GitHub classic PAT should NOT match: %q", inv)
		}
	}
}

func TestSensitiveJSONFieldNamesLookup(t *testing.T) {
	cases := map[string]bool{
		"api_key":       true,
		"apikey":        true,
		"api-key":       true,
		"token":         true,
		"password":      true,
		"private_key":   true,
		"client_secret": true,
		"my_api_key":    false, // contains "api_key" but is not exactly it
		"authorization": false,
		"data":          false,
		"":             false,
	}
	for k, want := range cases {
		if got := sensitiveJSONFieldLookup(k); got != want {
			t.Errorf("sensitiveJSONFieldLookup(%q) = %v, want %v", k, got, want)
		}
	}
}

func TestRedactJSON_SensitiveFieldNames(t *testing.T) {
	// The field-name pass redacts the value of any key in
	// SensitiveJSONFieldNames regardless of whether the value matches a
	// built-in regex. This is the JSON-aware counterpart of the regex-
	// driven streaming path; it is sound because RedactJSON walks the
	// parsed structure and visits each value in isolation.
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	cases := []struct {
		name    string
		input   string
		wantKey string
	}{
		{
			name:    "unknown token behind api_key",
			input:   `{"api_key": "internalplatformtoken-noregexmatch", "other": "v"}`,
			wantKey: `"api_key":"[SECRET_REDACTED]"`,
		},
		{
			name:    "private_key with embedded PEM",
			input:   `{"private_key": "-----BEGIN PRIVATE KEY-----\nABC\n-----END PRIVATE KEY-----"}`,
			wantKey: `"private_key":"[SECRET_REDACTED]"`,
		},
		{
			name:    "nested at depth 2",
			input:   `{"outer": {"apiKey": "totally-bogus-value"}}`,
			wantKey: `"apiKey":"[SECRET_REDACTED]"`,
		},
		{
			name:    "non-sensitive key untouched",
			input:   `{"description": "user typed hunter2 in a doc"}`,
			wantKey: `"description":"user typed hunter2 in a doc"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := r.RedactJSON([]byte(tc.input))
			if err != nil {
				t.Fatalf("RedactJSON: %v", err)
			}
			if !strings.Contains(string(got), tc.wantKey) {
				t.Errorf("output %q does not contain %q", got, tc.wantKey)
			}
		})
	}
}
