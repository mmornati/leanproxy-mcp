package bouncer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Redaction corpus (testdata/corpus).
//
// positive_*.json are realistic MCP tool results (a GitHub .env file and a
// service-account key, Postgres rows, a Slack export, an HTTP request log)
// whose secrets are placeholders such as {{AWS_KEY_ID}} or {{PEM_RSA:2}}.
// Every occurrence is replaced by a freshly generated fake value (the
// optional :N suffix is how many JSON string-escaping levels the value sits
// under, which only matters for multi-line values). Values are assembled at
// run time from split prefixes, so no realistic-looking credential is ever
// committed (GitHub push protection rejects those).
//
// negative_*.json contain no secrets: UUIDs and digests, a base64 image,
// lorem ipsum and security-related prose, a tools/list response whose
// schemas have password / token / api_key parameters, and source code that
// reads credentials from the environment. Every redaction there is a false
// positive.

const (
	corpusMinRecall        = 0.95
	corpusMaxFPPer100KB    = 1.0
	corpusDir              = "testdata/corpus"
	corpusPlaceholderRegex = `\{\{([A-Z_]+)(?::([0-9]))?\}\}`
)

// corpusSecret is one labeled secret: kind is the placeholder name, core a
// substring of the value that must not survive redaction.
type corpusSecret struct {
	kind, core string
}

func corpusValue(g *fakeGen, kind string) (value, core string) {
	simple := map[string]func() string{
		"AWS_KEY_ID": g.awsKeyID, "AWS_TEMP": g.awsTempKeyID, "AWS_SECRET": g.awsSecret,
		"GH_PAT": g.githubPAT, "GH_OAUTH": g.githubOAuth, "GH_FINE": g.githubFine, "GITLAB": g.gitlabPAT,
		"STRIPE_LIVE": g.stripeLive, "STRIPE_TEST": g.stripeTest, "STRIPE_RK": g.stripeRestricted,
		"OPENAI": g.openAILegacy, "OPENAI_PROJ": g.openAIProject, "ANTHROPIC": g.anthropic,
		"GOOGLE_KEY": g.googleAPIKey, "GCP_OAUTH": g.gcpOAuth, "SLACK_BOT": g.slackBot,
		"SLACK_WEBHOOK": g.slackWebhook, "NPM": g.npmToken, "JWT": g.jwt, "BASIC": g.basicCreds,
		"PASSWORD": g.password, "DSN_PW": g.dsnPassword,
	}
	if f, ok := simple[kind]; ok {
		v := f()
		core := v
		if kind == "SLACK_WEBHOOK" {
			core = v[strings.LastIndex(v, "/")+1:]
		}
		return v, core
	}
	labels := map[string]string{"PEM_RSA": "RSA PRIVATE KEY", "PEM_PGP": "PGP PRIVATE KEY BLOCK"}
	if label, ok := labels[kind]; ok {
		block, body := g.pemBlock(label)
		return block, body[:strings.Index(body, "\n")]
	}
	return "", ""
}

// escapeLevels JSON-string-escapes s n times.
func escapeLevels(s string, n int) string {
	for i := 0; i < n; i++ {
		b, _ := json.Marshal(s)
		s = string(b[1 : len(b)-1])
	}
	return s
}

// materializeCorpus replaces the placeholders of a positive document.
func materializeCorpus(t *testing.T, g *fakeGen, doc string) (string, []corpusSecret) {
	t.Helper()
	re := regexp.MustCompile(corpusPlaceholderRegex)
	var secrets []corpusSecret
	out := re.ReplaceAllStringFunc(doc, func(m string) string {
		sub := re.FindStringSubmatch(m)
		v, core := corpusValue(g, sub[1])
		if v == "" {
			t.Fatalf("unknown placeholder %s", m)
		}
		level := 1
		if sub[2] != "" {
			level, _ = strconv.Atoi(sub[2])
		}
		secrets = append(secrets, corpusSecret{kind: sub[1], core: core})
		return escapeLevels(v, level)
	})
	return out, secrets
}

func TestRedactionCorpus(t *testing.T) {
	files, err := filepath.Glob(filepath.Join(corpusDir, "*.json"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}
	sort.Strings(files)
	g := newFakeGen(306)
	variants := []struct {
		name string
		r    *Redactor
	}{
		{"default", builtinRedactor()},
		{"entropy", NewRedactorWithOptions(PatternsToRegexps(BuiltInPatterns), RedactorOptions{Entropy: true})},
	}
	for _, v := range variants {
		total, caught := 0, 0
		missedByKind := map[string]int{}
		negBytes, negFP := 0, 0
		for _, f := range files {
			raw, err := os.ReadFile(f) // #nosec G304 -- test fixture path
			if err != nil {
				t.Fatal(err)
			}
			base := filepath.Base(f)
			switch {
			case strings.HasPrefix(base, "positive_"):
				doc, secrets := materializeCorpus(t, g, string(raw))
				if !json.Valid([]byte(doc)) {
					t.Fatalf("%s: materialized document is not valid JSON", base)
				}
				out, _, _ := v.r.RedactJSON([]byte(doc))
				if !json.Valid(out) {
					t.Errorf("%s: redacted output is not valid JSON", base)
				}
				for _, s := range secrets {
					total++
					if strings.Contains(string(out), s.core) {
						missedByKind[s.kind]++
						t.Logf("%s [%s]: missed %s secret", base, v.name, s.kind)
					} else {
						caught++
					}
				}
			case strings.HasPrefix(base, "negative_"):
				if strings.Contains(string(raw), "{{") && regexp.MustCompile(corpusPlaceholderRegex).Match(raw) {
					t.Fatalf("%s: negative file must not contain placeholders", base)
				}
				out, n, _ := v.r.RedactJSON(raw)
				negBytes += len(raw)
				negFP += n
				if n > 0 {
					t.Logf("%s [%s]: %d false positive(s)", base, v.name, n)
				} else if string(out) != string(raw) {
					t.Errorf("%s: clean document changed", base)
				}
			}
		}
		recall := float64(caught) / float64(total)
		fpPer100KB := float64(negFP) / (float64(negBytes) / (100 * 1024))
		t.Logf("corpus [%s]: recall %d/%d = %.1f%%; negative set %d bytes, %d false positives = %.2f per 100 KB; missed by kind: %v",
			v.name, caught, total, 100*recall, negBytes, negFP, fpPer100KB, missedByKind)
		if recall < corpusMinRecall {
			t.Errorf("[%s] recall %.3f below %.2f", v.name, recall, corpusMinRecall)
		}
		if fpPer100KB >= corpusMaxFPPer100KB {
			t.Errorf("[%s] %.2f false positives per 100 KB (limit < %.0f)", v.name, fpPer100KB, corpusMaxFPPer100KB)
		}
		if negBytes < 100*1024 {
			t.Errorf("negative set is only %d bytes; need at least 100 KB for a meaningful rate", negBytes)
		}
	}
}
