package migrate

import (
	"context"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mmornati/leanproxy-mcp/pkg/registry"
)

type CacheSettings struct {
	Enabled  bool   `yaml:"enabled"`
	MaxSize  int    `yaml:"max_size"`
	TTL      string `yaml:"ttl"`
	TTLValue time.Duration
}

type SummarizeSettings struct {
	Enabled        bool   `yaml:"enabled"`
	MaxTokens      int    `yaml:"max_tokens"`
	Strategy       string `yaml:"strategy"`
}

type StdioConfig struct {
	Command string   `yaml:"command"`
	Args    []string `yaml:"args"`
	Env     []string `yaml:"env"`
	CWD     string   `yaml:"cwd"`
}

type HTTPConfig struct {
	URL     string            `yaml:"url"`
	Headers map[string]string `yaml:"headers"`
}

type ServerConfig struct {
	Name               string                   `yaml:"name"`
	Enabled            *bool                    `yaml:"enabled"`
	Transport          registry.TransportType   `yaml:"transport"`
	Stdio              *StdioConfig             `yaml:"stdio,omitempty"`
	HTTP               *HTTPConfig              `yaml:"http,omitempty"`
	Timeout            string                   `yaml:"timeout"`
	TimeoutValue       time.Duration            `yaml:"-"`
	ConnectTimeout     string                   `yaml:"connect_timeout"`
	ConnectTimeoutValue time.Duration           `yaml:"-"`
	CacheSettings      *CacheSettings           `yaml:"cache_settings,omitempty"`
	SummarizeSettings  *SummarizeSettings       `yaml:"summarize_settings,omitempty"`
}

type Config struct {
	Version string         `yaml:"version"`
	Servers []*ServerConfig `yaml:"servers"`
}

func (c *ServerConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if c.Transport == "" {
		return fmt.Errorf("server %s: transport type is required", c.Name)
	}
	switch c.Transport {
	case registry.TransportStdio:
		if c.Stdio == nil || c.Stdio.Command == "" {
			return fmt.Errorf("server %s: command is required for stdio transport", c.Name)
		}
	case registry.TransportHTTP, registry.TransportSSE:
		if c.HTTP == nil || c.HTTP.URL == "" {
			return fmt.Errorf("server %s: url is required for %s transport", c.Name, c.Transport)
		}
	default:
		return fmt.Errorf("server %s: invalid transport type %q (must be stdio, http, or sse)", c.Name, c.Transport)
	}
	return nil
}

func (c *Config) Validate() error {
	if c.Servers == nil {
		return nil
	}
	for _, server := range c.Servers {
		if err := server.Validate(); err != nil {
			return err
		}
	}
	return nil
}

func LoadConfig(ctx context.Context, path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	for _, server := range cfg.Servers {
		if server.Timeout != "" {
			d, err := time.ParseDuration(server.Timeout)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid timeout duration: %w", server.Name, err)
			}
			server.TimeoutValue = d
		} else {
			server.TimeoutValue = 30 * time.Second
		}

		if server.ConnectTimeout != "" {
			d, err := time.ParseDuration(server.ConnectTimeout)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid connect_timeout duration: %w", server.Name, err)
			}
			server.ConnectTimeoutValue = d
		} else {
			server.ConnectTimeoutValue = 10 * time.Second
		}

		if server.CacheSettings != nil && server.CacheSettings.TTL != "" {
			d, err := time.ParseDuration(server.CacheSettings.TTL)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid cache TTL: %w", server.Name, err)
			}
			server.CacheSettings.TTLValue = d
		}

		if server.Enabled == nil {
			server.Enabled = ptr(true)
		}
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func ptr(b bool) *bool { return &b }
