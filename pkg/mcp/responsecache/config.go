// Package responsecache implements the opt-in, exact-match tools/call
// response cache (issue #299): a bounded-by-bytes LRU keyed on the
// pre-redaction request, storing the redacted response. It is deliberately
// independent of pkg/migrate and pkg/cache so it can be imported from either
// without creating an import cycle; pkg/mcp wraps it as a Middleware.
package responsecache

import (
	"fmt"
	"path"
	"time"
)

// Defaults mirror the config example in issue #299.
const (
	DefaultTTL                 = 5 * time.Minute
	DefaultMaxBytes      int64 = 64 * 1024 * 1024 // 64 MiB total, LRU by bytes
	DefaultMaxEntryBytes int64 = 1024 * 1024      // larger responses are never cached
)

// Config is the `response_cache:` YAML block. It is off by default.
type Config struct {
	// Enabled is the master opt-in switch. Off by default: with a zero
	// Config, nothing is ever cached.
	Enabled bool `yaml:"enabled"`

	// TTL is a Go duration string (e.g. "5m"). Empty defaults to
	// DefaultTTL. TTLValue is populated by Normalize.
	TTL      string        `yaml:"ttl"`
	TTLValue time.Duration `yaml:"-"`

	// MaxBytes bounds total cached response bytes (LRU eviction by bytes).
	// <= 0 defaults to DefaultMaxBytes.
	MaxBytes int64 `yaml:"max_bytes"`

	// MaxEntryBytes is the largest single response ever cached; larger
	// responses are looked up (for potential hits) but never stored.
	// <= 0 defaults to DefaultMaxEntryBytes.
	MaxEntryBytes int64 `yaml:"max_entry_bytes"`

	// Tools is the explicit allowlist: entries are "server.tool" or a glob
	// such as "server.get_*" (path.Match syntax), matched against the
	// "server.tool" identity of the call. Only tools listed here — or,
	// when HonorAnnotations is set, tools whose upstream annotations say
	// readOnlyHint:true AND idempotentHint:true — are ever cached.
	Tools []string `yaml:"tools"`

	// HonorAnnotations additionally allows tools whose upstream MCP
	// annotations (as the proxy's tool cache holds them) mark them
	// readOnlyHint:true AND idempotentHint:true, and not
	// destructiveHint:true. pkg/mcp's ResponseCache applies it (Allowed
	// here is the allowlist alone).
	HonorAnnotations bool `yaml:"honor_annotations"`
}

// Normalize fills in defaults and parses TTL into TTLValue. It is safe to
// call on a nil *Config.
func (c *Config) Normalize() error {
	if c == nil {
		return nil
	}
	if c.TTL == "" {
		c.TTLValue = DefaultTTL
	} else {
		d, err := time.ParseDuration(c.TTL)
		if err != nil {
			return fmt.Errorf("response_cache: invalid ttl %q: %w", c.TTL, err)
		}
		c.TTLValue = d
	}
	if c.MaxBytes <= 0 {
		c.MaxBytes = DefaultMaxBytes
	}
	if c.MaxEntryBytes <= 0 {
		c.MaxEntryBytes = DefaultMaxEntryBytes
	}
	return nil
}

// Allowed reports whether identity ("server.tool") may be cached under the
// explicit allowlist. Entries match exactly or as a path.Match glob (e.g.
// "github.get_*"). HonorAnnotations is not consulted here: this package does
// not see tool definitions; pkg/mcp's ResponseCache checks the annotations.
func (c *Config) Allowed(identity string) bool {
	if c == nil || identity == "" {
		return false
	}
	for _, pattern := range c.Tools {
		if pattern == "" {
			continue
		}
		if pattern == identity {
			return true
		}
		if ok, err := path.Match(pattern, identity); err == nil && ok {
			return true
		}
	}
	return false
}
