package migrate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/mmornati/leanproxy-mcp/pkg/bouncer"
	"github.com/mmornati/leanproxy-mcp/pkg/bouncer/injection"
	"github.com/mmornati/leanproxy-mcp/pkg/utils"
)

// ErrConfigNotFound is returned by LoadConfig when the config file does not
// exist on disk. Callers that need to distinguish "operator pointed at a
// missing file" from "operator pointed at an unreadable / unparseable file"
// (or a file that exists but is empty) can use errors.Is to detect this
// sentinel. Callers that want to fall back to defaults should treat this as a
// non-error and either branch on the sentinel explicitly or rely on the
// nil-config / nil-error return contract.
var ErrConfigNotFound = errors.New("config file not found")

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

type SQLiteVectorConfig struct {
	Path string `yaml:"path"`
}

type QdrantVectorConfig struct {
	URL        string `yaml:"url"`
	APIKey     string `yaml:"api_key"`
	APIKeyEnv  string `yaml:"api_key_env"`
	Collection string `yaml:"collection"`
}

type PineconeVectorConfig struct {
	Index     string `yaml:"index"`
	APIKeyEnv string `yaml:"api_key_env"`
}

type VectorStoreConfig struct {
	Backend   string                `yaml:"backend"`
	Dimension int                   `yaml:"dimension"`
	SQLite    *SQLiteVectorConfig   `yaml:"sqlite,omitempty"`
	Qdrant    *QdrantVectorConfig   `yaml:"qdrant,omitempty"`
	Pinecone  *PineconeVectorConfig `yaml:"pinecone,omitempty"`
}

type CacheConfig struct {
	VectorStore *VectorStoreConfig `yaml:"vector_store,omitempty"`
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
	ComplexityTier      string             `yaml:"complexity_tier,omitempty"`
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

type ReconnectConfig struct {
	Enabled             *bool         `yaml:"enabled"`
	HealthInterval      string        `yaml:"health_check_interval"`
	HealthIntervalValue time.Duration `yaml:"-"`
	MaxFailures         int           `yaml:"health_check_failures"`
	MaxRestartAttempts  int           `yaml:"max_restart_attempts"`
	RestartBackoff      string        `yaml:"restart_backoff"`
	RestartBackoffValue time.Duration `yaml:"-"`
	StableWindow        string        `yaml:"stable_window"`
	StableWindowValue   time.Duration `yaml:"-"`
}

type ResolvedReconnect struct {
	Enabled            bool
	HealthInterval     time.Duration
	MaxFailures        int
	MaxRestartAttempts int
	RestartBackoff     time.Duration
	StableWindow       time.Duration
}

func (c *Config) EffectiveReconnect() ResolvedReconnect {
	r := ResolvedReconnect{
		Enabled:            true,
		HealthInterval:     30 * time.Second,
		MaxFailures:        3,
		MaxRestartAttempts: 5,
		RestartBackoff:     time.Second,
		StableWindow:       2 * time.Minute,
	}
	if c == nil || c.Reconnect == nil {
		return r
	}
	rc := c.Reconnect
	if rc.Enabled != nil {
		r.Enabled = *rc.Enabled
	}
	// health_check_interval is honored whenever explicitly set — including
	// "0", which disables the proactive health check as documented. The
	// string field (not the parsed value) is what distinguishes "unset"
	// (fall back to the 30s default) from an explicit 0.
	if rc.HealthInterval != "" {
		r.HealthInterval = rc.HealthIntervalValue
	}
	if rc.MaxFailures > 0 {
		r.MaxFailures = rc.MaxFailures
	}
	if rc.MaxRestartAttempts > 0 {
		r.MaxRestartAttempts = rc.MaxRestartAttempts
	}
	if rc.RestartBackoffValue > 0 {
		r.RestartBackoff = rc.RestartBackoffValue
	}
	if rc.StableWindowValue > 0 {
		r.StableWindow = rc.StableWindowValue
	}
	return r
}

type Config struct {
	Version      string              `yaml:"version"`
	Servers      []*ServerConfig     `yaml:"servers"`
	Reconnect    *ReconnectConfig    `yaml:"reconnect,omitempty"`
	Optimization *OptimizationConfig `yaml:"optimization,omitempty"`
	Cache        *CacheConfig        `yaml:"cache,omitempty"`
	Federation   *FederationConfig   `yaml:"federation,omitempty"`
	Injection    *injection.Config   `yaml:"injection,omitempty"`
	Bouncer      *bouncer.Config     `yaml:"bouncer,omitempty"`
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
	if c == nil {
		return nil
	}
	if c.Servers != nil {
		for _, server := range c.Servers {
			if err := server.Validate(); err != nil {
				return err
			}
		}
	}
	if c.Bouncer != nil {
		if err := validateBouncerPatterns(c.Bouncer); err != nil {
			return err
		}
	}
	return nil
}

// validateBouncerPatterns returns the first dangerous-pattern violation found
// in the bouncer block. Patterns here that fail SafeCompile are rejected at
// load time so the redactor never silently falls back to built-ins only — a
// behavior that previously let ReDoS-prone custom patterns ship to production
// without any startup signal.
func validateBouncerPatterns(cfg *bouncer.Config) error {
	if cfg == nil {
		return nil
	}
	for _, p := range cfg.AllPatternDefs() {
		if err := bouncer.ValidatePattern(p.Pattern); err != nil {
			name := p.Name
			if name == "" {
				name = "<unnamed>"
			}
			return fmt.Errorf("bouncer pattern %q: %w", name, err)
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
			return nil, fmt.Errorf("read config file %q: %w", path, ErrConfigNotFound)
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

	if cfg.Reconnect != nil {
		rc := cfg.Reconnect
		if rc.HealthInterval != "" {
			d, err := time.ParseDuration(rc.HealthInterval)
			if err != nil {
				return nil, fmt.Errorf("invalid reconnect health_check_interval duration: %w", err)
			}
			if d < 0 {
				return nil, fmt.Errorf("invalid reconnect health_check_interval: must be >= 0 (0 disables the health check)")
			}
			rc.HealthIntervalValue = d
		} else {
			rc.HealthIntervalValue = 30 * time.Second
		}

		if rc.RestartBackoff != "" {
			d, err := time.ParseDuration(rc.RestartBackoff)
			if err != nil {
				return nil, fmt.Errorf("invalid reconnect restart_backoff duration: %w", err)
			}
			if d < 0 {
				return nil, fmt.Errorf("invalid reconnect restart_backoff: must be >= 0")
			}
			rc.RestartBackoffValue = d
		} else {
			rc.RestartBackoffValue = time.Second
		}

		if rc.StableWindow != "" {
			d, err := time.ParseDuration(rc.StableWindow)
			if err != nil {
				return nil, fmt.Errorf("invalid reconnect stable_window duration: %w", err)
			}
			if d < 0 {
				return nil, fmt.Errorf("invalid reconnect stable_window: must be >= 0")
			}
			rc.StableWindowValue = d
		} else {
			rc.StableWindowValue = 2 * time.Minute
		}

		if rc.MaxFailures <= 0 {
			rc.MaxFailures = 3
		}
		if rc.MaxRestartAttempts <= 0 {
			rc.MaxRestartAttempts = 5
		}
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

	if cfg.Cache != nil && cfg.Cache.VectorStore != nil {
		vs := cfg.Cache.VectorStore
		if vs.Backend == "" {
			vs.Backend = "sqlite-vec"
		}
		if vs.Dimension <= 0 {
			vs.Dimension = 1536
		}
		if vs.SQLite != nil && vs.SQLite.Path == "" {
			home, err := os.UserHomeDir()
			if err == nil {
				vs.SQLite.Path = filepath.Join(home, ".leanproxy", "cache", "vectors.db")
			}
		}
		if vs.Qdrant != nil && vs.Qdrant.Collection == "" {
			vs.Qdrant.Collection = "leanproxy_cache"
		}
		if vs.Pinecone != nil && vs.Pinecone.APIKeyEnv == "" {
			vs.Pinecone.APIKeyEnv = "PINECONE_API_KEY"
		}
	}

	return &cfg, nil
}

func ptr(b bool) *bool { return &b }

func MarshalConfig(cfg *Config) ([]byte, error) {
	return yaml.Marshal(cfg)
}
