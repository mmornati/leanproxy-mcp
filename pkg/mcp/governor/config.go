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
	"path"
	"path/filepath"
	"strings"
	"time"
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
	return nil
}

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
