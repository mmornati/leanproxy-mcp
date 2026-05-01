package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Version  string          `json:"version" yaml:"version"`
	Name     string          `json:"name" yaml:"name"`
	Servers  []ServerConfig  `json:"servers" yaml:"servers"`
	Auth     *AuthConfig     `json:"auth,omitempty" yaml:"auth,omitempty"`
	Limits   *LimitsConfig   `json:"limits,omitempty" yaml:"limits,omitempty"`
	Logging  *LoggingConfig  `json:"logging,omitempty" yaml:"logging,omitempty"`
	Meta     Metadata        `json:"_meta,omitempty" yaml:"_meta,omitempty"`
}

type ServerConfig struct {
	ID       string   `json:"id" yaml:"id"`
	Command  []string `json:"command" yaml:"command"`
	Env      []string `json:"env,omitempty" yaml:"env,omitempty"`
	Port     int      `json:"port" yaml:"port"`
	Metadata Metadata `json:"_meta,omitempty" yaml:"_meta,omitempty"`
}

type AuthConfig struct {
	Token   string            `json:"token" yaml:"token"`
	Header  string            `json:"header" yaml:"header"`
	Expiry  time.Time         `json:"expiry" yaml:"expiry"`
	Scopes  []string          `json:"scopes" yaml:"scopes"`
	Meta    Metadata          `json:"_meta,omitempty" yaml:"_meta,omitempty"`
}

type LimitsConfig struct {
	MaxConnections int      `json:"maxConnections" yaml:"maxConnections"`
	TimeoutSeconds int      `json:"timeoutSeconds" yaml:"timeoutSeconds"`
	Meta           Metadata `json:"_meta,omitempty" yaml:"_meta,omitempty"`
}

type LoggingConfig struct {
	Level      string   `json:"level" yaml:"level"`
	Format     string   `json:"format" yaml:"format"`
	OutputPath string   `json:"outputPath" yaml:"outputPath"`
	Meta       Metadata `json:"_meta,omitempty" yaml:"_meta,omitempty"`
}

type Metadata struct {
	Source   string   `json:"source" yaml:"source"`
	Priority int      `json:"priority" yaml:"priority"`
	Sources  []string `json:"sources" yaml:"sources"`
}

type Layer struct {
	Source string
	Config *Config
}

type MergedConfig struct {
	Config    *Config          `json:"config"`
	Sources   map[string][]string `json:"sources"`
	Timestamp time.Time        `json:"timestamp"`
}

type ManifestMerger struct {
	logger *slog.Logger
}

func NewManifestMerger(logger *slog.Logger) *ManifestMerger {
	return &ManifestMerger{
		logger: logger,
	}
}

func (m *ManifestMerger) Merge(ctx context.Context, configs ...*Config) (*Config, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("manifest: no configs provided")
	}

	result := configs[0]
	for i := 1; i < len(configs); i++ {
		merged, err := m.deepMerge(result, configs[i])
		if err != nil {
			return nil, fmt.Errorf("manifest: merge config %d: %w", i, err)
		}
		result = merged
	}

	return result, nil
}

func (m *ManifestMerger) MergeWithLayers(ctx context.Context, layers ...Layer) (*MergedConfig, error) {
	if len(layers) == 0 {
		return nil, fmt.Errorf("manifest: no layers provided")
	}

	configs := make([]*Config, len(layers))
	for i, layer := range layers {
		configs[i] = layer.Config
	}

	merged, err := m.Merge(ctx, configs...)
	if err != nil {
		return nil, fmt.Errorf("manifest: merge layers: %w", err)
	}

	sources := make(map[string][]string)
	for _, layer := range layers {
		if layer.Source != "" {
			sources[layer.Source] = append(sources[layer.Source], layer.Source)
		}
	}

	return &MergedConfig{
		Config:    merged,
		Sources:   sources,
		Timestamp: time.Now(),
	}, nil
}

func (m *ManifestMerger) MergeFiles(ctx context.Context, paths ...string) (*Config, error) {
	configs := make([]*Config, 0, len(paths))

	for _, path := range paths {
		cfg, err := m.readConfigFile(path)
		if err != nil {
			return nil, fmt.Errorf("manifest: read file %s: %w", path, err)
		}
		configs = append(configs, cfg)
	}

	return m.Merge(ctx, configs...)
}

func (m *ManifestMerger) deepMerge(base, override *Config) (*Config, error) {
	result := &Config{
		Version: override.Version,
		Name:    override.Name,
	}

	if base.Version != "" && override.Version == "" {
		result.Version = base.Version
	}
	if base.Name != "" && override.Name == "" {
		result.Name = base.Name
	}

	if len(override.Servers) > 0 {
		result.Servers = override.Servers
	} else {
		result.Servers = base.Servers
	}

	if override.Auth != nil {
		auth := &AuthConfig{}
		if base.Auth != nil {
			auth.Token = base.Auth.Token
			auth.Header = base.Auth.Header
			auth.Expiry = base.Auth.Expiry
			auth.Scopes = base.Auth.Scopes
			auth.Meta = base.Auth.Meta
		}
		if override.Auth.Token != "" {
			auth.Token = override.Auth.Token
		}
		if override.Auth.Header != "" {
			auth.Header = override.Auth.Header
		}
		if !override.Auth.Expiry.IsZero() {
			auth.Expiry = override.Auth.Expiry
		}
		if len(override.Auth.Scopes) > 0 {
			auth.Scopes = override.Auth.Scopes
		}
		if override.Auth.Meta.Source != "" {
			auth.Meta = override.Auth.Meta
		}
		result.Auth = auth
	} else if base.Auth != nil {
		result.Auth = base.Auth
	}

	if override.Limits != nil {
		limits := &LimitsConfig{}
		if base.Limits != nil {
			limits.MaxConnections = base.Limits.MaxConnections
			limits.TimeoutSeconds = base.Limits.TimeoutSeconds
			limits.Meta = base.Limits.Meta
		}
		if override.Limits.MaxConnections > 0 {
			limits.MaxConnections = override.Limits.MaxConnections
		}
		if override.Limits.TimeoutSeconds > 0 {
			limits.TimeoutSeconds = override.Limits.TimeoutSeconds
		}
		if override.Limits.Meta.Source != "" {
			limits.Meta = override.Limits.Meta
		}
		result.Limits = limits
	} else if base.Limits != nil {
		result.Limits = base.Limits
	}

	if override.Logging != nil {
		logging := &LoggingConfig{}
		if base.Logging != nil {
			logging.Level = base.Logging.Level
			logging.Format = base.Logging.Format
			logging.OutputPath = base.Logging.OutputPath
			logging.Meta = base.Logging.Meta
		}
		if override.Logging.Level != "" {
			logging.Level = override.Logging.Level
		}
		if override.Logging.Format != "" {
			logging.Format = override.Logging.Format
		}
		if override.Logging.OutputPath != "" {
			logging.OutputPath = override.Logging.OutputPath
		}
		if override.Logging.Meta.Source != "" {
			logging.Meta = override.Logging.Meta
		}
		result.Logging = logging
	} else if base.Logging != nil {
		result.Logging = base.Logging
	}

	if override.Meta.Source != "" {
		result.Meta = override.Meta
	} else if base.Meta.Source != "" {
		result.Meta = base.Meta
	}

	return result, nil
}

func (m *ManifestMerger) readConfigFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read file: %w", err)
	}

	var cfg Config
	switch {
	case hasJSONExt(path):
		if err := json.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("manifest: parse json: %w", err)
		}
	case hasYAMLExt(path):
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("manifest: parse yaml: %w", err)
		}
	default:
		return nil, fmt.Errorf("manifest: unsupported file format: %s", path)
	}

	return &cfg, nil
}

func (m *ManifestMerger) Validate(cfg *Config) error {
	if cfg.Name == "" {
		return fmt.Errorf("manifest: config name is required")
	}
	if cfg.Version == "" {
		return fmt.Errorf("manifest: config version is required")
	}
	for _, server := range cfg.Servers {
		if server.ID == "" {
			return fmt.Errorf("manifest: server ID is required")
		}
		if len(server.Command) == 0 {
			return fmt.Errorf("manifest: server command is required for server %s", server.ID)
		}
		if server.Port < 0 || server.Port > 65535 {
			return fmt.Errorf("manifest: server port must be between 0 and 65535, got %d for server %s", server.Port, server.ID)
		}
	}
	return nil
}

func hasJSONExt(path string) bool {
	return len(path) >= 5 && (path[len(path)-5:] == ".json")
}

func hasYAMLExt(path string) bool {
	return (len(path) >= 5 && path[len(path)-5:] == ".yaml") ||
		(len(path) >= 4 && path[len(path)-4:] == ".yml")
}