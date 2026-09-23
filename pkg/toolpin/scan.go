package toolpin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
)

// Severity ranks a scanner finding.
type Severity string

const (
	SeverityLow    Severity = "low"
	SeverityMedium Severity = "medium"
	SeverityHigh   Severity = "high"
)

func (s Severity) rank() int {
	switch s {
	case SeverityHigh:
		return 3
	case SeverityMedium:
		return 2
	case SeverityLow:
		return 1
	}
	return 0
}

// AtLeast reports whether s is as severe as min or more.
func (s Severity) AtLeast(min Severity) bool { return s.rank() >= min.rank() }

// Finding is one scanner result for a tool.
type Finding struct {
	// Rule names the heuristic or pattern ("tool-use-precondition",
	// "hidden-unicode", "injection/ignore-previous-instructions", ...).
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	// Field is the part of the definition it was found in: title,
	// description, inputSchema, outputSchema or annotations.
	Field string `json:"field"`
	// Detail explains the finding (never more than a host name or a
	// count from the scanned text).
	Detail string `json:"detail,omitempty"`
}

func (f Finding) String() string {
	return fmt.Sprintf("%s %s in %s", f.Severity, f.Rule, f.Field)
}

// MaxSeverity returns the highest severity of fs ("" when empty).
func MaxSeverity(fs []Finding) Severity {
	var max Severity
	for _, f := range fs {
		if f.Severity.rank() > max.rank() {
			max = f.Severity
		}
	}
	return max
}

// Severity thresholds on pattern weights: >= 80 high, >= 50 medium.
const (
	highWeight   = 80
	mediumWeight = 50
)

func severityFromWeight(w int) Severity {
	switch {
	case w >= highWeight:
		return SeverityHigh
	case w >= mediumWeight:
		return SeverityMedium
	default:
		return SeverityLow
	}
}

// ToolPoisoningPatternDefs is the scanner's own pattern set (separate from
// the injection guard's defaults, which also run, as "injection/<name>").
// Patterns match the injection engine's normalized text: lower-cased,
// invisible characters dropped, whitespace collapsed. A weight of 80 or
// more is a high-severity finding, 50 to 79 medium, below 50 low.
var ToolPoisoningPatternDefs = []injection.PatternDef{
	{
		Name:        "tool-use-precondition",
		Pattern:     `\b(before|prior\s+to|instead\s+of)\s+(using|calling|invoking|running|executing)\s+(this|the|any|other|each|every)\s+(\w+\s+)?(tools?|functions?|commands?)\b`,
		Weight:      60,
		Enabled:     true,
		Description: "Tells the model what to do before or instead of using a tool",
	},
	{
		Name:        "instruction-tag",
		Pattern:     `<\s*/?\s*(important|system|instructions?|admin|secret|hidden|critical|note\s+to\s+(the\s+)?(ai|assistant|model))\s*>`,
		Weight:      80,
		Enabled:     true,
		Description: "Pseudo-tag used to smuggle instructions to the model",
	},
	{
		Name:        "role-prefix",
		Pattern:     `(?m)(^|[\[<])\s*(system|assistant)\s*(:|\]|>)`,
		Weight:      60,
		Enabled:     true,
		Description: "Fake system or assistant turn",
	},
	{
		Name:        "conceal-from-user",
		Pattern:     `\b(do\s+not|don'?t|never|without)\s+(tell|telling|inform|informing|mention|mentioning|reveal|revealing|show|showing|alert|alerting|notify|notifying|let)\s+(this\s+|it\s+|anything\s+)?(to\s+)?(the\s+)?(users?|human|operator)\b|\b(the\s+)?users?\s+(must|should|will|need)\s+not\s+(see|know|notice|be\s+told|find\s+out)\b`,
		Weight:      90,
		Enabled:     true,
		Description: "Asks the model to hide something from the user",
	},
	{
		Name:        "sensitive-path",
		Pattern:     `(~/\.ssh\b|\.ssh/|\bid_(rsa|dsa|ecdsa|ed25519)\b|(^|[\s/"'(=:,])\.env\b|/etc/(passwd|shadow)\b|\.aws/credentials|\.netrc\b|\.npmrc\b|\.pypirc\b|\.kube/config\b|\.docker/config\.json|\.git-credentials|\.config/gh/hosts\.ya?ml)`,
		Weight:      50,
		Enabled:     true,
		Description: "Mentions a file that holds secrets",
	},
	{
		Name:        "sensitive-file-access",
		Pattern:     `\b(read|cat|open|load|include|pass|send|upload|attach|copy|forward|post|provide|append|embed|extract|collect|fetch|get|use)\b[^\n]{0,80}?(~/\.ssh\b|\.ssh/|\bid_(rsa|dsa|ecdsa|ed25519)\b|(^|[\s/"'(=:,])\.env\b|/etc/(passwd|shadow)\b|\.aws/credentials|\.netrc\b|\.git-credentials|\.kube/config\b|\.docker/config\.json|\.config/gh/hosts\.ya?ml)`,
		Weight:      90,
		Enabled:     true,
		Description: "Asks the model to read or pass on a file that holds secrets",
	},
	{
		Name:        "secret-in-parameter",
		Pattern:     `\b(pass|put|include|insert|add|send|place|copy|paste|provide)\s+(\S+\s+){0,6}?(contents?|keys?|tokens?|credentials?|passwords?|secrets?|history|conversation)\s+(\S+\s+){0,4}?(in|into|as|via|inside)\s+(the\s+)?\S+\s+(field|parameter|param|argument|arg)\b`,
		Weight:      60,
		Enabled:     true,
		Description: "Asks the model to smuggle data through a parameter",
	},
	{
		Name:        "model-directive",
		Pattern:     `\b(you|the\s+(assistant|model|ai|agent|llm))\s+(must|should|shall|need\s+to|have\s+to|are\s+required\s+to)\s+((always|also|first|now|immediately)\s+)*(call|invoke|read|send|include|pass|forward|ignore|disregard|execute|run)\b`,
		Weight:      50,
		Enabled:     true,
		Description: "Instruction addressed to the model",
	},
	{
		Name:        "tool-override",
		Pattern:     `\b(instead\s+of|rather\s+than|do\s+not|don'?t|never)\s+(use|using|call|calling|invoke|invoking)\s+(the\s+)?[\w.-]+\s+(tool|function|server)\b`,
		Weight:      60,
		Enabled:     true,
		Description: "Steers the model away from another tool (shadowing)",
	},
	{
		Name:        "redirect-traffic",
		Pattern:     `\b(all|every|any)\s+(emails?|messages?|requests?|calls?|payments?|transfers?)\s+(must|should|shall|will)\s+(always\s+)?(be\s+)?(sent|forwarded|redirected|copied|cc'?d|bcc'?d|routed)\s+to\b`,
		Weight:      85,
		Enabled:     true,
		Description: "Redirects other tools' traffic to a fixed destination",
	},
	{
		Name:        "credential-reference",
		Pattern:     `\b(api[\s_-]?keys?|access[\s_-]?tokens?|tokens?|credentials?|passwords?|secrets?|private[\s_-]keys?)\b`,
		Weight:      20,
		Enabled:     true,
		Description: "Mentions credentials",
	},
}

// maxScanChars caps the text of one field that is classified (head and
// tail are kept).
const maxScanChars = 64 << 10

var (
	urlRe = regexp.MustCompile(`(?i)\b(?:https?|ftp|wss?)://([^\s/?#"'<>()\[\]{}\\]+)`)
	// base64Re finds long runs of the (standard or URL-safe) base64
	// alphabet; candidates are then checked for mixed character classes.
	base64Re = regexp.MustCompile(`[A-Za-z0-9+/_-]{80,}={0,2}`)
)

// ScannerOptions configures a Scanner.
type ScannerOptions struct {
	// AllowedDomains are URL hosts never reported (with their subdomains).
	AllowedDomains []string
	// MaxDescriptionChars is the long-description threshold (default
	// DefaultMaxDescriptionChars).
	MaxDescriptionChars int
}

// Scanner looks for poisoning signals in tool metadata. It is safe for
// concurrent use.
type Scanner struct {
	poison  *injection.Classifier
	guard   *injection.Classifier
	allowed []string
	maxDesc int
}

// NewScanner builds a scanner with ToolPoisoningPatternDefs and the
// injection guard's default patterns.
func NewScanner(o ScannerOptions) *Scanner {
	compiled := make([]*injection.InjectionPattern, 0, len(ToolPoisoningPatternDefs))
	for _, def := range ToolPoisoningPatternDefs {
		p, err := def.Compile()
		if err != nil {
			slog.Warn("toolpin: invalid scanner pattern, skipping", "name", def.Name, "error", err)
			continue
		}
		compiled = append(compiled, p)
	}
	poison := injection.NewClassifier()
	poison.SetPatterns(compiled)
	maxDesc := o.MaxDescriptionChars
	if maxDesc <= 0 {
		maxDesc = DefaultMaxDescriptionChars
	}
	allowed := make([]string, 0, len(o.AllowedDomains))
	for _, d := range o.AllowedDomains {
		if d = normalizeHost(d); d != "" {
			allowed = append(allowed, d)
		}
	}
	return &Scanner{poison: poison, guard: injection.NewClassifier(), allowed: allowed, maxDesc: maxDesc}
}

// Scan returns the findings for d, most severe first. serverDomains are
// extra allowed URL hosts (the server's own).
func (s *Scanner) Scan(d Definition, serverDomains []string) []Finding {
	var out []Finding
	if d.Title != "" {
		out = append(out, s.scanField("title", []string{d.Title}, serverDomains)...)
	}
	if d.Description != "" {
		out = append(out, s.scanField("description", []string{d.Description}, serverDomains)...)
		if n := utf8.RuneCountInString(d.Description); n > s.maxDesc {
			out = append(out, Finding{Rule: "long-description", Severity: SeverityLow, Field: "description",
				Detail: fmt.Sprintf("%d characters (limit %d)", n, s.maxDesc)})
		}
	}
	for _, f := range []struct {
		name string
		raw  json.RawMessage
	}{{"inputSchema", d.InputSchema}, {"outputSchema", d.OutputSchema}, {"annotations", d.Annotations}} {
		if texts := jsonStringValues(f.raw); len(texts) > 0 {
			out = append(out, s.scanField(f.name, texts, serverDomains)...)
		}
	}
	sortFindings(out)
	return out
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if a, b := fs[i].Severity.rank(), fs[j].Severity.rank(); a != b {
			return a > b
		}
		if fs[i].Field != fs[j].Field {
			return fs[i].Field < fs[j].Field
		}
		return fs[i].Rule < fs[j].Rule
	})
}

func (s *Scanner) scanField(field string, texts []string, serverDomains []string) []Finding {
	var out []Finding
	add := func(rule string, sev Severity, detail string) {
		for _, f := range out {
			if f.Rule == rule {
				return
			}
		}
		out = append(out, Finding{Rule: rule, Severity: sev, Field: field, Detail: detail})
	}

	hidden, invisible := 0, 0
	for _, t := range texts {
		for _, r := range t {
			if IsInvisible(r) {
				invisible++
				if isHiddenText(r) {
					hidden++
				}
			}
		}
	}
	switch {
	case hidden > 0:
		add("hidden-unicode", SeverityHigh, fmt.Sprintf("%d bidi-control or tag characters (stripped before reaching the client)", hidden))
	case invisible > 0:
		add("invisible-unicode", SeverityMedium, fmt.Sprintf("%d zero-width characters (stripped before reaching the client)", invisible))
	}

	regexHits := 0
	for _, c := range []struct {
		cl     *injection.Classifier
		prefix string
	}{{s.poison, ""}, {s.guard, "injection/"}} {
		tb := injection.NewTextBuilder()
		for _, t := range texts {
			tb.AddString(capText(t))
		}
		res := c.cl.ClassifyNormalized(tb.Text())
		tb.Release()
		for _, m := range res.Matches {
			if m.Weight <= 0 {
				continue
			}
			sev := severityFromWeight(m.Weight)
			if sev.AtLeast(SeverityMedium) {
				regexHits++
			}
			add(c.prefix+m.PatternName, sev, m.Description)
		}
	}
	if regexHits >= 2 && MaxSeverity(out) != SeverityHigh {
		add("combined-signals", SeverityHigh, fmt.Sprintf("%d medium-severity patterns in the same field", regexHits))
	}

	var hosts []string
	var blobs int
	for _, t := range texts {
		for _, m := range urlRe.FindAllStringSubmatch(capText(t), -1) {
			h := normalizeHost(m[1])
			if h == "" || s.allowedHost(h, serverDomains) || containsString(hosts, h) {
				continue
			}
			hosts = append(hosts, h)
		}
		for _, cand := range base64Re.FindAllString(capText(t), -1) {
			if looksLikeBase64(cand) {
				blobs++
			}
		}
	}
	if len(hosts) > 0 {
		sort.Strings(hosts)
		if len(hosts) > 3 {
			hosts = append(hosts[:3], "...")
		}
		add("external-url", SeverityMedium, "links to "+strings.Join(hosts, ", "))
	}
	if blobs > 0 {
		add("base64-blob", SeverityMedium, fmt.Sprintf("%d base64-like blob(s) of 80+ characters", blobs))
	}
	return out
}

// capText keeps the head and tail of an oversized text.
func capText(t string) string {
	if len(t) <= maxScanChars {
		return t
	}
	half := maxScanChars / 2
	head := t[:half]
	for len(head) > 0 && !utf8.ValidString(head) {
		head = head[:len(head)-1]
	}
	tail := t[len(t)-half:]
	for len(tail) > 0 && !utf8.ValidString(tail) {
		tail = tail[1:]
	}
	return head + "\n" + tail
}

// looksLikeBase64 requires the three character classes of encoded binary
// data, so a long identifier or hex digest is not reported.
func looksLikeBase64(s string) bool {
	var upper, lower, digit bool
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			upper = true
		case unicode.IsLower(r):
			lower = true
		case unicode.IsDigit(r):
			digit = true
		}
	}
	return upper && lower && digit
}

func normalizeHost(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if i := strings.LastIndexByte(h, '@'); i >= 0 {
		h = h[i+1:]
	}
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	h = strings.Trim(h, "[].")
	return h
}

func (s *Scanner) allowedHost(h string, serverDomains []string) bool {
	switch h {
	case "localhost", "127.0.0.1", "::1", "example.com", "example.org", "example.net":
		return true
	}
	for _, list := range [][]string{s.allowed, serverDomains} {
		for _, d := range list {
			d = normalizeHost(d)
			if d != "" && (h == d || strings.HasSuffix(h, "."+d)) {
				return true
			}
		}
	}
	return false
}

func containsString(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// jsonStringValues returns every string value (not object keys) of a JSON
// document.
func jsonStringValues(raw json.RawMessage) []string {
	v, present, err := decodeRaw(raw)
	if err != nil || !present {
		return nil
	}
	var out []string
	var walk func(v interface{})
	walk = func(v interface{}) {
		switch x := v.(type) {
		case string:
			if x != "" {
				out = append(out, x)
			}
		case []interface{}:
			for _, e := range x {
				walk(e)
			}
		case map[string]interface{}:
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(x[k])
			}
		}
	}
	walk(v)
	return out
}
