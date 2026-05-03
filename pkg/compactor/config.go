package compactor

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Enabled     bool   `yaml:"enabled"`
	LLMProvider string `yaml:"llm_provider"`
	LLMEndpoint string `yaml:"llm_endpoint"`
	LLMAPIKey   string `yaml:"llm_api_key"`
	LLMModel    string `yaml:"llm_model"`
	CacheDir    string `yaml:"cache_dir"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("compactor: read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("compactor: parse config: %w", err)
	}

	cfg.applyDefaults()

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.LLMProvider == "" {
		c.LLMProvider = "openai"
	}
	if c.LLMModel == "" {
		c.LLMModel = "gpt-4o-mini"
	}
	if c.LLMEndpoint == "" {
		c.LLMEndpoint = "https://api.openai.com/v1/chat/completions"
	}
	if c.CacheDir == "" {
		usr, err := os.UserHomeDir()
		if err == nil {
			c.CacheDir = filepath.Join(usr, ".config", "leanproxy", "distilled")
		}
	}
	if c.Enabled {
		c.Enabled = true
	}
}

func (c *Config) GetAPIKey() string {
	if c.LLMAPIKey != "" {
		return c.LLMAPIKey
	}
	return os.Getenv("LEANPROXY_LLM_API_KEY")
}
