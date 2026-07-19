package sidecar

import (
	"fmt"
	"strings"
)

const (
	ProviderOllama = "ollama"

	defaultOllamaURL   = "http://localhost:11434"
	defaultOllamaModel = "llama3.1:8b"
)

type Config struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	URL      string `yaml:"url"`
}

func (c *Config) Enabled() bool {
	return c != nil && strings.EqualFold(c.Provider, ProviderOllama)
}

func (c *Config) withDefaults() {
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
		return fmt.Errorf("sidecar: model must not be empty")
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
