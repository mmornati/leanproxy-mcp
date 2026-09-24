// Package governor holds the building blocks of the response token governor
// (issue #319): its `response:` config block, the per-session spill store
// that keeps the full (redacted) result of a shortened tool call, the smart
// text and structural JSON truncation, the read_result retrieval primitives
// (paging, grep, a JSONPath subset), and the field projection of #320
// (project.go).
//
// It has no dependency on pkg/mcp or pkg/migrate, so both can import it;
// pkg/mcp wraps it as a pipeline Middleware (see pkg/mcp/middleware_governor.go).
package governor

import (
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mmornati/leanproxy-mcp/pkg/httpsec"
)

// Defaults of the `response:` block.
const (
	// DefaultEnabled: the governor is off by default in v1.0-rc1 (issue
	// #319); it is turned on per config.
	DefaultEnabled = false
	// DefaultMaxTokens is the per-call budget, in estimated tokens
	// (pkg/reporter.Estimator: 1 token ≈ 4 bytes), when response.max_tokens
	// is unset.
	DefaultMaxTokens = 4000
	// MinMaxTokens is the smallest non-zero budget: below it the head, the
	// tail and the marker no longer fit.
	MinMaxTokens = 100
	// DefaultTTL is how long a spilled result stays retrievable.
	DefaultTTL = 30 * time.Minute
	// DefaultMaxBytes caps the spill store (LRU by bytes): 128 MiB.
	DefaultMaxBytes int64 = 128 << 20
	// DefaultDir is where spill.disk writes results ("~" is the home
	// directory).
	DefaultDir = "~/.leanproxy/results"

	// DefaultDedupMinTokens is the smallest result (estimated tokens) that
	// in-session dedup (#321) tracks. Smaller results are not worth the
	// per-session hash bookkeeping.
	DefaultDedupMinTokens = 500

	// DefaultSummarizeProvider is the only local-LLM provider summarization
	// supports (#321): the existing Ollama sidecar plumbing.
	DefaultSummarizeProvider = "ollama"
	// DefaultSummarizeThresholdTokens is response.summarize.threshold_tokens
	// when unset: a result must be at least this large (estimated tokens)
	// for summarization to run on it.
	DefaultSummarizeThresholdTokens = 8000
	// DefaultSummarizeMaxTokens is response.summarize.max_summary_tokens
	// when unset: the summary is capped to about this many estimated
	// tokens.
	DefaultSummarizeMaxTokens = 800
	// DefaultSummarizeTimeout is response.summarize.timeout when unset.
	DefaultSummarizeTimeout = 10 * time.Second
	// DefaultSummarizeURL is response.summarize.url when unset: the local
	// Ollama sidecar's default.
	DefaultSummarizeURL = "http://localhost:11434"
	// maxSummarizeInputBytes caps what is ever sent to the summarizer,
	// independent of threshold_tokens, so one call cannot balloon the
	// local model's request.
	maxSummarizeInputBytes = 120 * 1024
)

// Config is the `response:` block.
//
// Field projection (#320) adds projections and default_projections; later
// stories (#321 dedup and summarization) add their settings here too.
type Config struct {
	// Enabled switches the governor on. Off by default (DefaultEnabled).
	Enabled bool `yaml:"enabled"`
	// MaxTokens is the global per-call budget in estimated tokens. Unset
	// means DefaultMaxTokens; 0 means no truncation (results are still
	// counted).
	MaxTokens *int `yaml:"max_tokens,omitempty"`
	// Tools are per-server or per-tool overrides, matched against the
	// call's "server.tool" identity with path.Match globs ("github.*",
	// "fs.read_file"). The first matching rule wins.
	Tools []ToolRule `yaml:"tools,omitempty"`
	// Spill configures where the full results are kept.
	Spill SpillConfig `yaml:"spill,omitempty"`
	// Projections are per-server or per-tool field projection rules
	// (#320), matched like Tools; the first matching rule wins.
	Projections []ProjectionRule `yaml:"projections,omitempty"`
	// DefaultProjections applies the built-in drop pack (DefaultDropPaths)
	// to the tools no projection rule matches. Off by default.
	DefaultProjections bool `yaml:"default_projections,omitempty"`
	// Dedup is "off" (default) or "on": in-session dedup of byte-identical
	// results (#321). Keyed per client session only — never across
	// sessions, so one session can never learn what another session saw.
	Dedup string `yaml:"dedup,omitempty"`
	// Summarize is response.summarize (#321): optional local-LLM
	// summarization of results still over budget after projection and
	// dedup. nil or Enabled: false (the default) turns it off.
	Summarize *SummarizeConfig `yaml:"summarize,omitempty"`
}

// SummarizeConfig is the response.summarize block (#321). Off by default;
// summarization only ever runs for the tools listed in Tools.
type SummarizeConfig struct {
	// Enabled switches summarization on. Off by default.
	Enabled bool `yaml:"enabled,omitempty"`
	// Provider is the local-LLM provider. Only "ollama" is supported
	// (empty means "ollama").
	Provider string `yaml:"provider,omitempty"`
	// Model is the Ollama model. Empty means the sidecar's default model.
	Model string `yaml:"model,omitempty"`
	// URL is the Ollama server's base URL. Must be a loopback address
	// unless AllowRemote is set (only local providers are allowed).
	URL string `yaml:"url,omitempty"`
	// ThresholdTokens: a result must be at least this large (estimated
	// tokens) before summarization is attempted. Unset means
	// DefaultSummarizeThresholdTokens.
	ThresholdTokens *int `yaml:"threshold_tokens,omitempty"`
	// MaxSummaryTokens caps the summary's estimated size. Unset means
	// DefaultSummarizeMaxTokens.
	MaxSummaryTokens *int `yaml:"max_summary_tokens,omitempty"`
	// Tools is a glob allowlist ("server.tool" identities, path.Match
	// syntax). Required: nothing is summarized unless listed here.
	Tools []string `yaml:"tools,omitempty"`
	// Timeout bounds one summarization call (a Go duration). Unset means
	// DefaultSummarizeTimeout. On timeout or any error, the result falls
	// back to truncation (#319).
	Timeout string `yaml:"timeout,omitempty"`
	// AllowRemote allows a non-loopback URL. Off by default: only local
	// providers are allowed, so a redacted result is never sent off-box
	// without an explicit opt-in.
	AllowRemote bool `yaml:"allow_remote,omitempty"`
}

// ProjectionRule is one entry of response.projections.
type ProjectionRule struct {
	// Match is a "server.tool" glob (path.Match syntax).
	Match string `yaml:"match"`
	// Keep is an allowlist of paths (see project.go for the syntax).
	Keep []string `yaml:"keep,omitempty"`
	// Drop is a denylist of paths. Exclusive with Keep. A rule with
	// neither projects nothing: it exempts the matching tools from later
	// rules and from the default pack.
	Drop []string `yaml:"drop,omitempty"`
}

// ToolRule is one entry of response.tools.
type ToolRule struct {
	// Match is a "server.tool" glob (path.Match syntax).
	Match string `yaml:"match"`
	// MaxTokens overrides the budget for the matching tools (0: no
	// truncation). Unset keeps the global budget.
	MaxTokens *int `yaml:"max_tokens,omitempty"`
	// Passthrough never shortens the matching tools' results.
	Passthrough bool `yaml:"passthrough,omitempty"`
}

// SpillConfig is the response.spill block.
type SpillConfig struct {
	// TTL is how long a spilled result stays retrievable (a Go duration).
	// Empty means DefaultTTL.
	TTL string `yaml:"ttl,omitempty"`
	// MaxBytes caps the bytes kept (LRU eviction). 0 or unset means
	// DefaultMaxBytes.
	MaxBytes int64 `yaml:"max_bytes,omitempty"`
	// Disk keeps the spilled results in files (mode 0600, in a per-process
	// directory under Dir) instead of memory.
	Disk bool `yaml:"disk,omitempty"`
	// Dir is the parent directory of the spill files. Empty means
	// DefaultDir.
	Dir string `yaml:"dir,omitempty"`
}

// Validate checks the block. A nil receiver is valid (governor off).
func (c *Config) Validate() error {
	if c == nil {
		return nil
	}
	if err := validateBudget("response.max_tokens", c.MaxTokens); err != nil {
		return err
	}
	for i, r := range c.Tools {
		field := fmt.Sprintf("response.tools[%d]", i)
		if strings.TrimSpace(r.Match) == "" {
			return fmt.Errorf("%s: match is required", field)
		}
		if _, err := path.Match(r.Match, ""); err != nil {
			return fmt.Errorf("%s: invalid match glob %q: %w", field, r.Match, err)
		}
		if err := validateBudget(field+".max_tokens", r.MaxTokens); err != nil {
			return err
		}
		if r.Passthrough && r.MaxTokens != nil {
			return fmt.Errorf("%s: passthrough and max_tokens are mutually exclusive", field)
		}
	}
	for i, r := range c.Projections {
		field := fmt.Sprintf("response.projections[%d]", i)
		if strings.TrimSpace(r.Match) == "" {
			return fmt.Errorf("%s: match is required", field)
		}
		if _, err := path.Match(r.Match, ""); err != nil {
			return fmt.Errorf("%s: invalid match glob %q: %w", field, r.Match, err)
		}
		if len(r.Keep) == 0 && len(r.Drop) == 0 {
			continue // an exemption
		}
		if _, err := CompileProjection(r.Keep, r.Drop); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	if c.Spill.TTL != "" {
		d, err := time.ParseDuration(c.Spill.TTL)
		if err != nil {
			return fmt.Errorf("response.spill.ttl: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("response.spill.ttl must be > 0, got %s", c.Spill.TTL)
		}
	}
	if c.Spill.MaxBytes < 0 {
		return fmt.Errorf("response.spill.max_bytes must be >= 0, got %d", c.Spill.MaxBytes)
	}
	if d := c.Spill.Dir; d != "" && !strings.HasPrefix(d, "/") && d != "~" && !strings.HasPrefix(d, "~/") {
		return fmt.Errorf("response.spill.dir must be an absolute path or start with ~/, got %q", d)
	}
	switch strings.ToLower(strings.TrimSpace(c.Dedup)) {
	case "", "off", "on":
	default:
		return fmt.Errorf("response.dedup must be %q or %q, got %q", "off", "on", c.Dedup)
	}
	if err := c.Summarize.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate checks the response.summarize block. A nil receiver, or one
// with Enabled: false, is always valid.
func (c *SummarizeConfig) Validate() error {
	if c == nil || !c.Enabled {
		return nil
	}
	if p := strings.ToLower(strings.TrimSpace(c.Provider)); p != "" && p != DefaultSummarizeProvider {
		return fmt.Errorf("response.summarize.provider: only %q is supported, got %q (local providers only)", DefaultSummarizeProvider, c.Provider)
	}
	if len(c.Tools) == 0 {
		return fmt.Errorf("response.summarize.tools is required: nothing is summarized unless listed")
	}
	for i, t := range c.Tools {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("response.summarize.tools[%d] is empty", i)
		}
		if _, err := path.Match(t, ""); err != nil {
			return fmt.Errorf("response.summarize.tools[%d]: invalid glob %q: %w", i, t, err)
		}
	}
	if c.ThresholdTokens != nil && *c.ThresholdTokens <= 0 {
		return fmt.Errorf("response.summarize.threshold_tokens must be > 0, got %d", *c.ThresholdTokens)
	}
	if c.MaxSummaryTokens != nil && *c.MaxSummaryTokens <= 0 {
		return fmt.Errorf("response.summarize.max_summary_tokens must be > 0, got %d", *c.MaxSummaryTokens)
	}
	if c.Timeout != "" {
		d, err := time.ParseDuration(c.Timeout)
		if err != nil {
			return fmt.Errorf("response.summarize.timeout: %w", err)
		}
		if d <= 0 {
			return fmt.Errorf("response.summarize.timeout must be > 0, got %s", c.Timeout)
		}
	}
	u := c.URLValue()
	parsed, err := url.Parse(u)
	if err != nil || parsed.Hostname() == "" {
		return fmt.Errorf("response.summarize.url: invalid URL %q", u)
	}
	if !c.AllowRemote && !httpsec.IsLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("response.summarize.url must be a loopback address (got %q); set allow_remote: true to use a remote provider (only local providers are allowed by default)", u)
	}
	return nil
}

// DedupEnabled reports whether response.dedup is "on".
func (c *Config) DedupEnabled() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Dedup), "on")
}

// SummarizeEnabled reports whether response.summarize is on.
func (c *Config) SummarizeEnabled() bool {
	return c != nil && c.Summarize != nil && c.Summarize.Enabled
}

// ProviderValue is response.summarize.provider with its default.
func (c *SummarizeConfig) ProviderValue() string {
	if c == nil || strings.TrimSpace(c.Provider) == "" {
		return DefaultSummarizeProvider
	}
	return c.Provider
}

// URLValue is response.summarize.url with its default.
func (c *SummarizeConfig) URLValue() string {
	if c == nil || strings.TrimSpace(c.URL) == "" {
		return DefaultSummarizeURL
	}
	return c.URL
}

// ThresholdTokensValue is response.summarize.threshold_tokens with its
// default.
func (c *Config) ThresholdTokensValue() int {
	if c == nil || c.Summarize == nil || c.Summarize.ThresholdTokens == nil {
		return DefaultSummarizeThresholdTokens
	}
	return *c.Summarize.ThresholdTokens
}

// MaxSummaryTokensValue is response.summarize.max_summary_tokens with its
// default.
func (c *Config) MaxSummaryTokensValue() int {
	if c == nil || c.Summarize == nil || c.Summarize.MaxSummaryTokens == nil {
		return DefaultSummarizeMaxTokens
	}
	return *c.Summarize.MaxSummaryTokens
}

// SummarizeTimeoutValue is response.summarize.timeout with its default.
func (c *Config) SummarizeTimeoutValue() time.Duration {
	if c == nil || c.Summarize == nil || c.Summarize.Timeout == "" {
		return DefaultSummarizeTimeout
	}
	d, err := time.ParseDuration(c.Summarize.Timeout)
	if err != nil || d <= 0 {
		return DefaultSummarizeTimeout
	}
	return d
}

// MatchesSummarizeTool reports whether identity ("server.tool") is listed
// in response.summarize.tools. False (never summarized) when summarize is
// off or the allowlist is empty: nothing is summarized unless listed.
func (c *Config) MatchesSummarizeTool(identity string) bool {
	if !c.SummarizeEnabled() {
		return false
	}
	for _, glob := range c.Summarize.Tools {
		if matchIdentity(glob, identity) {
			return true
		}
	}
	return false
}

// MaxSummarizeInputBytes caps what is ever sent to the summarizer.
func MaxSummarizeInputBytes() int { return maxSummarizeInputBytes }

func validateBudget(field string, v *int) error {
	if v == nil {
		return nil
	}
	if *v < 0 {
		return fmt.Errorf("%s must be >= 0, got %d", field, *v)
	}
	if *v != 0 && *v < MinMaxTokens {
		return fmt.Errorf("%s must be 0 (no truncation) or >= %d, got %d", field, MinMaxTokens, *v)
	}
	return nil
}

// Budget is what the governor applies to one tool.
type Budget struct {
	// MaxTokens is the budget in estimated tokens; 0 means no truncation.
	MaxTokens int
	// Passthrough: the result is never shortened.
	Passthrough bool
}

// Limited reports whether results under this budget may be shortened.
func (b Budget) Limited() bool { return !b.Passthrough && b.MaxTokens > 0 }

// GlobalMaxTokens is response.max_tokens with its default.
func (c *Config) GlobalMaxTokens() int {
	if c == nil || c.MaxTokens == nil {
		return DefaultMaxTokens
	}
	return *c.MaxTokens
}

// BudgetFor returns the budget of the tool whose "server.tool" identity is
// given: the first matching response.tools rule, else the global budget.
func (c *Config) BudgetFor(identity string) Budget {
	global := Budget{MaxTokens: c.GlobalMaxTokens()}
	if c == nil {
		return global
	}
	for _, r := range c.Tools {
		if !matchIdentity(r.Match, identity) {
			continue
		}
		if r.Passthrough {
			return Budget{Passthrough: true}
		}
		if r.MaxTokens != nil {
			return Budget{MaxTokens: *r.MaxTokens}
		}
		return global
	}
	return global
}

// matchIdentity reports whether a "server.tool" glob matches identity.
func matchIdentity(glob, identity string) bool {
	if glob == identity {
		return true
	}
	ok, err := path.Match(glob, identity)
	return err == nil && ok
}

// RuleProjection is the projection configured for one tool.
type RuleProjection struct {
	*Projection
	// Rule names where it comes from: the matching rule's glob, or
	// "default_projections".
	Rule string
}

// Projections is the compiled response.projections block (with the
// default pack when enabled).
type Projections struct {
	rules []compiledRule
	pack  *Projection
}

type compiledRule struct {
	match string
	proj  *Projection // nil: an exemption
}

// CompileProjections compiles response.projections. Validate must have
// passed; a rule that does not compile is skipped.
func (c *Config) CompileProjections() *Projections {
	ps := &Projections{}
	if c == nil {
		return ps
	}
	for _, r := range c.Projections {
		cr := compiledRule{match: r.Match}
		if len(r.Keep) > 0 || len(r.Drop) > 0 {
			p, err := CompileProjection(r.Keep, r.Drop)
			if err != nil {
				continue
			}
			cr.proj = p
		}
		ps.rules = append(ps.rules, cr)
	}
	if c.DefaultProjections {
		ps.pack, _ = CompileProjection(nil, DefaultDropPaths)
	}
	return ps
}

// Len is the number of configured rules (the default pack excluded).
func (ps *Projections) Len() int {
	if ps == nil {
		return 0
	}
	return len(ps.rules)
}

// Default reports whether the default pack is on.
func (ps *Projections) Default() bool { return ps != nil && ps.pack != nil }

// For returns the projection of the tool whose "server.tool" identity is
// given: the first matching rule, else the default pack, else none.
func (ps *Projections) For(identity string) (RuleProjection, bool) {
	if ps == nil {
		return RuleProjection{}, false
	}
	for _, r := range ps.rules {
		if !matchIdentity(r.match, identity) {
			continue
		}
		if r.proj == nil {
			return RuleProjection{}, false
		}
		return RuleProjection{Projection: r.proj, Rule: r.match}, true
	}
	if ps.pack != nil {
		return RuleProjection{Projection: ps.pack, Rule: "default_projections"}, true
	}
	return RuleProjection{}, false
}

// TTLValue is spill.ttl with its default (Validate must have passed).
func (c *Config) TTLValue() time.Duration {
	if c == nil || c.Spill.TTL == "" {
		return DefaultTTL
	}
	d, err := time.ParseDuration(c.Spill.TTL)
	if err != nil || d <= 0 {
		return DefaultTTL
	}
	return d
}

// MaxBytesValue is spill.max_bytes with its default.
func (c *Config) MaxBytesValue() int64 {
	if c == nil || c.Spill.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return c.Spill.MaxBytes
}

// DirValue is spill.dir with its default, "~" expanded against home.
func (c *Config) DirValue(home string) string {
	d := DefaultDir
	if c != nil && c.Spill.Dir != "" {
		d = c.Spill.Dir
	}
	if d == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(d, "~/"); ok {
		return filepath.Join(home, rest)
	}
	return d
}
