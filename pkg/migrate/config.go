package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

type CacheSettings struct {
	Enabled  bool   `yaml:"enabled"`
	MaxSize  int    `yaml:"max_size"`
	TTL      string `yaml:"ttl"`
	TTLValue time.Duration
}

type SummarizeSettings struct {
	Enabled   bool   `yaml:"enabled"`
	MaxTokens int    `yaml:"max_tokens"`
	Strategy  string `yaml:"strategy"`
}

type LazyLoadingSettings struct {
	Enabled       bool          `yaml:"enabled"`
	StubTokens    int           `yaml:"stub_tokens"`
	CacheTTL      string        `yaml:"cache_ttl"`
	CacheTTLValue time.Duration `yaml:"-"`
	Prewarm       []string      `yaml:"prewarm"`
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
	Auth    *AuthConfig       `yaml:"auth,omitempty"`
}

type AuthConfig struct {
	Type         string   `yaml:"type"` // bearer, oauth2
	ClientID     string   `yaml:"client_id"`
	ClientSecret string   `yaml:"client_secret"`
	Scopes       []string `yaml:"scopes"`
	TokenURL     string   `yaml:"token_url"` // optional, for bearer token exchange
}

type ServerConfig struct {
	Name                string             `yaml:"name"`
	Enabled             *bool              `yaml:"enabled"`
	Transport           TransportType      `yaml:"transport"`
	Stdio               *StdioConfig       `yaml:"stdio,omitempty"`
	HTTP                *HTTPConfig        `yaml:"http,omitempty"`
	Timeout             string             `yaml:"timeout"`
	TimeoutValue        time.Duration      `yaml:"-"`
	ConnectTimeout      string             `yaml:"connect_timeout"`
	ConnectTimeoutValue time.Duration      `yaml:"-"`
	IdleTimeout         string             `yaml:"idle_timeout"`
	IdleTimeoutValue    time.Duration      `yaml:"-"`
	CacheSettings       *CacheSettings     `yaml:"cache_settings,omitempty"`
	SummarizeSettings   *SummarizeSettings `yaml:"summarize_settings,omitempty"`
}

type Config struct {
	Version      string              `yaml:"version"`
	Servers      []*ServerConfig     `yaml:"servers"`
	Optimization *OptimizationConfig `yaml:"optimization,omitempty"`
	Federation   *FederationConfig   `yaml:"federation,omitempty"`
}

type OptimizationConfig struct {
	LazyLoading *LazyLoadingSettings `yaml:"lazy_loading,omitempty"`
}

type PeerConfig struct {
	Name      string `yaml:"name"`
	URL       string `yaml:"url"`
	AuthToken string `yaml:"auth_token,omitempty"`
}

type FederationConfig struct {
	Enabled bool          `yaml:"enabled"`
	Peers   []*PeerConfig `yaml:"peers"`
}

func (c *ServerConfig) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server name is required")
	}
	if c.Transport == "" {
		return fmt.Errorf("server %s: transport type is required", c.Name)
	}
	switch c.Transport {
	case TransportStdio:
		if c.Stdio == nil || c.Stdio.Command == "" {
			return fmt.Errorf("server %s: command is required for stdio transport", c.Name)
		}
	case TransportHTTP, TransportSSE:
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
	baseDir := filepath.Dir(filepath.Clean(path))
	if err := utils.ValidatePath(path, baseDir); err != nil {
		return nil, fmt.Errorf("path validation: %w", err)
	}

	data, err := os.ReadFile(path) // #nosec G304 -- path validated via ValidatePath above
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

		if server.IdleTimeout != "" {
			d, err := time.ParseDuration(server.IdleTimeout)
			if err != nil {
				return nil, fmt.Errorf("server %s: invalid idle_timeout duration: %w", server.Name, err)
			}
			server.IdleTimeoutValue = d
		} else {
			server.IdleTimeoutValue = 30 * time.Minute
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

	if cfg.Optimization != nil && cfg.Optimization.LazyLoading != nil {
		lazy := cfg.Optimization.LazyLoading
		if lazy.CacheTTL != "" {
			d, err := time.ParseDuration(lazy.CacheTTL)
			if err != nil {
				return nil, fmt.Errorf("invalid lazy_loading cache_ttl: %w", err)
			}
			lazy.CacheTTLValue = d
		} else {
			lazy.CacheTTLValue = 24 * time.Hour
		}

		if lazy.StubTokens == 0 {
			lazy.StubTokens = 54
		}
	}

	return &cfg, nil
}

func ptr(b bool) *bool { return &b }

func MarshalConfig(cfg *Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
