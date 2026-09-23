package bouncer

import (
	"regexp"
	"strings"
	"testing"
)

// patternCase is one positive sample for a built-in pattern: text is the
// input, secret the part that must disappear, keep a part that must
// survive (the context a SecretGroup pattern leaves readable).
type patternCase struct {
	text, secret, keep string
}

// builtinPatternCases returns positive and negative samples for every
// built-in pattern. Positive values are generated (see fakesecrets_test.go).
func builtinPatternCases() map[string]struct {
	pos []patternCase
	neg []string
} {
	g := newFakeGen(20260923)
	one := func(v string) []patternCase { return []patternCase{{text: "value " + v + " end", secret: v}} }
	awsSecret := g.awsSecret()
	basic := g.basicCreds()
	dsnPW := g.dsnPassword()
	dsnPW2 := g.dsnPassword()
	envPW := g.password()
	envTok := g.chars(alphaNum, 30)
	pem, pemBody := g.pemBlock("RSA PRIVATE KEY")
	pgp, pgpBody := g.pemBlock("PGP PRIVATE KEY BLOCK")
	cert, certBody := g.pemBlock("CERTIFICATE")
	type cases = struct {
		pos []patternCase
		neg []string
	}
	return map[string]cases{
		"aws-access-key":           {pos: one(g.awsKeyID()), neg: []string{"AKIA123", "akia" + strings.Repeat("A", 16)}},
		"aws-temporary-access-key": {pos: one(g.awsTempKeyID()), neg: []string{"ASIA is a continent", "ASIA-PACIFIC-REGION-01"}},
		"aws-secret-access-key": {pos: []patternCase{
			{text: "aws_secret_access_key = " + awsSecret + "\n", secret: awsSecret, keep: "aws_secret_access_key = "},
			{text: `"AWS_SECRET_ACCESS_KEY": "` + awsSecret + `"`, secret: awsSecret, keep: "AWS_SECRET_ACCESS_KEY"},
		}, neg: []string{"aws_secret_access_key = short", "aws_secret_access_key: " + strings.Repeat("a", 41)}},
		"github-classic-pat":      {pos: one(g.githubPAT()), neg: []string{"ghp_short", "ghp-" + strings.Repeat("a", 36)}},
		"github-app-token":        {pos: one(g.githubOAuth()), neg: []string{"gho_tooshort", "ghx_" + strings.Repeat("a", 36)}},
		"github-fine-grained-pat": {pos: one(g.githubFine()), neg: []string{"github_pat_short"}},
		"gitlab-pat":              {pos: one(g.gitlabPAT()), neg: []string{"glpat-short"}},
		"stripe-secret-key": {pos: append(one(g.stripeLive()), one(g.stripeTest())...),
			neg: []string{"sk_live_short", "sk_prod_" + strings.Repeat("a", 30)}},
		"stripe-restricted-key":  {pos: one(g.stripeRestricted()), neg: []string{"rk_live_short", "rk_" + strings.Repeat("a", 30)}},
		"stripe-publishable-key": {pos: one(join("pk", "_live_", g.chars(alphaNum, 24))), neg: []string{"pk_test_" + strings.Repeat("x", 24)}},
		"pem-private-key": {pos: []patternCase{{text: "key:\n" + pem + "\n", secret: pemBody}},
			neg: []string{"-----BEGIN PUBLIC KEY-----\nabc\n-----END PUBLIC KEY-----", "-----BEGIN RSA PRIVATE KEY----- (truncated)"}},
		"pgp-private-key": {pos: []patternCase{{text: pgp, secret: pgpBody}},
			neg: []string{"-----BEGIN PGP PUBLIC KEY BLOCK-----\nabc\n-----END PGP PUBLIC KEY BLOCK-----"}},
		"pem-certificate":     {pos: []patternCase{{text: cert, secret: certBody}}, neg: []string{"-----BEGIN CERTIFICATE REQUEST-----"}},
		"gcp-service-account": {pos: []patternCase{{text: `{"type": "service_account"}`, secret: `"service_account"`}}, neg: []string{`{"type": "authorized_user"}`}},
		"gcp-oauth-token":     {pos: one(g.gcpOAuth()), neg: []string{"ya29.short"}},
		"google-api-key":      {pos: one(g.googleAPIKey()), neg: []string{"AIza-too-short", "aiza" + strings.Repeat("a", 35)}},
		"slack-token":         {pos: one(g.slackBot()), neg: []string{"xoxb-short", "xoxz-" + strings.Repeat("1", 30)}},
		"slack-webhook":       {pos: one(g.slackWebhook()), neg: []string{"https://hooks.slack.com/services/", "https://hooks.slack.com/docs"}},
		"openai-api-key":      {pos: one(g.openAILegacy()), neg: []string{"sk-short", "task-" + strings.Repeat("ab-", 20)}},
		"openai-project-key":  {pos: one(g.openAIProject()), neg: []string{"sk-proj-short", "desk-proj-notes"}},
		"anthropic-api-key":   {pos: one(g.anthropic()), neg: []string{"sk-ant-short"}},
		"npm-token":           {pos: one(g.npmToken()), neg: []string{"npm_install", "npm_" + strings.Repeat("a", 10)}},
		"generic-api-key": {pos: []patternCase{{text: "api_key=" + strings.Repeat("q", 20), secret: strings.Repeat("q", 20)}},
			neg: []string{"api_key=short", "the api is key"}},
		"bearer-token": {pos: []patternCase{{text: "Authorization: Bearer " + g.jwt(), secret: "Bearer "}},
			neg: []string{"bearer bonds", "Bearer of bad news. Really"}},
		"jwt": {pos: one(g.jwt()), neg: []string{"eyJhbGciOi", "eyJ.eyJ.x"}},
		"basic-auth-header": {pos: []patternCase{
			{text: "Authorization: Basic " + basic, secret: basic, keep: "Authorization: Basic "},
			{text: `"authorization": "basic ` + basic + `"`, secret: basic, keep: `"authorization": "basic `},
		}, neg: []string{"Basic information about the project", "Authorization: Basic"}},
		"dsn-credentials": {pos: []patternCase{
			{text: "postgres://app:" + dsnPW + "@db.internal:5432/app", secret: dsnPW, keep: "postgres://app:"},
			{text: "mongodb+srv://admin:" + dsnPW2 + "@cluster0.example.net/db", secret: dsnPW2, keep: "@cluster0.example.net/db"},
			{text: "redis://:" + dsnPW + "@cache:6379/0", secret: dsnPW, keep: "redis://:"},
			{text: "mysql://root:" + dsnPW2 + "@localhost/app", secret: dsnPW2},
			{text: "amqp://guest:" + dsnPW + "@mq:5672/", secret: dsnPW},
		}, neg: []string{"https://example.com:8080/path", "postgres://db.internal:5432/app", "http://user@host/"}},
		"env-var-value": {pos: []patternCase{{text: "run $API_KEY=" + envTok, secret: envTok}}, neg: []string{"cost is $5", "$lower=value"}},
		"env-file-secret": {pos: []patternCase{
			{text: "APP_ENV=prod\nDB_PASSWORD=" + envPW + "\nPORT=80", secret: envPW, keep: "DB_PASSWORD="},
			{text: "export GITHUB_TOKEN='" + envTok + "'", secret: envTok, keep: "export GITHUB_TOKEN="},
			{text: "STRIPE_API_KEY = " + envTok, secret: envTok},
		}, neg: []string{"MAX_TOKENS=4096", "TOKEN_EXPIRY=3600", "the SECRET = in prose"}},
	}
}

func TestBuiltinPatternsPositiveAndNegative(t *testing.T) {
	cases := builtinPatternCases()
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	for _, p := range BuiltInPatterns {
		c, ok := cases[p.Name]
		if !ok {
			t.Errorf("pattern %q has no positive/negative test cases", p.Name)
			continue
		}
		if len(c.pos) == 0 || len(c.neg) == 0 {
			t.Errorf("pattern %q needs at least one positive and one negative case", p.Name)
		}
		if p.Severity == "" {
			t.Errorf("pattern %q has no severity", p.Name)
		}
		single := NewRedactor([]*regexp.Regexp{p.Pattern})
		for _, pc := range c.pos {
			if !p.Pattern.MatchString(pc.text) {
				t.Errorf("%s: should match %q", p.Name, pc.text)
				continue
			}
			for _, red := range []*Redactor{single, r} {
				got := red.RedactText(pc.text)
				if strings.Contains(got, pc.secret) {
					t.Errorf("%s: secret survived redaction: %q", p.Name, got)
				}
				if pc.keep != "" && !strings.Contains(got, pc.keep) {
					t.Errorf("%s: context %q should survive, got %q", p.Name, pc.keep, got)
				}
			}
		}
		for _, neg := range c.neg {
			if got := single.RedactText(neg); got != neg {
				t.Errorf("%s: should not redact %q, got %q", p.Name, neg, got)
			}
		}
	}
}

// TestKeywordPrefilterIsExact checks the prefilter invariant: running only
// the rules whose keywords occur gives exactly the spans that running every
// rule gives.
func TestKeywordPrefilterIsExact(t *testing.T) {
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	naive := func(s []byte) []span {
		var out []span
		for i := range r.rules {
			out = r.rules[i].appendSpans(s, out)
		}
		return mergeSpans(out)
	}
	for name, c := range builtinPatternCases() {
		inputs := append([]string{}, c.neg...)
		for _, pc := range c.pos {
			inputs = append(inputs, pc.text, strings.ToUpper(pc.text), strings.ToLower(pc.text))
		}
		for _, in := range inputs {
			got := r.findSpans([]byte(in))
			want := naive([]byte(in))
			if len(got) != len(want) {
				t.Errorf("%s: prefilter spans %v != naive %v for %q", name, got, want, in)
				continue
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("%s: prefilter spans %v != naive %v for %q", name, got, want, in)
					break
				}
			}
		}
	}
}

func TestCustomPatternPrefilter(t *testing.T) {
	// Literal prefix → keyword; no prefix (case-insensitive) → always run.
	r := NewRedactor([]*regexp.Regexp{
		regexp.MustCompile(`itk_[0-9a-f]{16}`),
		regexp.MustCompile(`(?i)internal-[0-9]{6}`),
	})
	got := r.RedactText("a itk_0123456789abcdef b INTERNAL-123456 c")
	if got != "a [SECRET_REDACTED] b [SECRET_REDACTED] c" {
		t.Errorf("custom patterns: got %q", got)
	}
	if len(r.always) != 1 {
		t.Errorf("expected one always-on rule, got %d", len(r.always))
	}
}

func TestRedactTextSingleScanNoDoubleReplace(t *testing.T) {
	// Overlapping matches of two patterns become one marker.
	g := newFakeGen(7)
	jwt := g.jwt()
	r := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	got := r.RedactText("Authorization: Bearer " + jwt)
	if strings.Count(got, SecretRedacted) != 1 || strings.Contains(got, jwt) {
		t.Errorf("got %q", got)
	}
}

func TestEntropyDetector(t *testing.T) {
	g := newFakeGen(99)
	tok := g.chars(alphaNum, 32)
	off := NewRedactor(PatternsToRegexps(BuiltInPatterns))
	on := NewRedactorWithOptions(PatternsToRegexps(BuiltInPatterns), RedactorOptions{Entropy: true})

	near := "internal service key: " + tok
	if got := off.RedactText(near); got != near {
		t.Errorf("entropy must be off by default, got %q", got)
	}
	if got := on.RedactText(near); strings.Contains(got, tok) {
		t.Errorf("entropy on: token near keyword should be redacted, got %q", got)
	}
	far := "a random identifier with no hint word nearby at all " + tok
	if got := on.RedactText(far); got != far {
		t.Errorf("entropy on: token far from keyword must be kept, got %q", got)
	}
	for _, low := range []string{
		"token: " + strings.Repeat("ab", 16),                  // low entropy
		"token: 9fceb02d0ae598e95dc970b74767f19372d61af8",     // hex SHA-1 (< 4 bits/char)
		"token: 3f2b8c1e-9a4d-4e7b-8c21-0242ac120002",         // UUID
		"password reset for user_account_management_settings", // identifier
	} {
		if got := on.RedactText(low); got != low {
			t.Errorf("entropy on: %q should be kept, got %q", low, got)
		}
	}
	// JSON: the key names the value.
	doc := []byte(`{"stripe_signing_key":"` + tok + `","note":"` + tok + `"}`)
	out, n, _ := on.RedactJSON(doc)
	if n != 1 || string(out) != `{"stripe_signing_key":"[SECRET_REDACTED]","note":"`+tok+`"}` {
		t.Errorf("entropy JSON key context: n=%d out=%s", n, out)
	}
}
