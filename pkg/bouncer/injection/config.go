package injection

import (
	"fmt"
	"io"
	"log/slog"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Enabled        bool         `yaml:"enabled"`
	Threshold      int          `yaml:"threshold"`
	CustomPatterns []PatternDef `yaml:"custom_patterns"`
}

func DefaultConfig() Config {
	return Config{
		Enabled:   true,
		Threshold: 70,
	}
}

func LoadConfig(r io.Reader) (*Config, error) {
	var cfg Config
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("injection config: %w", err)
	}
	if cfg.Threshold <= 0 {
		slog.Warn("injection: threshold clamped to 70", "original", cfg.Threshold)
		cfg.Threshold = 70
	}
	if cfg.Threshold > 100 {
		slog.Warn("injection: threshold clamped to 100", "original", cfg.Threshold)
		cfg.Threshold = 100
	}
	return &cfg, nil
}

func LoadConfigFile(path string) (*Config, error) {
	r, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("injection config file: %w", err)
	}
	defer r.Close()
	return LoadConfig(r)
}

func (c *Config) BuildClassifier() (*Classifier, error) {
	if !c.Enabled {
		slog.Info("injection classifier disabled by configuration")
		return nil, nil
	}

	slog.Info("building injection classifier",
		"enabled", c.Enabled,
		"threshold", c.Threshold,
		"custom_patterns", len(c.CustomPatterns))

	classifier := NewClassifier()

	for _, def := range c.CustomPatterns {
		_, err := def.Compile()
		if err != nil {
			slog.Warn("injection: invalid custom pattern, skipping",
				"name", def.Name,
				"error", err)
			continue
		}
		if err := classifier.AddPattern(def); err != nil {
			slog.Warn("injection: failed to add custom pattern",
				"name", def.Name,
				"error", err)
			continue
		}
		slog.Debug("injection: added custom pattern",
			"name", def.Name,
			"weight", def.Weight,
			"enabled", def.Enabled)
	}

	return classifier, nil
}
