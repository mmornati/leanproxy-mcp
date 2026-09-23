package bouncer

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Severity ranks how damaging a leaked secret of a given kind is. It is
// informational (shown by `bouncer list-patterns` and in the docs); every
// match is redacted regardless of severity.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
)

// SecretPattern is one built-in redaction rule.
//
// Keywords is a prefilter: every match of Pattern must contain at least one
// of the keywords (compared ASCII case-insensitively when KeywordsFold is
// set). The redactor only runs Pattern over text that contains a keyword,
// which keeps the per-byte cost of dozens of patterns close to the cost of a
// single pass. A pattern with no keywords is run over every string.
//
// SecretGroup, when non-zero, names the capture group that holds the secret
// itself: only that group is replaced, so the context that made the match
// recognizable (a URL scheme and host, an `Authorization: Basic` prefix, a
// variable name) stays readable.
type SecretPattern struct {
	Name         string
	Severity     Severity
	Pattern      *regexp.Regexp
	Keywords     []string
	KeywordsFold bool
	SecretGroup  int
	Example      string
	Description  string
}

var BuiltInPatterns = []SecretPattern{
	{
		Name:        "aws-access-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		Keywords:    []string{"AKIA"},
		Example:     "AKIAIOSFODNN7EXAMPLE",
		Description: "AWS Access Key ID (20 characters, starts with AKIA)",
	},
	{
		Name:        "aws-temporary-access-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`ASIA[0-9A-Z]{16}`),
		Keywords:    []string{"ASIA"},
		Example:     "ASIA followed by 16 uppercase letters or digits",
		Description: "AWS temporary (STS) Access Key ID (20 characters, starts with ASIA)",
	},
	{
		Name:         "aws-secret-access-key",
		Severity:     SeverityCritical,
		Pattern:      regexp.MustCompile(`(?i)aws_?secret_?access_?key["']?\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})(?:[^A-Za-z0-9/+=]|$)`),
		Keywords:     []string{"aws"},
		KeywordsFold: true,
		SecretGroup:  1,
		Example:      "aws_secret_access_key = <40 base64 characters>",
		Description:  "AWS Secret Access Key (40 base64 characters after aws_secret_access_key; only the key is replaced)",
	},
	{
		Name:        "github-classic-pat",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`ghp_[A-Za-z0-9]{36,}`),
		Keywords:    []string{"ghp_"},
		Example:     "ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		Description: "GitHub Classic Personal Access Token (starts with ghp_, 36+ chars after prefix)",
	},
	{
		Name:        "github-app-token",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`gh[ousr]_[A-Za-z0-9]{36,}`),
		Keywords:    []string{"gho_", "ghu_", "ghs_", "ghr_"},
		Example:     "gho_ / ghu_ / ghs_ / ghr_ followed by 36+ alphanumerics",
		Description: "GitHub OAuth, user-to-server, server-to-server and refresh tokens (gho_, ghu_, ghs_, ghr_)",
	},
	{
		Name:        "github-fine-grained-pat",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`),
		Keywords:    []string{"github_pat_"},
		Example:     "github_pat_11XXXXXXXXXXXXXXXX_XXXXXXXXXXXXXXXXXXXX",
		Description: "GitHub Fine-grained PAT (starts with github_pat_)",
	},
	{
		Name:        "gitlab-pat",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
		Keywords:    []string{"glpat-"},
		Example:     "glpat-XXXXXXXXXXXXXXXXXXXX",
		Description: "GitLab Personal Access Token (starts with glpat-, 20+ chars after prefix)",
	},
	{
		Name:        "stripe-secret-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`sk_(?:live|test)_[A-Za-z0-9]{24,}`),
		Keywords:    []string{"sk_live_", "sk_test_"},
		Example:     "sk_live_ / sk_test_ followed by 24+ alphanumerics",
		Description: "Stripe secret key, live or test mode (sk_live_, sk_test_)",
	},
	{
		Name:        "stripe-restricted-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`rk_(?:live|test)_[A-Za-z0-9]{24,}`),
		Keywords:    []string{"rk_live_", "rk_test_"},
		Example:     "rk_live_ / rk_test_ followed by 24+ alphanumerics",
		Description: "Stripe restricted key, live or test mode (rk_live_, rk_test_)",
	},
	{
		Name:        "stripe-publishable-key",
		Severity:    SeverityLow,
		Pattern:     regexp.MustCompile(`pk_live_[A-Za-z0-9]{24}`),
		Keywords:    []string{"pk_live_"},
		Example:     "[Stripe Publishable Key - 24 chars after pk_live_]",
		Description: "Stripe Live Publishable Key (starts with pk_live_)",
	},
	{
		Name:        "pem-private-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----[\s\S]+?-----END (?:RSA |EC |DSA |OPENSSH |ENCRYPTED )?PRIVATE KEY-----`),
		Keywords:    []string{"PRIVATE KEY-----"},
		Example:     "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQ...\n-----END RSA PRIVATE KEY-----",
		Description: "Multi-line PEM-encoded private keys (RSA, EC, DSA, OpenSSH, PKCS8, encrypted)",
	},
	{
		Name:        "pgp-private-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`-----BEGIN PGP PRIVATE KEY BLOCK-----[\s\S]+?-----END PGP PRIVATE KEY BLOCK-----`),
		Keywords:    []string{"-----BEGIN PGP PRIVATE KEY BLOCK-----"},
		Example:     "-----BEGIN PGP PRIVATE KEY BLOCK-----\n...\n-----END PGP PRIVATE KEY BLOCK-----",
		Description: "ASCII-armored PGP/GPG private key blocks",
	},
	{
		Name:        "pem-certificate",
		Severity:    SeverityLow,
		Pattern:     regexp.MustCompile(`-----BEGIN CERTIFICATE-----[\s\S]+?-----END CERTIFICATE-----`),
		Keywords:    []string{"-----BEGIN CERTIFICATE-----"},
		Example:     "-----BEGIN CERTIFICATE-----\nMIIDdzCCAl+gAwIBAgI...\n-----END CERTIFICATE-----",
		Description: "Multi-line PEM-encoded X.509 certificates",
	},
	{
		Name:        "gcp-service-account",
		Severity:    SeverityLow,
		Pattern:     regexp.MustCompile(`"type"\s*:\s*"service_account"`),
		Keywords:    []string{"service_account"},
		Example:     `{"type": "service_account", "project_id": "...", "private_key": "..."}`,
		Description: "GCP service account JSON key marker (\"type\": \"service_account\") in free text; in JSON the private_key field is redacted by key name",
	},
	{
		Name:        "gcp-oauth-token",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`ya29\.[A-Za-z0-9_-]{20,}`),
		Keywords:    []string{"ya29."},
		Example:     "ya29.XXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		Description: "GCP OAuth2 access / refresh token (starts with ya29., 20+ alphanumeric/_/- chars after)",
	},
	{
		Name:        "google-api-key",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
		Keywords:    []string{"AIza"},
		Example:     "AIza followed by 35 characters from [0-9A-Za-z_-]",
		Description: "Google API key (Maps, Firebase, Gemini, ...; 39 characters starting with AIza)",
	},
	{
		Name:        "slack-token",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`xox[bpars]-[A-Za-z0-9-]{20,}`),
		Keywords:    []string{"xox"},
		Example:     "xoxb-XXXXXXXXXXXXXXXX-XXXXXXXXXXXXXXXX-XXXXXXXXXXXXXXXXXXXXXXXX",
		Description: "Slack bot/user/app/refresh token (xoxb-, xoxp-, xoxa-, xoxr-, xoxs-, 20+ chars after prefix)",
	},
	{
		Name:        "slack-webhook",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`https://hooks\.slack\.com/services/T[A-Z0-9]{6,}/B[A-Z0-9]{6,}/[A-Za-z0-9]{16,}`),
		Keywords:    []string{"hooks.slack.com"},
		Example:     "https://hooks.slack.com/services/T.../B.../...",
		Description: "Slack incoming-webhook URL",
	},
	{
		Name:        "openai-api-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`sk-[A-Za-z0-9]{40,}`),
		Keywords:    []string{"sk-"},
		Example:     "sk-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		Description: "OpenAI API key, legacy format (sk- followed by 40+ alphanumeric chars)",
	},
	{
		Name:        "openai-project-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`sk-(?:proj|svcacct|admin)-[A-Za-z0-9_-]{40,}`),
		Keywords:    []string{"sk-proj-", "sk-svcacct-", "sk-admin-"},
		Example:     "sk-proj- followed by 40+ characters from [A-Za-z0-9_-]",
		Description: "OpenAI project, service-account and admin keys (sk-proj-, sk-svcacct-, sk-admin-)",
	},
	{
		Name:        "anthropic-api-key",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{32,}`),
		Keywords:    []string{"sk-ant-"},
		Example:     "sk-ant-api03-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
		Description: "Anthropic API key (starts with sk-ant-, 32+ chars after prefix)",
	},
	{
		Name:        "npm-token",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`npm_[A-Za-z0-9]{36}`),
		Keywords:    []string{"npm_"},
		Example:     "npm_ followed by 36 alphanumerics",
		Description: "npm access token (starts with npm_)",
	},
	{
		Name:         "generic-api-key",
		Severity:     SeverityMedium,
		Pattern:      regexp.MustCompile(`(?i)(api[_-]?key)[_-]?[=]?[A-Za-z0-9]{16,}`),
		Keywords:     []string{"api"},
		KeywordsFold: true,
		Example:      "api_key=abcdefghijklmnopqrstuvwx",
		Description:  "Generic API key pattern (case-insensitive)",
	},
	{
		Name:         "bearer-token",
		Severity:     SeverityHigh,
		Pattern:      regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+\.[A-Za-z0-9\-_]+`),
		Keywords:     []string{"bearer"},
		KeywordsFold: true,
		Example:      "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		Description:  "JWT Bearer token (three base64url segments)",
	},
	{
		Name:        "jwt",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`eyJ[A-Za-z0-9_-]{5,}\.eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]*`),
		Keywords:    []string{"eyJ"},
		Example:     "eyJ<header>.eyJ<payload>.<signature>",
		Description: "JSON Web Token without a Bearer prefix (base64url header and payload both start with eyJ)",
	},
	{
		Name:         "basic-auth-header",
		Severity:     SeverityHigh,
		Pattern:      regexp.MustCompile(`(?i)authorization["']?\s*[:=]\s*["']?basic\s+([A-Za-z0-9+/]{8,}={0,2})`),
		Keywords:     []string{"authorization"},
		KeywordsFold: true,
		SecretGroup:  1,
		Example:      "Authorization: Basic dXNlcjpwYXNzd29yZA==",
		Description:  "HTTP Basic credentials after an Authorization header (only the base64 credentials are replaced)",
	},
	{
		Name:        "dsn-credentials",
		Severity:    SeverityCritical,
		Pattern:     regexp.MustCompile(`://[^:@/\s"'<>]*:([^@/\s"'<>]+)@`),
		Keywords:    []string{"://"},
		SecretGroup: 1,
		Example:     "postgres://user:password@db:5432/app",
		Description: "Password in a connection string or URL (postgres://, mysql://, mongodb+srv://, redis://, amqp://, https://user:pass@...; only the password is replaced)",
	},
	{
		Name:        "env-var-value",
		Severity:    SeverityMedium,
		Pattern:     regexp.MustCompile(`\$[A-Z_][A-Z0-9_]{0,30}=([^\s,}]+)`),
		Keywords:    []string{"$"},
		Example:     "$API_KEY=secret123",
		Description: "Environment variable assignment",
	},
	{
		Name:        "env-file-secret",
		Severity:    SeverityHigh,
		Pattern:     regexp.MustCompile(`(?m)^[ \t]*(?:export[ \t]+)?[A-Z0-9_]*(?:PASSWORD|PASSWD|SECRET|TOKEN|API_?KEY|PRIVATE_KEY|ACCESS_KEY)[ \t]*=[ \t]*["']?([^\s"'#]+)`),
		Keywords:    []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "API", "PRIVATE_KEY", "ACCESS_KEY"},
		SecretGroup: 1,
		Example:     "DB_PASSWORD=hunter2 (a line of a .env file)",
		Description: "Value of a .env / shell assignment whose UPPER_CASE name ends in PASSWORD, SECRET, TOKEN, API_KEY, PRIVATE_KEY or ACCESS_KEY (only the value is replaced)",
	},
}

// sensitiveKeyExact lists normalized JSON key names (lowercase, with '-',
// '_' and spaces removed) whose value is always a credential, whatever its
// type or content.
var sensitiveKeyExact = map[string]struct{}{
	"password": {}, "passwd": {}, "pwd": {}, "passphrase": {},
	"secret": {}, "token": {}, "apikey": {}, "apisecret": {}, "appsecret": {},
	"accesstoken": {}, "refreshtoken": {}, "idtoken": {}, "authtoken": {},
	"bearertoken": {}, "sessiontoken": {}, "csrftoken": {}, "xsrftoken": {},
	"clientsecret": {}, "privatekey": {}, "secretkey": {}, "secretaccesskey": {},
	"authorization": {}, "proxyauthorization": {}, "cookie": {}, "setcookie": {},
	"sessionid": {}, "awssecretaccesskey": {}, "awssessiontoken": {},
	"xapikey": {}, "xauthtoken": {}, "apitoken": {},
}

// sensitiveKeySuffixes lists normalized suffixes that also mark a key as
// sensitive ("db_password", "stripeApiKey", "github_access_token"). "token"
// on its own is deliberately not a suffix: "next_page_token" and
// "max_tokens" are not credentials.
var sensitiveKeySuffixes = []string{
	"password", "passwd", "passphrase", "secret", "apikey", "secretkey",
	"privatekey", "accesstoken", "refreshtoken", "authtoken", "sessiontoken",
	"apitoken", "clientsecret",
}

// SensitiveJSONFieldNames lists the canonical sensitive key names, for
// documentation and for callers that want to display them. Matching is
// done on normalized keys by IsSensitiveKey.
var SensitiveJSONFieldNames = func() []string {
	names := make([]string, 0, len(sensitiveKeyExact))
	for k := range sensitiveKeyExact {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}()

// normalizeKey lowercases ASCII letters and drops '-', '_' and spaces,
// appending the result to dst.
func normalizeKey(dst []byte, key []byte) []byte {
	for _, c := range key {
		switch {
		case c == '-' || c == '_' || c == ' ':
			continue
		case c >= 'A' && c <= 'Z':
			dst = append(dst, c+('a'-'A'))
		default:
			dst = append(dst, c)
		}
	}
	return dst
}

// isSensitiveNormalizedKey reports whether an already-normalized key names
// a credential.
func isSensitiveNormalizedKey(norm []byte) bool {
	if len(norm) < 3 {
		return false
	}
	if _, ok := sensitiveKeyExact[string(norm)]; ok {
		return true
	}
	for _, suf := range sensitiveKeySuffixes {
		if len(norm) > len(suf) && string(norm[len(norm)-len(suf):]) == suf {
			return true
		}
	}
	return false
}

// IsSensitiveKey reports whether a JSON object key names a credential. The
// comparison is case-insensitive and ignores '-', '_' and spaces, so
// "API-Key", "api_key" and "apiKey" are the same key.
func IsSensitiveKey(key string) bool {
	var buf [64]byte
	return isSensitiveNormalizedKey(normalizeKey(buf[:0], []byte(key)))
}

func sensitiveJSONFieldLookup(key string) bool {
	return IsSensitiveKey(key)
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
	patterns := make([]*regexp.Regexp, 0, len(configs))
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

func CompileCustomPatternsWithTimeout(configs []PatternConfig, timeout time.Duration) ([]*regexp.Regexp, error) {
	patterns := make([]*regexp.Regexp, 0, len(configs))
	for _, c := range configs {
		if c.Name == "" || c.Pattern == "" {
			continue
		}
		if err := ValidateReDoS(c.Pattern, timeout); err != nil {
			slog.Warn("pattern skipped due to ReDoS risk", "name", c.Name, "error", err)
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

func ValidateReDoS(pattern string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = 100 * time.Millisecond
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return err
	}

	toxic := make([]byte, 1000)
	for i := range toxic {
		toxic[i] = '!'
	}

	done := make(chan bool, 1)
	go func() {
		re.Match(toxic)
		done <- true
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("pattern appears vulnerable to ReDoS (timeout)")
	}
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
	if len(pc.Pattern) > 500 {
		return fmt.Errorf("allowlist: pattern config %q exceeds maximum length of 500", pc.Name)
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

// RedactSecrets replaces every built-in secret pattern match in free-form
// text (stderr lines, error strings). It uses the same engine and the same
// single-scan-per-pattern span logic as RedactJSON.
func RedactSecrets(input string) string {
	return DefaultRedactor().RedactText(input)
}

// RedactWithPatterns replaces every match of patterns in input. Prefer
// Redactor.RedactText in hot paths: this helper compiles a redactor for the
// pattern list on every call.
func RedactWithPatterns(input string, patterns []*regexp.Regexp) string {
	return NewRedactor(patterns).RedactText(input)
}

func FormatPatternList() string {
	lines := make([]string, 0, len(BuiltInPatterns))
	for _, p := range BuiltInPatterns {
		lines = append(lines, fmt.Sprintf("- %s [%s]: %s (%s)", p.Name, p.Severity, p.Description, p.Example))
	}
	return strings.Join(lines, "\n")
}
