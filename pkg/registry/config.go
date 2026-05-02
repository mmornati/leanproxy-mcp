package registry

import (
	"fmt"
	"io"
	"log/slog"

	"gopkg.in/yaml.v3"
)

type RegistryConfig struct {
	CompactByDefault bool `yaml:"compact_by_default"`
	MaxSignatureBytes int `yaml:"max_signature_bytes"`
}

func LoadRegistryConfig(r io.Reader) (*RegistryConfig, error) {
	var cfg RegistryConfig
	if err := yaml.NewDecoder(r).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("registry config: %w", err)
	}

	if cfg.MaxSignatureBytes == 0 {
		cfg.MaxSignatureBytes = 500
	}

	if !cfg.CompactByDefault {
		cfg.CompactByDefault = true
	}

	slog.Info("registry config loaded",
		"compact_by_default", cfg.CompactByDefault,
		"max_signature_bytes", cfg.MaxSignatureBytes)

	return &cfg, nil
}

func (c *RegistryConfig) Validate() error {
	if c.MaxSignatureBytes <= 0 {
		return fmt.Errorf("registry config: max_signature_bytes must be positive")
	}
	if c.MaxSignatureBytes > 10000 {
		slog.Warn("max_signature_bytes is quite large", "value", c.MaxSignatureBytes)
	}
	return nil
}
