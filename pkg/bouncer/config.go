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
	Enabled        bool         `yaml:"enabled"`
	CustomPatterns []PatternDef `yaml:"custom_patterns"`
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

	for _, p := range c.CustomPatterns {
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
