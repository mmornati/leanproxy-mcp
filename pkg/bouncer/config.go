package bouncer

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Config defines the bouncer configuration, including whether the bouncer
// is enabled and any custom regex patterns for secret detection.
type Config struct {
	// Enabled defaults to true when omitted; set `enabled: false` to turn the
	// regex redactor off explicitly.
	Enabled *bool `yaml:"enabled,omitempty"`
	// Patterns is the documented key for custom patterns; CustomPatterns is
	// accepted as an alias for older configs. Both lists are compiled.
	Patterns       []PatternDef `yaml:"patterns,omitempty"`
	CustomPatterns []PatternDef `yaml:"custom_patterns,omitempty"`
	// SidecarAlwaysCall opts in to the per-request sidecar LLM behavior
	// introduced (and then reverted) around #274. When false (the default),
	// the sidecar is consulted only when the regex layer matched zero
	// secrets, which keeps the per-request cost to one regex pass for the
	// common case.
	SidecarAlwaysCall *bool `yaml:"sidecar_always_call,omitempty"`
}

// IsEnabled reports whether the redactor should run. A nil Config or an
// omitted `enabled` key means enabled.
func (c *Config) IsEnabled() bool {
	if c == nil || c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// ShouldAlwaysCallSidecar reports whether the sidecar LLM must run on every
// request, even when the regex layer already matched a secret. Defaults to
// false (the regex-cleaned payload is forwarded without the LLM round-trip).
func (c *Config) ShouldAlwaysCallSidecar() bool {
	if c == nil || c.SidecarAlwaysCall == nil {
		return false
	}
	return *c.SidecarAlwaysCall
}

// allPatternDefs returns the documented and legacy custom pattern lists.
func (c *Config) allPatternDefs() []PatternDef {
	if c == nil {
		return nil
	}
	return append(append([]PatternDef{}, c.Patterns...), c.CustomPatterns...)
}

// PatternDef defines a custom regex pattern for secret detection.
type PatternDef struct {
	Name    string `yaml:"name"`
	Pattern string `yaml:"pattern"`
}

// LoadedPatterns holds the compiled built-in and custom regex patterns.
type LoadedPatterns struct {
	BuiltIn []SecretPattern
	Custom  []SecretPattern
	All     []*regexp.Regexp
}

// LoadConfig reads and parses a bouncer YAML configuration from the provided reader.
func LoadConfig(r io.Reader) (*Config, error) {
	var cfg Config
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("bouncer config: %w", err)
	}
	return &cfg, nil
}

// CompilePatterns compiles built-in and custom patterns into a LoadedPatterns struct.
func (c *Config) CompilePatterns() (*LoadedPatterns, error) {
	loaded := &LoadedPatterns{
		BuiltIn: BuiltInPatterns,
	}

	for _, p := range c.allPatternDefs() {
		re, err := SafeCompile(p.Pattern)
		if err != nil {
			slog.Warn("invalid custom pattern, skipping",
				"name", p.Name,
				"pattern", p.Pattern,
				"error", err)
			continue
		}
		loaded.Custom = append(loaded.Custom, SecretPattern{
			Name:    p.Name,
			Pattern: re,
		})
		loaded.All = append(loaded.All, re)
	}

	for _, p := range BuiltInPatterns {
		loaded.All = append(loaded.All, p.Pattern)
	}

	slog.Info("patterns compiled",
		"custom_count", len(loaded.Custom),
		"builtin_count", len(loaded.BuiltIn),
		"total_count", len(loaded.All))

	return loaded, nil
}

// LoadConfigFile reads and parses a bouncer YAML configuration from the given file path.
func LoadConfigFile(path string) (*Config, error) {
	r, err := os.Open(path) // #nosec G304 -- config path provided by user
	if err != nil {
		return nil, fmt.Errorf("bouncer config file: %w", err)
	}
	defer r.Close()
	return LoadConfig(r)
}
