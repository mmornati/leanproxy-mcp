package sidecar

import (
	"fmt"
	"strings"
)

const (
	ProviderOllama = "ollama"
	ProviderMLX    = "mlx"

	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "llama3.1:8b"

	// defaultMLXModel is a documented default. MLX intentionally does NOT
	// auto-fill this in withDefaults(): an MLX provider config must declare
	// the model explicitly so users are aware the runtime is bound to
	// huggingface.co/mlx-community model ids, not Ollama's registry.
	defaultMLXModel = "mlx-community/Llama-3.2-3B-Instruct-4bit"

	// PlaceholderRedacted is the literal substituted by aggressive (non-LLM) redaction.
	// MLX uses the same placeholder until the real cgo binding to mlx-c is wired up.
	PlaceholderRedacted = "[VALUE_REDACTED]"
)

type Config struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	URL      string `yaml:"url"`
}

func (c *Config) Enabled() bool {
	if c == nil {
		return false
	}
	p := strings.TrimSpace(c.Provider)
	return strings.EqualFold(p, ProviderOllama) || strings.EqualFold(p, ProviderMLX)
}

func (c *Config) withDefaults() {
	if strings.EqualFold(strings.TrimSpace(c.Provider), ProviderMLX) {
		return
	}
	if c.URL == "" {
		c.URL = defaultOllamaURL
	}
	if c.Model == "" {
		c.Model = defaultOllamaModel
	}
}

func (c *Config) Validate() error {
	if c == nil || !c.Enabled() {
		return nil
	}
	c.withDefaults()
	if strings.TrimSpace(c.Model) == "" {
		if strings.EqualFold(strings.TrimSpace(c.Provider), ProviderMLX) {
			return fmt.Errorf("sidecar: MLX model must be configured (e.g. model: mlx-community/Llama-3.2-3B-Instruct-4bit)")
		}
		return fmt.Errorf("sidecar: model must not be empty")
	}
	if strings.EqualFold(strings.TrimSpace(c.Provider), ProviderMLX) {
		return nil
	}
	if strings.TrimSpace(c.URL) == "" {
		return fmt.Errorf("sidecar: URL must not be empty")
	}
	return nil
}

func DefaultConfig() Config {
	return Config{
		Provider: "",
	}
}
